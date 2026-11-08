package main

import (
	"bytes"
	"context"
	"fmt"
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

func TestAudioRangeProfileKeepsVideoDefaultsAndSmallBuffers(t *testing.T) {
	for _, tc := range []struct {
		mime        string
		total, want int64
	}{
		{"audio/mp4", 200 * 1024, 200 * 1024},
		{"audio/mp4", 1024 * 1024, 256 * 1024},
		{"audio/webm", 7 * 1024 * 1024, audioRangeMaxChunkSize},
		{"video/mp4", 7 * 1024 * 1024, adaptiveRangeChunkSize},
		{"", 1024 * 1024, adaptiveRangeChunkSize},
	} {
		if got := nativeRangeChunkSize(&youtube.Format{MimeType: tc.mime}, tc.total); got != tc.want {
			t.Errorf("mime=%q total=%d chunk=%d want=%d", tc.mime, tc.total, got, tc.want)
		}
	}
	// A previously pooled video buffer must not inflate audio worker memory.
	video := getNativeRangeBuffer(adaptiveRangeChunkSize + 1)
	putNativeRangeBuffer(video)
	audio := getNativeRangeBuffer(256*1024 + 1)
	defer putNativeRangeBuffer(audio)
	if int64(cap(audio)) != audioRangeMaxChunkSize+1 {
		t.Fatalf("audio retained %d bytes of capacity", cap(audio))
	}
}

func TestAudioRangePipelineParallelResumePreservesPrefix(t *testing.T) {
	payload := pipelinePayload(3*1024*1024 + 111)
	const resume = 12345
	var requests atomic.Int32
	ready := make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < resume || end >= int64(len(payload)) || end < start || end-start+1 > audioRangeMaxChunkSize {
			t.Errorf("invalid audio range %q", r.Header.Get("Range"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if requests.Add(1) == nativeRangeConcurrency {
			close(ready)
		}
		select {
		case <-ready:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer fixture.Close()
	file, err := os.Create(filepath.Join(t.TempDir(), "audio.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(payload[:resume]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &server{rangeHTTPClient: fixture.Client()}
	format := &youtube.Format{URL: fixture.URL, MimeType: `audio/mp4; codecs="mp4a.40.2"`, ContentLength: int64(len(payload))}
	last := int64(resume)
	written, err := s.downloadNativeRangeWindows(ctx, nil, "sample", format, file, resume, "", func(offset int64) error {
		if offset <= last {
			t.Errorf("checkpoint moved backwards: %d after %d", offset, last)
		}
		last = offset
		return nil
	})
	got, readErr := os.ReadFile(file.Name())
	if err != nil || readErr != nil || written != int64(len(payload)) || last != written || requests.Load() != nativeRangeConcurrency || !bytes.Equal(got, payload) {
		t.Fatalf("audio resume written=%d checkpoint=%d requests=%d errors=%v/%v", written, last, requests.Load(), err, readErr)
	}
}

func TestAudioRangePipelineRetriesWithSmallerChunks(t *testing.T) {
	payload := pipelinePayload(7*audioRangeMaxChunkSize + 99)
	var failed atomic.Bool
	fixture := rangeFixture(payload, func(start int64) bool {
		return start == 3*audioRangeMaxChunkSize && failed.CompareAndSwap(false, true)
	})
	defer fixture.Close()
	file, err := os.Create(filepath.Join(t.TempDir(), "audio.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	format := &youtube.Format{URL: fixture.URL, MimeType: "audio/mp4", ContentLength: int64(len(payload))}
	var last int64
	written, err := s.downloadNativeRangeWindows(context.Background(), nil, "sample", format, file, 0, "", func(offset int64) error {
		if offset <= last {
			t.Errorf("checkpoint moved backwards: %d after %d", offset, last)
		}
		last = offset
		return nil
	})
	got, readErr := os.ReadFile(file.Name())
	if !failed.Load() || err != nil || readErr != nil || written != int64(len(payload)) || last != written || !bytes.Equal(got, payload) {
		t.Fatalf("audio retry written=%d checkpoint=%d failed=%t errors=%v/%v", written, last, failed.Load(), err, readErr)
	}
}

func TestAudioRangeReportsInitialPrefixBeforeCheckpointWindow(t *testing.T) {
	payload := pipelinePayload(3 * audioRangeMaxChunkSize)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if start > 0 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer fixture.Close()
	file, err := os.Create(filepath.Join(t.TempDir(), "audio"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	format := &youtube.Format{URL: fixture.URL, MimeType: "audio/mp4", ContentLength: int64(len(payload))}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &server{rangeHTTPClient: fixture.Client()}
	var checkpointed int64
	sawInitial := false
	_, err = s.downloadNativeRangeWindows(ctx, nil, "sample", format, file, 0, "", func(offset int64) error { checkpointed = offset; return nil }, nativeRangeObserver{progress: func(prefix int64) {
		if !sawInitial {
			if checkpointed != 0 || prefix <= 0 || prefix >= format.ContentLength {
				t.Errorf("first progress prefix=%d checkpoint=%d", prefix, checkpointed)
			}
			sawInitial = true
			once.Do(func() { close(release) })
		}
	}})
	if err != nil || !sawInitial || checkpointed != format.ContentLength {
		t.Fatalf("initial progress=%t checkpoint=%d error=%v", sawInitial, checkpointed, err)
	}
}
