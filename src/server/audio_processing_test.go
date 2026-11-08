package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestAudioProgressCoalescesFramesAndReportsFinalBytes(t *testing.T) {
	var reported []int64
	progress := audioProgress{callback: func(value int64) { reported = append(reported, value) }}
	progress.update(1)
	progress.last = time.Now().Add(time.Hour)
	for value := int64(2); value <= 10000; value++ {
		progress.update(value)
	}
	if len(reported) != 1 {
		t.Fatalf("per-frame progress was not coalesced: %d callbacks", len(reported))
	}
	progress.last = time.Now().Add(-time.Second)
	progress.update(10001)
	progress.update(10002)
	progress.flush()
	progress.flush()
	if len(reported) != 3 || reported[0] != 1 || reported[1] != 10001 || reported[2] != 10002 {
		t.Fatalf("progress lost final bytes or emitted duplicates: %v", reported)
	}
}

type failingPCMReader struct {
	*bytes.Reader
	failure error
}

func (r failingPCMReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if errors.Is(err, io.EOF) {
		err = r.failure
	}
	return n, err
}

func TestAudioPCMPrefetchPreservesSamplesAndReadErrors(t *testing.T) {
	for _, finalError := range []error{io.EOF, errors.New("damaged AAC frame")} {
		payload := make([]byte, 64*16*7+19)
		for i := range payload {
			payload[i] = byte(i * 31)
		}
		stream := newAudioPCMStream(context.Background(), failingPCMReader{bytes.NewReader(payload), finalError}, 64)
		got, err := io.ReadAll(stream)
		stream.Close()
		if !bytes.Equal(got, payload) {
			t.Fatal("prefetch reordered or dropped PCM samples")
		}
		if errors.Is(finalError, io.EOF) {
			// io.ReadFull translates an incomplete final batch to UnexpectedEOF.
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("final batch error = %v", err)
			}
		} else if !errors.Is(err, finalError) {
			t.Fatalf("decoder error = %v", err)
		}
	}
}

type countingPCMReader struct {
	calls atomic.Int32
	reads chan struct{}
}

func (r *countingPCMReader) Read(data []byte) (int, error) {
	r.calls.Add(1)
	r.reads <- struct{}{}
	clear(data)
	return len(data), nil
}

func TestAudioPCMPrefetchIsBoundedAndCancellationJoinsDecoder(t *testing.T) {
	reader := &countingPCMReader{reads: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	stream := newAudioPCMStream(ctx, reader, 64)
	// Two queued batches plus the producer's batch use all three buffers.
	for range 3 {
		select {
		case <-reader.reads:
		case <-time.After(time.Second):
			t.Fatal("decoder did not run ahead of encoding")
		}
	}
	cancel()
	stream.Close()
	if calls := reader.calls.Load(); calls != 3 {
		t.Fatalf("unbounded PCM buffering: %d batches", calls)
	}
	if _, err := stream.Read(make([]byte, 64)); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancellation: %v", err)
	}
}
