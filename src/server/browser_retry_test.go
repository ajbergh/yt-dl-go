package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	fresh := &dualRetryFixture{capture: func(video, audio string) (int64, int64, error) {
		// Exclusive fixture writes fail if the failed attempt wasn't cleaned up.
		writeWebMFixture(t, video, webm.TrackEntry{TrackNumber: 1, TrackUID: 1, TrackType: 1, CodecID: "V_VP9", Video: &webm.Video{PixelWidth: 1920, PixelHeight: 1080}}, []webMFrame{{data: []byte{1}, keyframe: true}})
		writeWebMFixture(t, audio, webm.TrackEntry{TrackNumber: 1, TrackUID: 2, TrackType: 2, CodecID: "A_OPUS", CodecPrivate: []byte("OpusHead\x01\x02"), Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2}}, []webMFrame{{data: []byte{2}, keyframe: true}})
		v, _ := os.Stat(video)
		a, _ := os.Stat(audio)
		return v.Size(), a.Size(), nil
	}}
	broken := &dualRetryFixture{capture: func(video, audio string) (int64, int64, error) {
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
