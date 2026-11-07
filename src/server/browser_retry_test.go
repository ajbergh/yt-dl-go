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

	"github.com/at-wat/ebml-go/webm"
	"github.com/kkdai/youtube/v2"
)

type dualRetryFixture struct {
	freshLeaseBrowserProvider
	capture func(string, string) (int64, int64, error)
	closed  bool
}

func (p *dualRetryFixture) Close() error { p.closed = true; return nil }
func (p *dualRetryFixture) CaptureTracks(_ context.Context, _ string, _ *youtube.Format, _ []*youtube.Format, _ *youtube.Format, video, audio string, _, _ int64, _ func(int64, int64)) (int64, int64, error) {
	return p.capture(video, audio)
}

func TestAdaptiveWebMRecoversWithFreshDualCapture(t *testing.T) {
	var broken *dualRetryFixture
	fresh := &dualRetryFixture{capture: func(video, audio string) (int64, int64, error) {
		if !broken.closed {
			t.Fatal("failed tab kept playing during recovery")
		}
		// Exclusive fixture writes fail if the failed attempt wasn't cleaned up.
		writeWebMFixture(t, video, webm.TrackEntry{TrackNumber: 1, TrackUID: 1, TrackType: 1, CodecID: "V_VP9", Video: &webm.Video{PixelWidth: 1920, PixelHeight: 1080}}, []webMFrame{{data: []byte{1}, keyframe: true}})
		writeWebMFixture(t, audio, webm.TrackEntry{TrackNumber: 1, TrackUID: 2, TrackType: 2, CodecID: "A_OPUS", CodecPrivate: []byte("OpusHead\x01\x02"), Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2}}, []webMFrame{{data: []byte{2}, keyframe: true}})
		v, _ := os.Stat(video)
		a, _ := os.Stat(audio)
		return v.Size(), a.Size(), nil
	}}
	broken = &dualRetryFixture{capture: func(video, audio string) (int64, int64, error) {
		if err := os.WriteFile(video, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(audio, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		return 0, 0, errBrowserUnavailable
	}}
	broken.next = fresh
	s := &server{ctx: context.Background(), browserFactory: func(context.Context) (browserMediaProvider, error) { return broken, nil }}
	j := &jobState{Job: Job{ID: "dual-retry", Status: "downloading"}, dir: t.TempDir(), itemProgress: map[int]*itemProgress{}}
	selection := streamSelection{
		video: &youtube.Format{ItagNo: 248, Width: 1920, Height: 1080, MimeType: `video/webm; codecs="vp9"`, ContentLength: 1000},
		audio: &youtube.Format{ItagNo: 251, MimeType: `audio/webm; codecs="opus"`, ContentLength: 1000},
	}
	file, err := s.transferAdaptiveWebM(context.Background(), j, nil, &youtube.Video{ID: "abcdefghijk"}, selection, 1, 1, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if broken.reopens != 1 || !fresh.closed || !broken.closed {
		t.Fatalf("retry lifecycle: reopens=%d freshClosed=%t oldClosed=%t", broken.reopens, fresh.closed, broken.closed)
	}
	if _, err := readWebMTrackEntry(filepath.Join(j.dir, file.Name), 2); err != nil {
		t.Fatalf("final output lost audio: %v", err)
	}
}

func TestBrowserCaptureRetryStopsOnCancellationAndStorageError(t *testing.T) {
	for _, failure := range []error{context.Canceled, errStorage} {
		calls := 0
		provider := &freshLeaseBrowserProvider{}
		ctx, cancel := context.WithCancel(context.Background())
		err := retryBrowserCapture(ctx, provider, func(browserMediaProvider) error {
			calls++
			if errors.Is(failure, context.Canceled) {
				cancel()
				return errBrowserUnavailable
			}
			return failure
		})
		cancel()
		if !errors.Is(err, failure) || calls != 1 || provider.reopens != 0 {
			t.Fatalf("failure=%v result=%v calls=%d reopens=%d", failure, err, calls, provider.reopens)
		}
	}
}

func TestBrowserCaptureRecoveryBudgetRequiresVerifiedProgress(t *testing.T) {
	for _, test := range []struct {
		name                string
		advance             bool
		finishAt, wantCalls int
	}{
		{"continued progress", true, 4, 4},
		{"stalled session", false, 4, 2},
		{"hard recovery cap", true, 9, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &freshLeaseBrowserProvider{}
			provider.next = provider
			calls := 0
			var verified int64
			err := retryBrowserCapture(context.Background(), provider, func(browserMediaProvider) error {
				calls++
				if calls == test.finishAt {
					return nil
				}
				if test.advance {
					verified += 10000
				}
				return errBrowserUnavailable
			}, func() int64 { return verified })
			if calls != test.wantCalls {
				t.Fatalf("attempts=%d want=%d", calls, test.wantCalls)
			}
			if (err == nil) != (test.finishAt <= test.wantCalls) {
				t.Fatalf("unexpected completion: %v", err)
			}
		})
	}
}

func TestAdaptiveRangeErrorsDistinguishLengthAndReservation(t *testing.T) {
	for _, tc := range []struct {
		length, budget int64
		want           error
	}{{0, 100, errLength}, {101, 100, errItemReservation}} {
		_, _, err := (&server{}).downloadAdaptiveRanges(context.Background(), &jobState{}, nil, &youtube.Video{}, &youtube.Format{ContentLength: tc.length}, "", "video", 1, tc.budget, nil, nil)
		if !errors.Is(err, tc.want) || errors.Is(err, errLimit) {
			t.Fatalf("length=%d budget=%d error=%v", tc.length, tc.budget, err)
		}
	}
}

type reservationTrackProvider struct {
	freshLeaseBrowserProvider
	calls int
}

func (p *reservationTrackProvider) CaptureTrack(_ context.Context, _ string, _ *youtube.Format, path string, _ int64, _ func(int64)) (int64, error) {
	p.calls++
	if err := os.WriteFile(path, []byte("partial browser bytes"), 0600); err != nil {
		return 0, err
	}
	return 0, errItemReservation
}

func TestBrowserTrackReservationFallsBackToVerifiedNativeRanges(t *testing.T) {
	payload := []byte("complete native source")
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	provider := &reservationTrackProvider{}
	path := filepath.Join(t.TempDir(), "video.part")
	format := &youtube.Format{ItagNo: 137, URL: fixture.URL, ContentLength: int64(len(payload))}
	size, usedBrowser, err := s.downloadAdaptiveRanges(context.Background(), &jobState{}, nil, &youtube.Video{ID: "abcdefghijk"}, format, path, "video", 1, int64(len(payload)), func(int64) {}, provider)
	actual, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || usedBrowser || size != int64(len(payload)) || !bytes.Equal(actual, payload) || provider.calls != 1 {
		t.Fatalf("native recovery: bytes=%q size=%d browser=%t calls=%d errors=%v/%v", actual, size, usedBrowser, provider.calls, err, readErr)
	}
}

func TestBrowserPairReservationRecoversWithoutDowngradingQuality(t *testing.T) {
	sources := t.TempDir()
	videoPath, audioPath := filepath.Join(sources, "video.webm"), filepath.Join(sources, "audio.webm")
	writeWebMFixture(t, videoPath, webm.TrackEntry{TrackNumber: 1, TrackUID: 1, TrackType: 1, CodecID: "V_VP9", Video: &webm.Video{PixelWidth: 1920, PixelHeight: 1080}}, []webMFrame{{data: []byte{1}, keyframe: true}})
	writeWebMFixture(t, audioPath, webm.TrackEntry{TrackNumber: 1, TrackUID: 2, TrackType: 2, CodecID: "A_OPUS", CodecPrivate: []byte("OpusHead\x01\x02"), Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2}}, []webMFrame{{data: []byte{2}, keyframe: true}})
	v, _ := os.ReadFile(videoPath)
	a, _ := os.ReadFile(audioPath)
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		payload := v
		if r.URL.Path == "/audio" {
			payload = a
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer fixture.Close()
	var captures int
	provider := &dualRetryFixture{capture: func(v, a string) (int64, int64, error) {
		captures++
		_ = os.WriteFile(v, []byte("partial"), 0600)
		return 0, 0, errItemReservation
	}}
	s := &server{ctx: context.Background(), rangeHTTPClient: fixture.Client(), browserFactory: func(context.Context) (browserMediaProvider, error) { return provider, nil }}
	j := &jobState{Job: Job{ID: "reservation-recovery", Status: "downloading"}, dir: t.TempDir(), itemProgress: map[int]*itemProgress{}}
	selection := streamSelection{kind: "video/webm", video: &youtube.Format{ItagNo: 248, URL: fixture.URL + "/video", Width: 1920, Height: 1080, MimeType: `video/webm; codecs="vp9"`, ContentLength: int64(len(v))}, audio: &youtube.Format{ItagNo: 251, URL: fixture.URL + "/audio", MimeType: `audio/webm; codecs="opus"`, ContentLength: int64(len(a))}}
	const budget = 1024 * 1024
	file, err := s.transferAdaptiveWebM(context.Background(), j, nil, &youtube.Video{ID: "abcdefghijk"}, selection, 1, 1, budget)
	if err != nil || file.Height != 1080 || file.Size > budget || captures != 1 || requests.Load() < 2 {
		t.Fatalf("pair recovery: file=%+v captures=%d requests=%d err=%v", file, captures, requests.Load(), err)
	}
	if _, err := readWebMTrackEntry(filepath.Join(j.dir, file.Name), 2); err != nil {
		t.Fatal("recovered output lost audio", err)
	}
}

type pairBudgetProvider struct {
	freshLeaseBrowserProvider
	videoBudget, audioBudget int64
}

func (p *pairBudgetProvider) CaptureTracks(_ context.Context, _ string, _ *youtube.Format, _ []*youtube.Format, _ *youtube.Format, v, a string, vb, ab int64, _ func(int64, int64)) (int64, int64, error) {
	p.videoBudget, p.audioBudget = vb, ab
	if err := os.WriteFile(v, bytes.Repeat([]byte{1}, 20), 0600); err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(a, bytes.Repeat([]byte{2}, 10), 0600); err != nil {
		return 0, 0, err
	}
	return 20, 10, nil
}
func TestBrowserPairSharesHeadroomWithoutExceedingReservation(t *testing.T) {
	p := &pairBudgetProvider{}
	selection := streamSelection{kind: "video/mp4", video: &youtube.Format{ItagNo: 137, ContentLength: 20}, audio: &youtube.Format{ItagNo: 140, ContentLength: 10}}
	dir := t.TempDir()
	_, _, used, err := (&server{}).captureAdaptivePair(context.Background(), &jobState{}, nil, &youtube.Video{ID: "abcdefghijk"}, selection, filepath.Join(dir, "v"), filepath.Join(dir, "a"), 1, 100, nil, p)
	if err != nil || !used || p.videoBudget <= 20 || p.audioBudget <= 10 || p.videoBudget+p.audioBudget != 100 {
		t.Fatalf("pair caps video=%d audio=%d used=%t err=%v", p.videoBudget, p.audioBudget, used, err)
	}
}
