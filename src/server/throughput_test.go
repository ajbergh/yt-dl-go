package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestNativeRangeRequiresExactBodyLength(t *testing.T) {
	for _, size := range []int{7, 8, 9} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Range", "bytes 0-7/8")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(bytes.Repeat([]byte{42}, size))
			}))
			defer fixture.Close()
			chunk := nativeRangeChunk{start: 0, end: 7}
			profile := youtubeClientProfile{client: youtube.AndroidClient}
			err := (&server{}).readNativeRange(context.Background(), fixture.Client(), profile, &youtube.Format{URL: fixture.URL, ContentLength: 8}, &chunk)
			if size == 8 {
				if err != nil || !bytes.Equal(chunk.data, bytes.Repeat([]byte{42}, 8)) {
					t.Fatalf("exact range data=%v err=%v", chunk.data, err)
				}
			} else if err == nil || len(chunk.data) != 0 {
				t.Fatalf("accepted %d-byte response for 8-byte range", size)
			}
		})
	}
}

// Model request latency separately from upstream delivery variability. Include
// disk writes and syncs, as successful resumable downloads require durability.
func BenchmarkNativeRangeDownload(b *testing.B) {
	payload := make([]byte, 4*adaptiveRangeChunkSize+123)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= int64(len(payload)) || end < start {
			http.Error(w, "invalid range", 400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	format := &youtube.Format{URL: fixture.URL, ContentLength: int64(len(payload))}
	file, err := os.Create(filepath.Join(b.TempDir(), "media.part"))
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		written, err := s.downloadNativeRangeWindows(context.Background(), nil, "sample", format, file, 0, "", func(int64) error { return nil })
		if err != nil || written != int64(len(payload)) {
			b.Fatalf("written=%d err=%v", written, err)
		}
	}
}
