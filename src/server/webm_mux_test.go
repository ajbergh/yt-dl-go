package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/mkvcore"
	"github.com/at-wat/ebml-go/webm"
)

func writeWebMFixture(t *testing.T, path string, track webm.TrackEntry, frames []webMFrame) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	writers, err := webm.NewSimpleBlockWriter(file, []webm.TrackEntry{track})
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	for _, frame := range frames {
		if _, err := writers[0].Write(frame.keyframe, frame.timestamp, frame.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writers[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMuxWebMInterleavesVP9AndOpus(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.webm")
	audioPath := filepath.Join(dir, "audio.webm")
	outputPath := filepath.Join(dir, "output.webm")

	videoTrack := webm.TrackEntry{
		TrackNumber: 7, TrackUID: 700, TrackType: webMVideoTrackType,
		CodecID: "V_VP9", DefaultDuration: 33333333,
		Video: &webm.Video{PixelWidth: 2560, PixelHeight: 1440},
	}
	opusHead := []byte("OpusHead\x01\x02")
	audioTrack := webm.TrackEntry{
		TrackNumber: 9, TrackUID: 900, TrackType: webMAudioTrackType,
		CodecID: "A_OPUS", CodecPrivate: opusHead, CodecDelay: 6500000, SeekPreRoll: 80000000,
		Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2},
	}
	writeWebMFixture(t, videoPath, videoTrack, []webMFrame{
		{data: []byte{0x10, 0x11}, keyframe: true, timestamp: 0},
		{data: []byte{0x12}, timestamp: 40},
	})
	writeWebMFixture(t, audioPath, audioTrack, []webMFrame{
		{data: []byte{0x20}, keyframe: true, timestamp: 0},
		{data: []byte{0x21}, keyframe: true, timestamp: 20},
		{data: []byte{0x22}, keyframe: true, timestamp: 40},
	})

	if err := muxWebM(context.Background(), videoPath, audioPath, outputPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() <= 0 {
		t.Fatalf("output WebM missing: %v", err)
	}

	var parsed struct {
		Segment struct {
			Tracks webm.Tracks `ebml:"Tracks,stop"`
		} `ebml:"Segment"`
	}
	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	err = ebml.Unmarshal(file, &parsed)
	_ = file.Close()
	if err != nil && !errors.Is(err, ebml.ErrReadStopped) {
		t.Fatal(err)
	}
	if len(parsed.Segment.Tracks.TrackEntry) != 2 {
		t.Fatalf("output tracks = %d, want 2", len(parsed.Segment.Tracks.TrackEntry))
	}
	gotVideo, gotAudio := parsed.Segment.Tracks.TrackEntry[0], parsed.Segment.Tracks.TrackEntry[1]
	if gotVideo.CodecID != "V_VP9" || gotVideo.TrackNumber != 1 || gotVideo.Video == nil || gotVideo.Video.PixelHeight != 1440 {
		t.Fatalf("unexpected video track: %+v", gotVideo)
	}
	if gotAudio.CodecID != "A_OPUS" || gotAudio.TrackNumber != 2 || string(gotAudio.CodecPrivate) != string(opusHead) || gotAudio.Audio == nil || gotAudio.Audio.Channels != 2 {
		t.Fatalf("unexpected audio track: %+v", gotAudio)
	}

	readFile, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer readFile.Close()
	readers, err := mkvcore.NewSimpleBlockReader(readFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, reader := range readers {
			_ = reader.Close()
		}
	}()
	counts := map[uint8]int{}
	var countsMu sync.Mutex
	errs := make(chan error, len(readers))
	var wg sync.WaitGroup
	for _, reader := range readers {
		reader := reader
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				data, _, _, err := reader.Read()
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					errs <- err
					return
				}
				if len(data) == 0 {
					errs <- errors.New("empty muxed frame")
					return
				}
				countsMu.Lock()
				counts[reader.TrackEntry().TrackType]++
				countsMu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if counts[1] != 2 || counts[2] != 3 {
		t.Fatalf("frame counts = %+v", counts)
	}
}

func TestMuxWebMRejectsUnexpectedVideoDimensions(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.webm")
	audioPath := filepath.Join(dir, "audio.webm")
	outputPath := filepath.Join(dir, "output.webm")
	writeWebMFixture(t, videoPath, webm.TrackEntry{
		TrackNumber: 1, TrackUID: 1, TrackType: webMVideoTrackType, CodecID: "V_VP9",
		Video: &webm.Video{PixelWidth: 3840, PixelHeight: 2160},
	}, []webMFrame{{data: []byte{1}, keyframe: true, timestamp: 0}})
	writeWebMFixture(t, audioPath, webm.TrackEntry{
		TrackNumber: 1, TrackUID: 2, TrackType: webMAudioTrackType, CodecID: "A_OPUS", CodecPrivate: []byte("OpusHead"),
		Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2},
	}, []webMFrame{{data: []byte{2}, keyframe: true, timestamp: 0}})
	if err := muxWebMForDimensions(context.Background(), videoPath, audioPath, outputPath, 2560, 1440); !errors.Is(err, errMux) {
		t.Fatalf("unexpected video dimensions returned %v, want errMux", err)
	}
}

func TestMuxWebMRejectsUnsupportedTracks(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.webm")
	audioPath := filepath.Join(dir, "audio.webm")
	outputPath := filepath.Join(dir, "output.webm")
	writeWebMFixture(t, videoPath, webm.TrackEntry{
		TrackNumber: 1, TrackUID: 1, TrackType: webMVideoTrackType, CodecID: "V_VP8",
		Video: &webm.Video{PixelWidth: 1920, PixelHeight: 1080},
	}, []webMFrame{{data: []byte{1}, keyframe: true, timestamp: 0}})
	writeWebMFixture(t, audioPath, webm.TrackEntry{
		TrackNumber: 1, TrackUID: 2, TrackType: webMAudioTrackType, CodecID: "A_OPUS", CodecPrivate: []byte("OpusHead"),
		Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2},
	}, []webMFrame{{data: []byte{2}, keyframe: true, timestamp: 0}})
	if err := muxWebM(context.Background(), videoPath, audioPath, outputPath); !errors.Is(err, errMux) {
		t.Fatalf("unsupported WebM codecs returned %v, want errMux", err)
	}
}
