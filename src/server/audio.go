package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	aacm4a "github.com/tphakala/go-m4a/aacm4a"
	"github.com/tphakala/go-mp3"
)

const audioOutputBufferSize = 64 * 1024

// audioProgress coalesces callbacks before they take the shared job lock.
type audioProgress struct {
	callback        func(int64)
	last            time.Time
	value, reported int64
}

func (p *audioProgress) update(value int64) {
	p.value = value
	if p.callback != nil && (p.last.IsZero() || time.Since(p.last) >= 200*time.Millisecond) {
		p.flush()
	}
}

func (p *audioProgress) flush() {
	if p.callback != nil && p.value != p.reported {
		p.callback(p.value)
		p.reported = p.value
		p.last = time.Now()
	}
}

type audioPCMBlock struct {
	data []byte
	err  error
}

// audioPCMStream overlaps the local AAC decoder with the stateful MP3 encoder.
// Three reusable buffers bound the read-ahead; only the producer reads decoder.
// Close joins the producer before the caller closes its seekable source file.
type audioPCMStream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	ready   chan audioPCMBlock
	free    chan []byte
	done    chan struct{}
	current []byte
	offset  int
	err     error
}

func newAudioPCMStream(ctx context.Context, decoder io.Reader, frameBytes int) *audioPCMStream {
	ctx, cancel := context.WithCancel(ctx)
	p := &audioPCMStream{ctx: ctx, cancel: cancel, ready: make(chan audioPCMBlock, 2), free: make(chan []byte, 3), done: make(chan struct{})}
	for range 3 {
		p.free <- make([]byte, frameBytes*16)
	}
	go func() {
		defer close(p.done)
		defer close(p.ready)
		for {
			var buffer []byte
			select {
			case <-ctx.Done():
				return
			case buffer = <-p.free:
			}
			if ctx.Err() != nil {
				return
			}
			n, err := io.ReadFull(decoder, buffer)
			select {
			case <-ctx.Done():
				return
			case p.ready <- audioPCMBlock{buffer[:n], err}:
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

func (p *audioPCMStream) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if p.offset == len(p.current) {
		if p.current != nil {
			p.free <- p.current[:cap(p.current)]
			p.current = nil
		}
		if p.err != nil {
			return 0, p.err
		}
		select {
		case <-p.ctx.Done():
			return 0, p.ctx.Err()
		case block, ok := <-p.ready:
			if !ok {
				return 0, io.EOF
			}
			p.current, p.offset, p.err = block.data, 0, block.err
		}
	}
	n := copy(buffer, p.current[p.offset:])
	p.offset += n
	if p.offset == len(p.current) {
		return n, p.err
	}
	return n, nil
}

func (p *audioPCMStream) Close() { p.cancel(); <-p.done }

func convertAACToMP3Tagged(ctx context.Context, source io.ReadSeeker, output io.Writer, bitrate string, budget int64, tag []byte, progress func(int64)) (int64, error) {
	var started time.Time
	if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
		started = time.Now()
	}
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
		// Fast gain search is roughly an order of magnitude quicker on dense
		// music and diverges from exact on well under 0.1% of granules.
		RateControl: mp3.RateControlFast,
	})
	if err != nil {
		return 0, errAudioConvert
	}

	frameBytes := mp3.FrameSize * info.Channels * 2
	decoded := newAudioPCMStream(ctx, decoder, frameBytes)
	defer decoded.Close()
	bufferedOutput := bufio.NewWriterSize(output, audioOutputBufferSize)
	reporter := audioProgress{callback: progress}
	pcm := make([]byte, frameBytes)
	planar := make([][]float32, info.Channels)
	for channel := range planar {
		planar[channel] = make([]float32, mp3.FrameSize)
	}
	frames := make([][]float32, info.Channels)
	encoded := make([]byte, 0, 8192)
	var written int64
	var decodedSamples int64
	if !started.IsZero() {
		defer func() {
			log.Printf("audio conversion elapsed=%s sample_rate=%d channels=%d samples=%d output_bytes=%d", time.Since(started).Round(time.Millisecond), info.SampleRate, info.Channels, decodedSamples, written)
		}()
	}
	writeEncoded := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if int64(len(data)) > budget-written {
			return errLimit
		}
		n, writeErr := bufferedOutput.Write(data)
		if writeErr != nil {
			return errStorage
		}
		if n != len(data) {
			return errStorage
		}
		written += int64(n)
		reporter.update(written)
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
		n, readErr := io.ReadFull(decoded, pcm)
		if err := ctx.Err(); err != nil {
			return written, err
		}
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
			if err := bufferedOutput.Flush(); err != nil {
				return written, errStorage
			}
			reporter.flush()
			return written, nil
		default:
			return written, errAudioConvert
		}
	}
}
