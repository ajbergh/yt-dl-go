package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestAudioAuthorizationCooldownSkipsRepeatedNativeFailures(t *testing.T) {
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fixture.Close()
	payload := []byte("authorized browser audio")
	var providers []*audioSourceBrowser
	s := &server{rangeHTTPClient: fixture.Client(), browserFactory: func(context.Context) (browserMediaProvider, error) {
		p := &audioSourceBrowser{payload: payload}
		providers = append(providers, p)
		return p, nil
	}}
	format := &youtube.Format{ItagNo: 140, URL: fixture.URL, MimeType: `audio/mp4; codecs="mp4a.40.2"`, ContentLength: int64(len(payload)), ApproxDurationMs: "300000"}
	for _, id := range []string{"82-jTNka3uc", "abcdefghijk"} {
		path := filepath.Join(t.TempDir(), "source")
		size, err := s.downloadSource(context.Background(), &jobState{}, nil, &youtube.Video{ID: id}, format, path, "mp3-source", 1, 1024, nil)
		got, readErr := os.ReadFile(path)
		if err != nil || readErr != nil || size != int64(len(payload)) || !bytes.Equal(got, payload) {
			t.Fatalf("source: size=%d data=%q errors=%v/%v", size, got, err, readErr)
		}
	}
	if requests.Load() != 1 || len(providers) != 2 || !providers[0].closed || !providers[1].closed {
		t.Fatalf("requests=%d providers=%+v", requests.Load(), providers)
	}
	// Expiry permits native access again instead of pinning this host to Chrome.
	s.audioTransport.mu.Lock()
	s.audioTransport.rejected[audioTransportKey(nil, format)] = time.Now().Add(-time.Second)
	s.audioTransport.mu.Unlock()
	_, err := s.downloadSource(context.Background(), &jobState{}, nil, &youtube.Video{ID: "abcdefghijk"}, format, filepath.Join(t.TempDir(), "source"), "mp3-source", 1, 1024, nil)
	if err != nil || requests.Load() != 2 {
		t.Fatalf("expired cooldown: requests=%d error=%v", requests.Load(), err)
	}
}

func TestAudioTransportKeepsHardFailuresAndBandwidthCapNative(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		cap    bool
		want   error
	}{
		{"rate limited", http.StatusTooManyRequests, false, errStreamRateLimited},
		{"bandwidth limited", http.StatusForbidden, true, errStreamAuthorization},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(tc.status)
			}))
			defer fixture.Close()
			var acquired atomic.Int32
			s := &server{rangeHTTPClient: fixture.Client(), browserFactory: func(context.Context) (browserMediaProvider, error) {
				acquired.Add(1)
				return &audioSourceBrowser{}, nil
			}}
			if tc.cap {
				s.bandwidth = newBandwidthLimiter(1024 * 1024)
			}
			format := &youtube.Format{ItagNo: 140, URL: fixture.URL, MimeType: `audio/mp4; codecs="mp4a.40.2"`, ContentLength: 100, ApproxDurationMs: "300000"}
			_, err := s.downloadSource(context.Background(), &jobState{}, nil, &youtube.Video{ID: "abcdefghijk"}, format, filepath.Join(t.TempDir(), "source"), "mp3-source", 1, 1024, nil)
			if !errors.Is(err, tc.want) || acquired.Load() != 0 {
				t.Fatalf("error=%v browser acquisitions=%d", err, acquired.Load())
			}
		})
	}
}

func TestAudioTransportRetriesWithFreshBrowserAfterNativeRejection(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer fixture.Close()
	payload := []byte("fresh browser audio")
	fresh := &audioSourceBrowser{payload: payload}
	broken := &audioSourceBrowser{payload: []byte("partial"), failure: errBrowserUnavailable}
	broken.next = fresh
	s := &server{rangeHTTPClient: fixture.Client(), browserFactory: func(context.Context) (browserMediaProvider, error) { return broken, nil }}
	format := &youtube.Format{ItagNo: 140, URL: fixture.URL, MimeType: `audio/mp4; codecs="mp4a.40.2"`, ContentLength: int64(len(payload)), ApproxDurationMs: "300000"}
	path := filepath.Join(t.TempDir(), "source")
	_, err := s.downloadSource(context.Background(), &jobState{}, nil, &youtube.Video{ID: "abcdefghijk"}, format, path, "mp3-source", 1, 1024, nil)
	got, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || !bytes.Equal(got, payload) || broken.reopens != 1 || !broken.closed || !fresh.closed {
		t.Fatalf("fresh recovery: bytes=%q reopens=%d closed=%t/%t errors=%v/%v", got, broken.reopens, broken.closed, fresh.closed, err, readErr)
	}
}

func TestAudioArtworkOverlapsAcquisitionAndJoinsOnFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			s := &server{thumbnailFetcher: func(ctx context.Context, _ string, path string) (string, error) {
				close(started)
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-release:
				}
				return "image/jpeg", os.WriteFile(path, []byte("artwork"), 0600)
			}}
			j := &jobState{dir: t.TempDir()}
			file := mediaFile{ID: "audio-artwork", ThumbnailURL: "https://i.ytimg.com/vi/abcdefghijk/default.jpg"}
			finish := s.startAudioThumbnail(context.Background(), j, &file)
			<-started // The caller can acquire media while the thumbnail is blocked.
			if file.ThumbnailLocalAvailable {
				t.Fatal("thumbnail published before joining fetch")
			}
			if failure {
				finish(errRead)
			} else {
				close(release)
				finish(nil)
			}
			finish(nil) // Joining again is harmless (tagging followed by deferred cleanup).
			if file.ThumbnailLocalAvailable == failure {
				t.Fatalf("artwork availability=%t failure=%t", file.ThumbnailLocalAvailable, failure)
			}
		})
	}
}

type resumedAudioBrowser struct {
	audioSourceBrowser
	t      *testing.T
	prefix []byte
}

func (p *resumedAudioBrowser) SetCheckpoints(sabrCheckpointIO) {}

func (p *resumedAudioBrowser) CaptureTrack(ctx context.Context, id string, format *youtube.Format, path string, budget int64, progress func(int64)) (int64, error) {
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, p.prefix) {
		p.t.Fatalf("browser resume lost its checkpointed source: bytes=%q error=%v", got, err)
	}
	return p.audioSourceBrowser.CaptureTrack(ctx, id, format, path, budget, progress)
}

func TestAudioSourceResumesBrowserCheckpointWithoutCooldown(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := &jobState{Job: Job{ID: "audio-browser-resume", URL: testVideo, Kind: "video", Quality: "best", Status: "downloading", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, dir: t.TempDir()}
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.dir, "source.m4a")
	prefix, payload := []byte("audio"), []byte("audio complete")
	if err := os.WriteFile(path, prefix, 0600); err != nil {
		t.Fatal(err)
	}
	format := &youtube.Format{ItagNo: 140, URL: "https://example.com/audio", MimeType: `audio/mp4; codecs="mp4a.40.2"`, ContentLength: int64(len(payload)), ApproxDurationMs: "300000"}
	if err := s.saveDownloadPart(j, "abcdefghijk", 1, "mp3-source", path, "browser-sabr", format, int64(len(prefix))); err != nil {
		t.Fatal(err)
	}
	provider := &resumedAudioBrowser{audioSourceBrowser: audioSourceBrowser{payload: payload}, t: t, prefix: prefix}
	s.browserFactory = func(context.Context) (browserMediaProvider, error) { return provider, nil }
	_, err := s.downloadSource(context.Background(), j, nil, &youtube.Video{ID: "abcdefghijk"}, format, path, "mp3-source", 1, 1024, nil)
	if err != nil || !provider.closed || provider.calls != 1 {
		t.Fatalf("resume error=%v closed=%t captures=%d", err, provider.closed, provider.calls)
	}
}
