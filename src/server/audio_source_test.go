package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/kkdai/youtube/v2"
)

type audioSourceBrowser struct {
	freshLeaseBrowserProvider
	payload []byte
	failure error
	closed  bool
	calls   int
}

func (p *audioSourceBrowser) Close() error { p.closed = true; return nil }

func (p *audioSourceBrowser) CaptureTrack(_ context.Context, _ string, _ *youtube.Format, path string, _ int64, progress func(int64)) (int64, error) {
	p.calls++
	if err := os.WriteFile(path, p.payload, 0600); err != nil {
		return 0, errStorage
	}
	if p.failure != nil {
		return 0, p.failure
	}
	progress(int64(len(p.payload)))
	return int64(len(p.payload)), nil
}

func TestAudioSourceUsesBrowserSessionAndFallsBackToNative(t *testing.T) {
	for _, tc := range []struct {
		name, mime        string
		browser, rejected bool
		failure, wantErr  error
	}{
		{name: "mp3 source with rejected native authorization", mime: `audio/mp4; codecs="mp4a.40.2"`, browser: true, rejected: true},
		{name: "working native source avoids browser", mime: `audio/mp4; codecs="mp4a.40.2"`, browser: true, failure: errItemReservation},
		{name: "browser failure does not repeat rejected native transfer", mime: `audio/mp4; codecs="mp4a.40.2"`, browser: true, rejected: true, failure: errItemReservation, wantErr: errStreamAuthorization},
		{name: "no browser", mime: `audio/mp4; codecs="mp4a.40.2"`},
		{name: "progressive video", mime: `video/mp4; codecs="avc1.42001E, mp4a.40.2"`, browser: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte("complete audio source")
			var requests atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.rejected {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				var start, end int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(payload[start : end+1])
			}))
			defer fixture.Close()
			provider := &audioSourceBrowser{payload: payload, failure: tc.failure}
			s := &server{rangeHTTPClient: fixture.Client()}
			acquired := 0
			if tc.browser {
				s.browserFactory = func(context.Context) (browserMediaProvider, error) {
					acquired++
					return provider, nil
				}
			}
			format := &youtube.Format{ItagNo: 140, URL: fixture.URL, MimeType: tc.mime, ContentLength: int64(len(payload)), ApproxDurationMs: "300000"}
			path := filepath.Join(t.TempDir(), "source.m4a")
			size, err := s.downloadSource(context.Background(), &jobState{}, nil, &youtube.Video{ID: "82-jTNka3uc"}, format, path, "mp3-source", 1, 1024, nil)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || requests.Load() != 1 || provider.calls != 1 || !provider.closed {
					t.Fatalf("failed recovery: error=%v requests=%d captures=%d closed=%t", err, requests.Load(), provider.calls, provider.closed)
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed recovery left partial source: %v", err)
				}
				return
			}
			got, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || size != int64(len(payload)) || !bytes.Equal(got, payload) {
				t.Fatalf("source size=%d bytes=%q errors=%v/%v", size, got, err, readErr)
			}
			wantBrowser := tc.browser && tc.rejected
			if wantBrowser && (acquired != 1 || provider.calls != 1 || !provider.closed) {
				t.Fatalf("browser lifecycle acquired=%d captures=%d closed=%t", acquired, provider.calls, provider.closed)
			}
			if !wantBrowser && acquired != 0 {
				t.Fatal("acquired browser for a non-audio source")
			}
			if requests.Load() != 1 {
				t.Fatalf("unexpected native requests: %d", requests.Load())
			}
		})
	}
}
