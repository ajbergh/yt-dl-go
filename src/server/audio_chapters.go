package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kkdai/youtube/v2"
	aacm4a "github.com/tphakala/go-m4a/aacm4a"
	"github.com/tphakala/go-mp3"
)

type chapterMP3Output struct {
	file    mediaFile
	part    string
	final   string
	output  *os.File
	encoder *mp3.Encoder
	pending [][]float32
	count   int
	bytes   int64
}

// transferChapterMP3 downloads the selected AAC source once and routes decoded
// samples to independent MP3 encoders according to the chapter time ranges.
// All outputs are finalized together so a failed chapter never leaves a partial
// source-item group in the Library.
func (s *server) transferChapterMP3(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, extension string, queueIndex, outputIndex int, budget int64) (files []mediaFile, err error) {
	chapters := parseDescriptionChapters(video.Description, video.Duration)
	if len(chapters) < 2 {
		return nil, errors.New("chapter splitting requires at least two valid chapters")
	}
	if budget <= 0 || (format.ContentLength > 0 && format.ContentLength > budget) {
		return nil, errLimit
	}
	sourcePath := filepath.Join(j.dir, fmt.Sprintf("%06d-%s.source.%s", outputIndex, video.ID, extension))
	sourceSize, err := s.downloadSource(ctx, j, engine, video, format, sourcePath, "mp3-source", queueIndex, budget, func(written int64) {
		s.updateProgress(j, queueIndex, written, format.ContentLength)
	})
	if err != nil {
		return nil, err
	}
	keepSource := false
	defer func() {
		if err != nil && !s.keepPartial(j) && !keepSource {
			if removeErr := os.Remove(sourcePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, errStorage)
			}
			if checkpointErr := s.deleteDownloadPart(j, queueIndex, "mp3-source"); checkpointErr != nil {
				err = errors.Join(err, errStorage)
			}
		}
	}()
	outputBudget := budget - sourceSize
	if outputBudget <= 0 {
		return nil, errLimit
	}
	s.updateProgress(j, queueIndex, sourceSize, sourceSize+outputBudget)
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, errStorage
	}
	decoder, info, decodeErr := aacm4a.NewDecoder(source)
	if decodeErr != nil || decoder == nil || info.Channels < 1 || info.Channels > 2 || info.SampleRate <= 0 {
		_ = source.Close()
		return nil, errAudioConvert
	}
	bitrateKbps, err := strconv.Atoi(strings.TrimSuffix(j.AudioBitrate, "k"))
	if err != nil {
		_ = source.Close()
		return nil, errAudioConvert
	}
	outputs := make([]chapterMP3Output, len(chapters))
	cleanup := func() {
		for index := range outputs {
			if outputs[index].output != nil {
				_ = outputs[index].output.Close()
			}
			_ = os.Remove(outputs[index].part)
			_ = os.Remove(outputs[index].final)
			if outputs[index].file.ThumbnailLocalAvailable {
				if thumbnail, pathErr := thumbnailPath(j, outputs[index].file); pathErr == nil {
					_ = os.Remove(thumbnail)
				}
			}
		}
	}
	for index, chapter := range chapters {
		file := mediaFile{
			ID: randomID(16), Name: fmt.Sprintf("%06d-%s-c%03d.mp3", outputIndex, video.ID, index+1),
			MimeType: "audio/mpeg", MediaType: "audio", Title: chapter.Title,
			ChapterTitle: chapter.Title, ChapterIndex: index + 1, SourceItemIndex: queueIndex,
			DurationSeconds:  max(int64(0), (chapter.EndMs-chapter.StartMs)/1000),
			ManagedAvailable: true, naming: namingValues{Codec: "mp3"},
		}
		applyVideoMetadata(&file, video)
		file.Title = chapter.Title
		file.ChapterTitle = chapter.Title
		file.ChapterIndex = index + 1
		file.SourceItemIndex = queueIndex
		file.DurationSeconds = max(int64(0), (chapter.EndMs-chapter.StartMs)/1000)
		file.Chapters = []mediaChapter{{StartMs: 0, EndMs: chapter.EndMs - chapter.StartMs, Title: chapter.Title}}
		file.Category = j.Category
		s.captureThumbnail(ctx, j, &file)
		part := filepath.Join(j.dir, file.Name+".part")
		final := filepath.Join(j.dir, file.Name)
		outputs[index] = chapterMP3Output{file: file, part: part, final: final}
		_ = os.Remove(part)
		_ = os.Remove(final)
		output, openErr := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			cleanup()
			_ = source.Close()
			return nil, errStorage
		}
		metadata := mp3MetadataFor(j, file, video.ID, index+1)
		metadata.TrackTotal = len(chapters)
		tag, tagErr := buildID3v23Tag(metadata)
		if tagErr != nil {
			tag = nil
		}
		outputs[index].output = output
		outputs[index].encoder, err = mp3.NewEncoder(mp3.EncoderConfig{SampleRate: info.SampleRate, Channels: info.Channels, Bitrate: bitrateKbps * 1000})
		if err != nil {
			cleanup()
			_ = source.Close()
			return nil, errAudioConvert
		}
		outputs[index].pending = make([][]float32, info.Channels)
		for channel := range outputs[index].pending {
			outputs[index].pending[channel] = make([]float32, mp3.FrameSize)
		}
		if len(tag) > 0 {
			n, writeErr := output.Write(tag)
			if writeErr != nil || n != len(tag) || int64(n) > outputBudget {
				cleanup()
				_ = source.Close()
				return nil, errStorage
			}
			outputs[index].bytes = int64(n)
			outputs[index].count = 0
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	frameBytes := mp3.FrameSize * info.Channels * 2
	pcm := make([]byte, frameBytes)
	frames := make([][]float32, info.Channels)
	encoded := make([]byte, 0, 8192)
	var totalWritten int64
	for index := range outputs {
		totalWritten += outputs[index].bytes
	}
	var decodedSamples int64
	activeChapter := 0
	writeFrame := func(output *chapterMP3Output, sampleCount int) error {
		if sampleCount == 0 {
			return nil
		}
		for channel := range frames {
			frames[channel] = output.pending[channel][:sampleCount]
		}
		encoded, encodeErr := output.encoder.EncodeFrame(encoded[:0], frames)
		if encodeErr != nil {
			return errAudioConvert
		}
		if int64(len(encoded)) > outputBudget-totalWritten {
			return errLimit
		}
		n, writeErr := output.output.Write(encoded)
		if writeErr != nil || n != len(encoded) {
			return errStorage
		}
		output.bytes += int64(n)
		totalWritten += int64(n)
		output.count = 0
		return nil
	}
	flushChapter := func(output *chapterMP3Output) error {
		if err := writeFrame(output, output.count); err != nil {
			return err
		}
		encoded, encodeErr := output.encoder.EncodeFrame(encoded[:0], nil)
		if encodeErr != nil {
			return errAudioConvert
		}
		if int64(len(encoded)) > outputBudget-totalWritten {
			return errLimit
		}
		n, writeErr := output.output.Write(encoded)
		if writeErr != nil || n != len(encoded) {
			return errStorage
		}
		output.bytes += int64(n)
		totalWritten += int64(n)
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = source.Close()
			return nil, err
		}
		n, readErr := io.ReadFull(decoder, pcm)
		if n%(info.Channels*2) != 0 {
			_ = source.Close()
			return nil, errAudioConvert
		}
		sampleCount := n / (info.Channels * 2)
		for sample := 0; sample < sampleCount; sample++ {
			absoluteSample := decodedSamples + int64(sample)
			for activeChapter < len(chapters) && absoluteSample >= chapterSample(chapters[activeChapter].EndMs, info.SampleRate) {
				if err := flushChapter(&outputs[activeChapter]); err != nil {
					_ = source.Close()
					return nil, err
				}
				activeChapter++
			}
			if activeChapter >= len(chapters) || absoluteSample < chapterSample(chapters[activeChapter].StartMs, info.SampleRate) {
				continue
			}
			output := &outputs[activeChapter]
			for channel := 0; channel < info.Channels; channel++ {
				offset := (sample*info.Channels + channel) * 2
				value := int16(binary.LittleEndian.Uint16(pcm[offset : offset+2]))
				output.pending[channel][output.count] = float32(value) / 32768
			}
			output.count++
			if output.count == mp3.FrameSize {
				if err := writeFrame(output, output.count); err != nil {
					_ = source.Close()
					return nil, err
				}
			}
		}
		decodedSamples += int64(sampleCount)
		s.updateProgress(j, queueIndex, sourceSize+totalWritten, 0)
		switch {
		case readErr == nil:
			if n == 0 {
				_ = source.Close()
				return nil, errAudioConvert
			}
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			if decodedSamples == 0 {
				_ = source.Close()
				return nil, errAudioConvert
			}
			for activeChapter < len(chapters) {
				if decodedSamples < chapterSample(chapters[activeChapter].EndMs, info.SampleRate) {
					_ = source.Close()
					return nil, errLength
				}
				if err := flushChapter(&outputs[activeChapter]); err != nil {
					_ = source.Close()
					return nil, err
				}
				activeChapter++
			}
			if err := source.Close(); err != nil {
				return nil, errStorage
			}
			goto finalized
		default:
			_ = source.Close()
			return nil, errAudioConvert
		}
	}

finalized:
	files = make([]mediaFile, len(outputs))
	for index := range outputs {
		output := &outputs[index]
		if output.bytes <= 0 || output.output.Sync() != nil || output.output.Close() != nil {
			return nil, errStorage
		}
		output.file.Size = output.bytes
		files[index] = output.file
	}
	for index := range outputs {
		if err := durableRename(outputs[index].part, outputs[index].final); err != nil {
			return nil, errStorage
		}
	}
	if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errStorage
	}
	if err := s.deleteDownloadPart(j, queueIndex, "mp3-source"); err != nil {
		return nil, errStorage
	}
	keepSource = true
	s.updateProgress(j, queueIndex, sourceSize+totalWritten, sourceSize+totalWritten)
	return files, nil
}

func chapterSample(milliseconds int64, sampleRate int) int64 {
	return milliseconds * int64(sampleRate) / 1000
}
