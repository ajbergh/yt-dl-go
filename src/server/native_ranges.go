package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
)

var (
	errStreamAuthorization = errors.New("media authorization was rejected; retry with a refreshed session")
	errStreamExpired       = errors.New("media source expired before transfer completed")
	errStreamRateLimited   = errors.New("media server rate limited the transfer; retry later")
)

type nativeRangeFailure struct {
	status  int
	delay   time.Duration
	expired bool
}

func (failure *nativeRangeFailure) Error() string {
	switch {
	case failure.status == 429:
		return errStreamRateLimited.Error()
	case failure.expired:
		return errStreamExpired.Error()
	case failure.status == 401 || failure.status == 403:
		return errStreamAuthorization.Error()
	}
	return errRead.Error()
}
func (failure *nativeRangeFailure) Unwrap() []error {
	causes := []error{errRead}
	switch {
	case failure.status == 429:
		causes = append(causes, errStreamRateLimited)
	case failure.expired:
		causes = append(causes, errStreamExpired)
	case failure.status == 401 || failure.status == 403:
		causes = append(causes, errStreamAuthorization)
	}
	return causes
}
func retryAfterDelay(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		if seconds > 3600 {
			return time.Hour
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
func sourceURLExpired(raw string, now time.Time) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	seconds, err := strconv.ParseInt(parsed.Query().Get("expire"), 10, 64)
	return err == nil && !time.Unix(seconds, 0).After(now)
}

type nativeHostSlot struct {
	users int
	slots chan struct{}
}

// Bound each transfer to four 8 MiB range buffers and share the same four-request
// host limit across concurrent jobs. Smaller sources only request what remains.
const nativeRangeConcurrency = 4

var nativeHosts = struct {
	sync.Mutex
	entries map[string]*nativeHostSlot
}{entries: make(map[string]*nativeHostSlot)}

func acquireNativeHost(ctx context.Context, host string) (func(), error) {
	nativeHosts.Lock()
	slot := nativeHosts.entries[host]
	if slot == nil {
		slot = &nativeHostSlot{slots: make(chan struct{}, nativeRangeConcurrency)}
		nativeHosts.entries[host] = slot
	}
	slot.users++
	nativeHosts.Unlock()
	put := func() {
		nativeHosts.Lock()
		slot.users--
		if slot.users == 0 {
			delete(nativeHosts.entries, host)
		}
		nativeHosts.Unlock()
	}
	select {
	case slot.slots <- struct{}{}:
		return func() { <-slot.slots; put() }, nil
	case <-ctx.Done():
		put()
		return nil, ctx.Err()
	}
}

type nativeRangeChunk struct {
	start, end int64
	data       []byte
	elapsed    time.Duration
}

func (s *server) readNativeRange(ctx context.Context, client *http.Client, profile youtubeClientProfile, format *youtube.Format, chunk *nativeRangeChunk) error {
	parsed, err := url.Parse(format.URL)
	if err != nil {
		return errRead
	}
	query := parsed.Query()
	query.Del("range")
	parsed.RawQuery = query.Encode()
	release, err := acquireNativeHost(ctx, parsed.Host)
	if err != nil {
		return err
	}
	defer release()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return errRead
	}
	request.Header.Set("User-Agent", profile.client.UserAgent)
	request.Header.Set("Origin", profile.origin)
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunk.start, chunk.end))
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &nativeRangeFailure{}
	}
	defer response.Body.Close()
	if !validRangeResponse(response, chunk.start, chunk.end, format.ContentLength) {
		return &nativeRangeFailure{status: response.StatusCode, delay: retryAfterDelay(response.Header.Get("Retry-After"), time.Now()), expired: sourceURLExpired(format.URL, time.Now())}
	}
	reader := idleDeadlineReader{reader: response.Body, timeout: transferIdleTimeout, onIdle: func() { _ = response.Body.Close() }}
	// The range length is known and bounded. Avoid ReadAll's repeated growth
	// and copies for each concurrent chunk; keep one extra byte to reject an
	// oversized response rather than silently accepting a valid-looking prefix.
	expected := chunk.end - chunk.start + 1
	data := make([]byte, expected+1)
	n, err := io.ReadFull(s.bandwidthReader(ctx, reader), data)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != io.ErrUnexpectedEOF || int64(n) != expected {
		return &nativeRangeFailure{}
	}
	chunk.data, chunk.elapsed = data[:n], time.Since(started)
	return nil
}

func (s *server) readNativeWindow(ctx context.Context, client *http.Client, profile youtubeClientProfile, format *youtube.Format, start int64) ([]nativeRangeChunk, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunks := make([]nativeRangeChunk, 0, nativeRangeConcurrency)
	for offset := start; offset < format.ContentLength && len(chunks) < nativeRangeConcurrency; {
		end := min(offset+adaptiveRangeChunkSize, format.ContentLength) - 1
		chunks = append(chunks, nativeRangeChunk{start: offset, end: end})
		offset = end + 1
	}
	var wg sync.WaitGroup
	failures := make(chan error, 1)
	for index := range chunks {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := s.readNativeRange(ctx, client, profile, format, &chunks[index]); err != nil {
				select {
				case failures <- err:
				default:
				}
				cancel()
			}
		}(index)
	}
	wg.Wait()
	select {
	case err := <-failures:
		return nil, err
	default:
		return chunks, nil
	}
}

func shouldRefreshSlowRange(bytes int64, elapsed time.Duration, bandwidthLimit int64) bool {
	return bandwidthLimit == 0 && elapsed >= 10*time.Second && float64(bytes)/elapsed.Seconds() < 100*1024
}

func (s *server) downloadNativeRangeWindows(ctx context.Context, engine nativeClient, videoID string, selected *youtube.Format, file *os.File, start int64, profileID string, checkpoint func(int64) error) (int64, error) {
	client := s.rangeHTTPClient
	if client == nil {
		client = nativeHTTPClient(0)
	}
	current := selected
	refreshedSlow := false
	for start < selected.ContentLength {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		var chunks []nativeRangeChunk
		var err error
		for attempt := 0; attempt < adaptiveRangeAttempts; attempt++ {
			profile := youtubeClientProfile{id: "android", client: youtube.AndroidClient, origin: "https://youtube.com"}
			if source, ok := engine.(interface{ RequestProfile() youtubeClientProfile }); ok {
				profile = source.RequestProfile()
			}
			chunks, err = s.readNativeWindow(ctx, client, profile, current, start)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return start, ctx.Err()
			}
			var failure *nativeRangeFailure
			errors.As(err, &failure)
			if attempt+1 == adaptiveRangeAttempts {
				return start, err
			}
			if failure != nil && (failure.status == 401 || failure.status == 403) && (attempt > 0 || engine == nil) {
				return start, err
			}
			delay := adaptiveRangeRetryDelay(attempt + 1)
			if failure != nil && failure.delay > delay {
				delay = failure.delay
			}
			// Long Retry-After values are surfaced instead of being shortened.
			if delay > 2*time.Minute {
				return start, err
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return start, ctx.Err()
			case <-timer.C:
			}
			refresh := failure == nil || failure.status == 0 || failure.expired || failure.status == 401 || failure.status == 403 || failure.status == 404 || failure.status == 410
			if refresh && engine != nil {
				current, err = s.refreshMatchingFormat(ctx, engine, videoID, selected, profileID)
				if err != nil {
					return start, err
				}
				if nativeProfileID(engine) != profileID {
					return start, errRead
				}
			}
		}
		if err != nil {
			return start, err
		}
		windowEnd := start
		for _, chunk := range chunks {
			written, writeErr := file.WriteAt(chunk.data, chunk.start)
			if writeErr != nil || written != len(chunk.data) {
				return start, errStorage
			}
			windowEnd = chunk.end + 1
			limit := int64(0)
			if s.bandwidth != nil {
				limit = s.bandwidth.Limit()
			}
			if !refreshedSlow && engine != nil && shouldRefreshSlowRange(int64(len(chunk.data)), chunk.elapsed, limit) {
				refreshedSlow = true
				if refreshed, refreshErr := s.refreshMatchingFormat(ctx, engine, videoID, selected, profileID); refreshErr == nil && nativeProfileID(engine) == profileID {
					current = refreshed
				}
			}
		}
		// Publish only a contiguous, durable prefix. One sync/checkpoint per
		// window avoids serial disk flushes between already downloaded chunks.
		if err := file.Sync(); err != nil {
			return start, errStorage
		}
		if err := checkpoint(windowEnd); err != nil {
			return start, err
		}
		start = windowEnd
	}
	return start, nil
}
