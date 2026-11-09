package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestConversionHandoffFreesDownloadSlotAndQueuesConversions(t *testing.T) {
	s := &server{convertSlots: 1, scheduleChanged: make(chan struct{})}
	j := &jobState{itemProgress: map[int]*itemProgress{1: {processing: true}, 2: {processing: true}}}
	j.Items = []queueItem{{Index: 1}, {Index: 2}}
	s.activeItems = 2

	if !s.beginConversion(context.Background(), j, 1) {
		t.Fatal("first conversion should start immediately")
	}
	if s.activeItems != 1 || s.activeConversions != 1 {
		t.Fatalf("hand-off left items=%d conversions=%d, want 1 and 1", s.activeItems, s.activeConversions)
	}

	second := make(chan bool, 1)
	go func() { second <- s.beginConversion(context.Background(), j, 2) }()
	select {
	case <-second:
		t.Fatal("second conversion ran beyond the conversion limit")
	case <-time.After(100 * time.Millisecond):
	}
	// The waiting item has already given its download slot back.
	s.mu.Lock()
	freed := s.activeItems
	s.mu.Unlock()
	if freed != 0 {
		t.Fatalf("a queued conversion still holds a download slot: %d", freed)
	}
	s.mu.Lock()
	running, waiting := j.Items[0].WaitingToConvert, j.Items[1].WaitingToConvert
	s.mu.Unlock()
	if running || !waiting {
		t.Fatalf("waitingToConvert running=%t waiting=%t, want false and true", running, waiting)
	}
	s.endConversion()
	select {
	case ok := <-second:
		if !ok {
			t.Fatal("queued conversion was refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued conversion never started")
	}
	s.mu.Lock()
	stillWaiting := j.Items[1].WaitingToConvert
	s.mu.Unlock()
	if stillWaiting {
		t.Fatal("waitingToConvert stayed set after the conversion started")
	}
	s.endConversion()
	s.releaseItemSlot(j, 1)
	s.releaseItemSlot(j, 2)
	if s.activeItems != 0 || s.activeConversions != 0 {
		t.Fatalf("slots leaked: items=%d conversions=%d", s.activeItems, s.activeConversions)
	}
}

func TestConversionQueueHonorsCancellation(t *testing.T) {
	s := &server{convertSlots: 1, scheduleChanged: make(chan struct{})}
	j := &jobState{itemProgress: map[int]*itemProgress{1: {}}}
	s.activeConversions = 1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- s.beginConversion(ctx, j, 1) }()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("cancelled wait acquired a conversion slot")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop the wait")
	}
	if s.activeConversions != 1 {
		t.Fatalf("cancelled wait changed the conversion count: %d", s.activeConversions)
	}
}

func TestMP3PlaylistCompletesWithSingleConversionSlot(t *testing.T) {
	data, err := os.ReadFile("testdata/audio/silence.m4a")
	if err != nil {
		t.Fatal(err)
	}
	fake := fixtureClient(3)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		video := fixtureVideo(id)
		video.Formats = youtube.FormatList{{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`, AudioChannels: 2, AudioSampleRate: "44100", ContentLength: int64(len(data))}}
		return video, nil
	}
	fake.streamFn = func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
	}
	s := testServer(t, fake, nil)
	s.convertSlots = 1
	body := `{"url":"` + testPlaylist + `","quality":"best","mediaType":"audio","audioFormat":"mp3","audioBitrate":"192k","rightsConfirmed":true}`
	response := request(s, "POST", "/api/jobs", body, nil)
	var created Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create job: %d %s", response.Code, response.Body.String())
	}
	done := waitTerminal(t, s, created.ID)
	if done.Status != "completed" || len(done.Files) != 3 {
		t.Fatalf("playlist did not complete: %s files=%d err=%q", done.Status, len(done.Files), done.Error)
	}
	published, _ := filepath.Glob(filepath.Join(s.settings.DownloadLocation, "*", "*.mp3"))
	if len(published) != 3 {
		t.Fatalf("published %d MP3 files, want 3", len(published))
	}
	s.mu.Lock()
	items, conversions := s.activeItems, s.activeConversions
	s.mu.Unlock()
	if items != 0 || conversions != 0 {
		t.Fatalf("slots leaked after completion: items=%d conversions=%d", items, conversions)
	}
}
