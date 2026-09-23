package main

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/mkvcore"
	"github.com/at-wat/ebml-go/webm"
)

const (
	webMVideoTrackType = uint64(1)
	webMAudioTrackType = uint64(2)
)

type muxAsyncError struct {
	mu  sync.Mutex
	err error
}

func (s *muxAsyncError) set(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *muxAsyncError) get() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

type webMTrackSource struct {
	file   *os.File
	reader mkvcore.BlockReadCloserWithTrackEntry
	fatal  *muxAsyncError
}

func (s *webMTrackSource) close() {
	if s == nil {
		return
	}
	if s.reader != nil {
		_ = s.reader.Close()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
}

type webMFrame struct {
	data      []byte
	keyframe  bool
	timestamp int64
	eof       bool
}

func readWebMTrackEntry(path string, trackType uint64) (webm.TrackEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return webm.TrackEntry{}, err
	}
	defer file.Close()

	var header struct {
		Segment struct {
			Tracks webm.Tracks `ebml:"Tracks,stop"`
		} `ebml:"Segment"`
	}
	err = ebml.Unmarshal(file, &header, ebml.WithMaxLeafElementSize(8<<20))
	if err != nil && !errors.Is(err, ebml.ErrReadStopped) {
		return webm.TrackEntry{}, err
	}
	for _, track := range header.Segment.Tracks.TrackEntry {
		if track.TrackType == trackType {
			return track, nil
		}
	}
	return webm.TrackEntry{}, errMux
}

func openWebMTrackSource(path string, trackType uint64) (*webMTrackSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fatal := &muxAsyncError{}
	readers, err := mkvcore.NewSimpleBlockReader(
		file,
		mkvcore.WithOnFatalHandler(fatal.set),
		mkvcore.WithUnmarshalOptions(ebml.WithMaxLeafElementSize(64<<20)),
	)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	var selected mkvcore.BlockReadCloserWithTrackEntry
	for _, reader := range readers {
		if selected == nil && uint64(reader.TrackEntry().TrackType) == trackType {
			selected = reader
			continue
		}
		_ = reader.Close()
	}
	if selected == nil {
		for _, reader := range readers {
			_ = reader.Close()
		}
		_ = file.Close()
		return nil, errMux
	}
	return &webMTrackSource{file: file, reader: selected, fatal: fatal}, nil
}

func nextWebMFrame(ctx context.Context, source *webMTrackSource) (webMFrame, error) {
	if err := ctx.Err(); err != nil {
		return webMFrame{}, err
	}
	data, keyframe, timestamp, err := source.reader.Read()
	if errors.Is(err, io.EOF) {
		if fatal := source.fatal.get(); fatal != nil {
			return webMFrame{}, fatal
		}
		return webMFrame{eof: true}, nil
	}
	if err != nil {
		return webMFrame{}, err
	}
	if len(data) == 0 || timestamp < 0 {
		return webMFrame{}, errMux
	}
	return webMFrame{data: data, keyframe: keyframe, timestamp: timestamp}, nil
}

func normalizeWebMTrack(track webm.TrackEntry, number, trackType uint64) webm.TrackEntry {
	track.TrackNumber = number
	track.TrackUID = number
	track.TrackType = trackType
	if trackType == webMVideoTrackType {
		track.Name = "Video"
		track.Audio = nil
	} else {
		track.Name = "Audio"
		track.Video = nil
	}
	return track
}

func validWebMAdaptiveTracks(video, audio webm.TrackEntry) bool {
	if video.TrackType != webMVideoTrackType || video.Video == nil || video.Video.PixelWidth == 0 || video.Video.PixelHeight == 0 {
		return false
	}
	if video.CodecID != "V_VP9" && video.CodecID != "V_AV1" {
		return false
	}
	if audio.TrackType != webMAudioTrackType || audio.Audio == nil || audio.Audio.Channels == 0 || audio.Audio.SamplingFrequency <= 0 {
		return false
	}
	return audio.CodecID == "A_OPUS" && len(audio.CodecPrivate) > 0
}

func closeWebMWriters(writers []webm.BlockWriteCloser) error {
	var first error
	for _, writer := range writers {
		if writer == nil {
			continue
		}
		if err := writer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// muxWebM remuxes separate VP9/AV1 video and Opus audio WebM tracks without
// decoding or transcoding. Frames are streamed and timestamp-interleaved, so
// memory use is bounded by the EBML parser and writer sort buffers.
func muxWebM(ctx context.Context, videoPath, audioPath, outputPath string) (err error) {
	return muxWebMForDimensions(ctx, videoPath, audioPath, outputPath, 0, 0)
}

func muxWebMForDimensions(ctx context.Context, videoPath, audioPath, outputPath string, expectedWidth, expectedHeight int) (err error) {
	videoTrack, err := readWebMTrackEntry(videoPath, webMVideoTrackType)
	if err != nil {
		return err
	}
	audioTrack, err := readWebMTrackEntry(audioPath, webMAudioTrackType)
	if err != nil {
		return err
	}
	if !validWebMAdaptiveTracks(videoTrack, audioTrack) {
		return errMux
	}
	if (expectedWidth > 0 && videoTrack.Video.PixelWidth != uint64(expectedWidth)) ||
		(expectedHeight > 0 && videoTrack.Video.PixelHeight != uint64(expectedHeight)) {
		return errMux
	}
	videoTrack = normalizeWebMTrack(videoTrack, 1, webMVideoTrackType)
	audioTrack = normalizeWebMTrack(audioTrack, 2, webMAudioTrackType)

	videoSource, err := openWebMTrackSource(videoPath, webMVideoTrackType)
	if err != nil {
		return err
	}
	defer videoSource.close()
	audioSource, err := openWebMTrackSource(audioPath, webMAudioTrackType)
	if err != nil {
		return err
	}
	defer audioSource.close()

	output, err := os.OpenFile(outputPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	writerFatal := &muxAsyncError{}
	writers, err := webm.NewSimpleBlockWriter(
		output,
		[]webm.TrackEntry{videoTrack, audioTrack},
		mkvcore.WithSeekHead(true),
		mkvcore.WithCues(256*1024),
		mkvcore.WithMinMaxClusterDuration(1, 1000, 5000),
		mkvcore.WithOnFatalHandler(writerFatal.set),
	)
	if err != nil {
		_ = output.Close()
		return err
	}
	writersClosed := false
	defer func() {
		if !writersClosed {
			_ = closeWebMWriters(writers)
		}
	}()

	videoFrame, err := nextWebMFrame(ctx, videoSource)
	if err != nil {
		return err
	}
	audioFrame, err := nextWebMFrame(ctx, audioSource)
	if err != nil {
		return err
	}
	for !videoFrame.eof || !audioFrame.eof {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fatal := writerFatal.get(); fatal != nil {
			return fatal
		}
		writeVideo := audioFrame.eof || (!videoFrame.eof && videoFrame.timestamp <= audioFrame.timestamp)
		if writeVideo {
			if _, err := writers[0].Write(videoFrame.keyframe, videoFrame.timestamp, videoFrame.data); err != nil {
				return err
			}
			videoFrame, err = nextWebMFrame(ctx, videoSource)
		} else {
			if _, err := writers[1].Write(audioFrame.keyframe, audioFrame.timestamp, audioFrame.data); err != nil {
				return err
			}
			audioFrame, err = nextWebMFrame(ctx, audioSource)
		}
		if err != nil {
			return err
		}
	}
	if err := closeWebMWriters(writers); err != nil {
		return err
	}
	writersClosed = true
	if fatal := writerFatal.get(); fatal != nil {
		return fatal
	}
	if fatal := videoSource.fatal.get(); fatal != nil {
		return fatal
	}
	if fatal := audioSource.fatal.get(); fatal != nil {
		return fatal
	}
	return nil
}
