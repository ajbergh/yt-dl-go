package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
)

const audioNativeStartupTimeout = 20 * time.Second
const audioAuthorizationCooldown = 2 * time.Minute

var errAudioNativeStartup = errors.New("native audio source did not make initial progress")

// Authorization failures are shared across playlist items using the same
// delivery host, client profile and audio format. Entries expire quickly so a
// recovered native route is tried again; neither signed URLs nor credentials
// are retained here.
type audioTransportState struct {
	mu       sync.Mutex
	rejected map[string]time.Time
}

func audioTransportKey(engine nativeClient, format *youtube.Format) string {
	parsed, err := url.Parse(format.URL)
	if err != nil {
		return ""
	}
	return parsed.Host + "\x00" + nativeProfileID(engine) + "\x00" + strconv.Itoa(format.ItagNo)
}

func (state *audioTransportState) prefersBrowser(key string, now time.Time) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	for candidate, expiry := range state.rejected {
		if !expiry.After(now) {
			delete(state.rejected, candidate)
		}
	}
	return state.rejected[key].After(now)
}

func (state *audioTransportState) record(key string, err error, now time.Time) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if err == nil {
		delete(state.rejected, key)
		return
	}
	if key == "" || !errors.Is(err, errStreamAuthorization) {
		return
	}
	if state.rejected == nil {
		state.rejected = make(map[string]time.Time)
	}
	if len(state.rejected) >= 64 {
		var oldest string
		var earliest time.Time
		for candidate, expiry := range state.rejected {
			if earliest.IsZero() || expiry.Before(earliest) {
				oldest, earliest = candidate, expiry
			}
		}
		delete(state.rejected, oldest)
	}
	state.rejected[key] = now.Add(audioAuthorizationCooldown)
}

func retryAudioWithBrowser(err error) bool {
	if errors.Is(err, errStreamRateLimited) || errors.Is(err, errStorage) || errors.Is(err, errLimit) || errors.Is(err, errItemReservation) {
		return false
	}
	return errors.Is(err, errAudioNativeStartup) || errors.Is(err, errRead) || errors.Is(err, errMetadata)
}

func (s *server) downloadAudioSource(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, path, partKey string, itemIndex int, budget int64, progress func(int64)) (int64, error) {
	started := time.Now()
	duration, _ := strconv.ParseInt(format.ApproxDurationMs, 10, 64)
	canBrowser := s.browserFactory != nil && !s.bandwidthLimited() && duration > 0
	key := audioTransportKey(engine, format)
	var provider browserMediaProvider
	resumeBrowser := false
	resumeNative := false
	if canBrowser && s.store != nil {
		part, err := s.store.loadPart(j.ID, itemIndex, partKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, errStorage
		}
		// The capture helper validates identity and bytes before resuming. Select
		// its persisted method first so a restart cannot discard browser progress.
		resumeBrowser = err == nil && part.Method == "browser-sabr" && part.Path == path
		resumeNative = err == nil && part.Method == "native-range" && part.Path == path && part.CompletedBytes > 0
	}
	if canBrowser && (resumeBrowser || (!resumeNative && s.audioTransport.prefersBrowser(key, time.Now()))) {
		provider = s.acquireAdaptiveBrowser(ctx)
		if resumeBrowser && provider == nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, errBrowserUnavailable
		}
	}
	var nativeErr error
	if provider == nil {
		// Bound only startup when browser recovery is possible. Once a verified
		// prefix arrives, normal transfer idle deadlines apply. Capped native
		// downloads keep their full budget and never start unmetered Chrome.
		nativeCtx := ctx
		stopTimer := func() {}
		var authorizationErr, rateLimitErr error
		if canBrowser {
			probeCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			timer := time.AfterFunc(audioNativeStartupTimeout, func() { cancel(errAudioNativeStartup) })
			defer timer.Stop()
			nativeCtx = probeCtx
			stopTimer = func() { timer.Stop() }
		}
		size, _, err := s.downloadAdaptiveRanges(nativeCtx, j, engine, video, format, path, partKey, itemIndex, budget, func(written int64) {
			if written > 0 {
				stopTimer()
			}
			if progress != nil {
				progress(written)
			}
		}, nil, adaptiveRangePolicy{onNativeBytes: func(int64) { stopTimer() }, onNativeProgress: func(int64) { stopTimer() }, onNativeFailure: func(err error) {
			if errors.Is(err, errStreamAuthorization) {
				authorizationErr = err
			}
			if errors.Is(err, errStreamRateLimited) {
				rateLimitErr = err
				// An explicit server delay takes precedence over startup probing.
				stopTimer()
			}
		}})
		stopTimer()
		if ctx.Err() != nil {
			return size, ctx.Err()
		}
		if errors.Is(context.Cause(nativeCtx), errAudioNativeStartup) {
			if rateLimitErr != nil {
				err = rateLimitErr
			} else {
				err = errAudioNativeStartup
			}
		}
		cacheErr := err
		if err != nil && authorizationErr != nil {
			cacheErr = authorizationErr
		}
		s.audioTransport.record(key, cacheErr, time.Now())
		if err == nil || !canBrowser || !retryAudioWithBrowser(err) {
			if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
				log.Printf("audio acquisition transport=native bytes=%d elapsed=%s error=%v", size, time.Since(started).Round(time.Millisecond), err)
			}
			return size, err
		}
		nativeErr = err
		provider = s.acquireAdaptiveBrowser(ctx)
		if provider == nil {
			return size, err
		}
	}
	defer provider.Close()
	// Native MP4 ranges and browser SABR fragments must never be appended to
	// each other. Browser checkpoints, when already selected by the cooldown,
	// still resume through the regular browser checkpoint machinery.
	if nativeErr != nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, errStorage
		}
		if err := s.deleteDownloadPart(j, itemIndex, partKey); err != nil {
			return 0, errStorage
		}
	}
	size, browserUsed, err := s.downloadAdaptiveRanges(ctx, j, engine, video, format, path, partKey, itemIndex, budget, progress, provider, adaptiveRangePolicy{nativeFailure: nativeErr})
	if err == nil && !browserUsed {
		s.audioTransport.record(key, nil, time.Now())
	}
	if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
		log.Printf("audio acquisition browser=%t bytes=%d elapsed=%s error=%v", browserUsed, size, time.Since(started).Round(time.Millisecond), err)
	}
	return size, err
}
