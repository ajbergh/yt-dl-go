package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"strings"

	aacm4a "github.com/tphakala/go-m4a/aacm4a"
	"github.com/tphakala/go-mp3"
)

// convertAACToMP3 decodes the AAC-LC audio track from an MP4/M4A stream and
// encodes it as CBR MP3, entirely in-process with Go libraries.
func convertAACToMP3(ctx context.Context, source io.ReadSeeker, output io.Writer, bitrate string, budget int64, progress func(int64)) (int64, error) {
	return convertAACToMP3Tagged(ctx, source, output, bitrate, budget, nil, progress)
}

func convertAACToMP3Tagged(ctx context.Context, source io.ReadSeeker, output io.Writer, bitrate string, budget int64, tag []byte, progress func(int64)) (int64, error) {
	if budget <= 0 {
		return 0, errLimit
	}
	decoder, info, err := aacm4a.NewDecoder(source)
	if err != nil || decoder == nil || info.Channels < 1 || info.Channels > 2 {
		return 0, errAudioConvert
	}
	bitrateKbps, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
	if err != nil {
		return 0, errAudioConvert
	}
	encoder, err := mp3.NewEncoder(mp3.EncoderConfig{
		SampleRate: info.SampleRate,
		Channels:   info.Channels,
		Bitrate:    bitrateKbps * 1000,
	})
	if err != nil {
		return 0, errAudioConvert
	}

	frameBytes := mp3.FrameSize * info.Channels * 2
	pcm := make([]byte, frameBytes)
	planar := make([][]float32, info.Channels)
	for channel := range planar {
		planar[channel] = make([]float32, mp3.FrameSize)
	}
	frames := make([][]float32, info.Channels)
	encoded := make([]byte, 0, 8192)
	var written int64
	var decodedSamples int64
	writeEncoded := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if int64(len(data)) > budget-written {
			return errLimit
		}
		n, writeErr := output.Write(data)
		if writeErr != nil {
			return errStorage
		}
		if n != len(data) {
			return errStorage
		}
		written += int64(n)
		if progress != nil {
			progress(written)
		}
		return nil
	}
	if len(tag) > 0 {
		if err := writeEncoded(tag); err != nil {
			return written, err
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := io.ReadFull(decoder, pcm)
		if n%(info.Channels*2) != 0 {
			return written, errAudioConvert
		}
		sampleCount := n / (info.Channels * 2)
		decodedSamples += int64(sampleCount)
		for sample := 0; sample < sampleCount; sample++ {
			for channel := 0; channel < info.Channels; channel++ {
				offset := (sample*info.Channels + channel) * 2
				value := int16(binary.LittleEndian.Uint16(pcm[offset : offset+2]))
				planar[channel][sample] = float32(value) / 32768
			}
		}
		if sampleCount > 0 {
			for channel := range frames {
				frames[channel] = planar[channel][:sampleCount]
			}
			encoded, err = encoder.EncodeFrame(encoded[:0], frames)
			if err != nil {
				return written, errAudioConvert
			}
			if err := writeEncoded(encoded); err != nil {
				return written, err
			}
		}

		switch {
		case readErr == nil:
			if n == 0 {
				return written, errAudioConvert
			}
			continue
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			if err := ctx.Err(); err != nil {
				return written, err
			}
			if decodedSamples == 0 {
				return written, errAudioConvert
			}
			encoded, err = encoder.EncodeFrame(encoded[:0], nil)
			if err != nil {
				return written, errAudioConvert
			}
			if err := writeEncoded(encoded); err != nil {
				return written, err
			}
			return written, nil
		default:
			return written, errAudioConvert
		}
	}
}
