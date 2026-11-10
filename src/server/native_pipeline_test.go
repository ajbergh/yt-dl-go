package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func pipelinePayload(size int64) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i*31 + i>>8)
	}
	return payload
}

// rangeFixture serves payload with random per-request latency so chunks finish
// out of order. failChunk, when non-nil, decides whether a range start gets a
// 503.
func rangeFixture(payload []byte, failChunk func(start int64) bool) *httptest.Server {
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= int64(len(payload)) || end < start {
			http.Error(w, "invalid range", http.StatusBadRequest)
			return
		}
		select {
		case <-time.After(time.Duration(rand.IntN(30)) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		if failChunk != nil && failChunk(start) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	// A cancelled pipeline may leave a peer that no longer reads the response.
	// Bound the fixture's writes so Server.Close cannot hang during race tests.
	fixture.Config.WriteTimeout = 5 * time.Second
	fixture.Start()
	return fixture
}

func TestNativeRangePipelineWritesOutOfOrderChunksContiguously(t *testing.T) {
	payload := pipelinePayload(11*adaptiveRangeChunkSize + 4097)
	fixture := rangeFixture(payload, nil)
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	file, err := os.Create(filepath.Join(t.TempDir(), "media.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var checkpoints []int64
	format := &youtube.Format{URL: fixture.URL, ContentLength: int64(len(payload))}
	written, err := s.downloadNativeRangeWindows(context.Background(), nil, "sample", format, file, 0, "", func(offset int64) error {
		checkpoints = append(checkpoints, offset)
		return nil
	})
	if err != nil || written != int64(len(payload)) {
		t.Fatalf("written=%d err=%v", written, err)
	}
	for i, offset := range checkpoints {
		if offset <= 0 || (i > 0 && offset <= checkpoints[i-1]) {
			t.Fatalf("checkpoints must strictly increase: %v", checkpoints)
		}
	}
	if len(checkpoints) == 0 || checkpoints[len(checkpoints)-1] != int64(len(payload)) {
		t.Fatalf("final checkpoint must cover the whole file: %v", checkpoints)
	}
	got, err := os.ReadFile(file.Name())
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("file content differs from source (err=%v, len=%d)", err, len(got))
	}
}

func TestNativeRangePipelineRetriesFromDurablePrefix(t *testing.T) {
	payload := pipelinePayload(7*adaptiveRangeChunkSize + 99)
	var failed atomic.Bool
	var mu sync.Mutex
	requested := map[int64]int{}
	fixture := rangeFixture(payload, func(start int64) bool {
		mu.Lock()
		requested[start]++
		mu.Unlock()
		return start == 3*adaptiveRangeChunkSize && failed.CompareAndSwap(false, true)
	})
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	file, err := os.Create(filepath.Join(t.TempDir(), "media.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var last int64
	format := &youtube.Format{URL: fixture.URL, ContentLength: int64(len(payload))}
	written, err := s.downloadNativeRangeWindows(context.Background(), nil, "sample", format, file, 0, "", func(offset int64) error {
		if offset <= last {
			t.Errorf("checkpoint went backwards: %d after %d", offset, last)
		}
		last = offset
		return nil
	})
	if err != nil || written != int64(len(payload)) {
		t.Fatalf("written=%d err=%v", written, err)
	}
	got, _ := os.ReadFile(file.Name())
	if !bytes.Equal(got, payload) {
		t.Fatal("recovered file differs from source")
	}
	mu.Lock()
	defer mu.Unlock()
	// Only the failed chunk plus requests still in flight when it failed may
	// be repeated; a retry must not restart the whole transfer.
	total := 0
	for _, count := range requested {
		total += count
	}
	if chunks := 8; total > chunks+1+nativeRangeConcurrency {
		t.Fatalf("retry refetched too much: %d requests for %d chunks: %v", total, chunks, requested)
	}
}

func TestNativeRangePipelineKeepsPrefixWhenRetriesExhausted(t *testing.T) {
	payload := pipelinePayload(6*adaptiveRangeChunkSize + 5)
	fixture := rangeFixture(payload, func(start int64) bool { return start == 2*adaptiveRangeChunkSize })
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	file, err := os.Create(filepath.Join(t.TempDir(), "media.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var last int64
	format := &youtube.Format{URL: fixture.URL, ContentLength: int64(len(payload))}
	written, err := s.downloadNativeRangeWindows(context.Background(), nil, "sample", format, file, 0, "", func(offset int64) error {
		last = offset
		return nil
	})
	if !errors.Is(err, errRead) {
		t.Fatalf("expected a read failure, got %v", err)
	}
	// Chunks 0 and 1 may or may not land before chunk 2's failure cancels the
	// pipeline, but the durable prefix must never reach the failed chunk and
	// must equal the last checkpoint.
	if written > 2*adaptiveRangeChunkSize || written%adaptiveRangeChunkSize != 0 || last != written {
		t.Fatalf("durable prefix = %d, last checkpoint = %d, want <= %d", written, last, 2*adaptiveRangeChunkSize)
	}
	got, _ := os.ReadFile(file.Name())
	if len(got) < int(written) || !bytes.Equal(got[:written], payload[:written]) {
		t.Fatal("checkpointed prefix is not intact")
	}
}

func TestNativeRangePipelineStopsOnStorageFailure(t *testing.T) {
	payload := pipelinePayload(3 * adaptiveRangeChunkSize)
	fixture := rangeFixture(payload, nil)
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	file, err := os.Create(filepath.Join(t.TempDir(), "media.part"))
	if err != nil {
		t.Fatal(err)
	}
	file.Close() // every WriteAt now fails
	format := &youtube.Format{URL: fixture.URL, ContentLength: int64(len(payload))}
	_, err = s.downloadNativeRangeWindows(context.Background(), nil, "sample", format, file, 0, "", func(int64) error { return nil })
	if !errors.Is(err, errStorage) {
		t.Fatalf("expected storage error, got %v", err)
	}
}
