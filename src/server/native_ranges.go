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
	"strings"
	"sync"
	"sync/atomic"
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

// Share a four-request host limit across concurrent jobs. Audio uses smaller
// ranges to make useful parallel progress even on a short track.
const nativeRangeConcurrency = 4

const audioRangeMaxChunkSize int64 = 1024 * 1024

func nativeRangeChunkSize(format *youtube.Format, total int64) int64 {
	if format == nil {
		return adaptiveRangeChunkSize
	}
	kind, _, ok := formatType(format)
	if !ok || !strings.HasPrefix(kind, "audio/") {
		return adaptiveRangeChunkSize
	}
	// Avoid extra request setup for very small sources. For ordinary tracks,
	// aim for four initial requests without producing tiny ranges on long ones.
	if total > 0 && total <= 512*1024 {
		return total
	}
	const alignment int64 = 64 * 1024
	size := max(int64(256*1024), min(audioRangeMaxChunkSize, (total-1)/nativeRangeConcurrency+1))
	return ((size + alignment - 1) / alignment) * alignment
}

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

// Keep small/audio and video buffers separate so an audio worker never retains
// an 8 MiB allocation from a previous video transfer.
var nativeRangeBuffers, nativeAudioRangeBuffers sync.Pool

func getNativeRangeBuffer(size int64) []byte {
	pool, capacity := &nativeRangeBuffers, adaptiveRangeChunkSize+1
	if size <= audioRangeMaxChunkSize+1 {
		pool, capacity = &nativeAudioRangeBuffers, audioRangeMaxChunkSize+1
	}
	if pooled, ok := pool.Get().(*[]byte); ok && int64(cap(*pooled)) >= size {
		return (*pooled)[:size]
	}
	return make([]byte, size, max(size, capacity))
}

func putNativeRangeBuffer(buffer []byte) {
	switch int64(cap(buffer)) {
	case adaptiveRangeChunkSize + 1:
		buffer = buffer[:0]
		nativeRangeBuffers.Put(&buffer)
	case audioRangeMaxChunkSize + 1:
		buffer = buffer[:0]
		nativeAudioRangeBuffers.Put(&buffer)
	}
}

func (s *server) readNativeRange(ctx context.Context, client *http.Client, profile youtubeClientProfile, format *youtube.Format, chunk *nativeRangeChunk, progress ...func(int64)) error {
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
	data := getNativeRangeBuffer(expected + 1)
	var notify func(int64)
	if len(progress) > 0 {
		notify = progress[0]
	}
	n, err := io.ReadFull(nativeByteReader{reader: s.bandwidthReader(ctx, reader), progress: notify}, data)
	if ctx.Err() != nil {
		putNativeRangeBuffer(data)
		return ctx.Err()
	}
	if err != io.ErrUnexpectedEOF || int64(n) != expected {
		putNativeRangeBuffer(data)
		return &nativeRangeFailure{}
	}
	chunk.data, chunk.elapsed = data[:n], time.Since(started)
	return nil
}

func shouldRefreshSlowRange(bytes int64, elapsed time.Duration, bandwidthLimit int64) bool {
	return bandwidthLimit == 0 && elapsed >= 10*time.Second && float64(bytes)/elapsed.Seconds() < 100*1024
}

// nativeStopError marks a failure retrying cannot fix (storage or checkpoint
// persistence) so the retry loop returns it unchanged.
type nativeStopError struct{ err error }

func (e nativeStopError) Error() string { return e.err.Error() }
func (e nativeStopError) Unwrap() error { return e.err }

// runNativeRangePipeline keeps nativeRangeConcurrency range requests in flight
// continuously: a worker that finishes a chunk writes it at its offset and
// immediately claims the next one, so one slow chunk no longer idles the other
// slots and disk syncs never stall the network. Only the contiguous prefix of
// written chunks is synced and checkpointed, preserving resume semantics. It
// returns the durable contiguous offset even when it fails.
func (s *server) runNativeRangePipeline(ctx context.Context, client *http.Client, profile youtubeClientProfile, format func() *youtube.Format, onSlow func(), file *os.File, start, total int64, checkpoint func(int64) error, progress ...func(int64)) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunkSize := nativeRangeChunkSize(format(), total)
	type rangeResult struct {
		start, end int64
		elapsed    time.Duration
		err        error
	}
	results := make(chan rangeResult, 4*nativeRangeConcurrency)
	var cursor atomic.Int64
	cursor.Store(start)
	var workers sync.WaitGroup
	for range nativeRangeConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				first := cursor.Add(chunkSize) - chunkSize
				if first >= total {
					return
				}
				chunk := nativeRangeChunk{start: first, end: min(first+chunkSize, total) - 1}
				var bytesProgress func(int64)
				if len(progress) > 1 {
					bytesProgress = progress[1]
				}
				err := s.readNativeRange(ctx, client, profile, format(), &chunk, bytesProgress)
				if err == nil {
					written, writeErr := file.WriteAt(chunk.data, chunk.start)
					putNativeRangeBuffer(chunk.data)
					if writeErr != nil || written != int(chunk.end-chunk.start+1) {
						err = nativeStopError{errStorage}
					}
				}
				results <- rangeResult{start: chunk.start, end: chunk.end + 1, elapsed: chunk.elapsed, err: err}
				if err != nil {
					return
				}
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()

	completed := make(map[int64]int64, 2*nativeRangeConcurrency)
	prefix, checkpointed := start, start
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
		cancel()
	}
	flush := func() error {
		if prefix == checkpointed {
			return nil
		}
		// Sync covers every chunk already written, including those past the
		// prefix; only the prefix is published.
		if err := file.Sync(); err != nil {
			return nativeStopError{errStorage}
		}
		if err := checkpoint(prefix); err != nil {
			return nativeStopError{err}
		}
		checkpointed = prefix
		return nil
	}
	slowChecked := false
	for result := range results {
		if result.err != nil {
			fail(result.err)
			continue
		}
		previousPrefix := prefix
		completed[result.start] = result.end
		for end, ok := completed[prefix]; ok; end, ok = completed[prefix] {
			delete(completed, prefix)
			prefix = end
		}
		if firstErr != nil {
			continue
		}
		// This is verified, written progress, not a durable resume checkpoint.
		// Startup detection need not wait for a whole checkpoint window.
		if prefix > previousPrefix && len(progress) > 0 && progress[0] != nil {
			progress[0](prefix)
		}
		if !slowChecked && onSlow != nil {
			limit := int64(0)
			if s.bandwidth != nil {
				limit = s.bandwidth.Limit()
			}
			if shouldRefreshSlowRange(result.end-result.start, result.elapsed, limit) {
				slowChecked = true
				onSlow()
			}
		}
		if prefix-checkpointed >= nativeRangeConcurrency*chunkSize || prefix >= total {
			if err := flush(); err != nil {
				fail(err)
			}
		}
	}
	// Persist whatever contiguous progress survived so a retry or resume does
	// not refetch it.
	if err := flush(); err != nil && firstErr == nil {
		firstErr = err
	}
	return prefix, firstErr
}

type nativeRangeObserver struct {
	failure       func(error)
	progress      func(int64)
	bytesProgress func(int64)
}

func (s *server) downloadNativeRangeWindows(ctx context.Context, engine nativeClient, videoID string, selected *youtube.Format, file *os.File, start int64, profileID string, checkpoint func(int64) error, observers ...nativeRangeObserver) (int64, error) {
	var observer nativeRangeObserver
	if len(observers) > 0 {
		observer = observers[0]
	}
	client := s.rangeHTTPClient
	if client == nil {
		client = nativeHTTPClient(0)
	}
	var formatLock sync.RWMutex
	current := selected
	currentFormat := func() *youtube.Format {
		formatLock.RLock()
		defer formatLock.RUnlock()
		return current
	}
	setFormat := func(format *youtube.Format) {
		formatLock.Lock()
		current = format
		formatLock.Unlock()
	}
	var onSlow func()
	if engine != nil {
		refreshedSlow := false
		onSlow = func() {
			if refreshedSlow {
				return
			}
			refreshedSlow = true
			if refreshed, refreshErr := s.refreshMatchingFormat(ctx, engine, videoID, selected, profileID); refreshErr == nil && nativeProfileID(engine) == profileID {
				setFormat(refreshed)
			}
		}
	}
	attempt := 0
	for start < selected.ContentLength {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		profile := youtubeClientProfile{id: "android", client: youtube.AndroidClient, origin: "https://youtube.com"}
		if source, ok := engine.(interface{ RequestProfile() youtubeClientProfile }); ok {
			profile = source.RequestProfile()
		}
		next, err := s.runNativeRangePipeline(ctx, client, profile, currentFormat, onSlow, file, start, selected.ContentLength, checkpoint, observer.progress, observer.bytesProgress)
		if next > start {
			// Progress was made and checkpointed; retry budget starts over.
			attempt = 0
		}
		start = next
		if err == nil {
			continue
		}
		if observer.failure != nil {
			observer.failure(err)
		}
		if ctx.Err() != nil {
			return start, ctx.Err()
		}
		var stop nativeStopError
		if errors.As(err, &stop) {
			return start, stop.err
		}
		var failure *nativeRangeFailure
		errors.As(err, &failure)
		if attempt+1 >= adaptiveRangeAttempts {
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
		attempt++
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return start, ctx.Err()
		case <-timer.C:
		}
		refresh := failure == nil || failure.status == 0 || failure.expired || failure.status == 401 || failure.status == 403 || failure.status == 404 || failure.status == 410
		if refresh && engine != nil {
			refreshed, refreshErr := s.refreshMatchingFormat(ctx, engine, videoID, selected, profileID)
			if refreshErr != nil {
				return start, refreshErr
			}
			setFormat(refreshed)
			if nativeProfileID(engine) != profileID {
				return start, errRead
			}
		}
	}
	return start, nil
}

// Byte arrival proves transport liveness before a complete range can be verified.
// It must never be treated as a durable checkpoint or published media progress.
type nativeByteReader struct {
	reader   io.Reader
	progress func(int64)
}

func (r nativeByteReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.progress != nil {
		r.progress(int64(n))
	}
	return n, err
}
