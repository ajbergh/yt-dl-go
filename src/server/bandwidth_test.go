package main

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func waitBandwidthQueue(t *testing.T, limiter *bandwidthLimiter, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		limiter.mu.Lock()
		got := len(limiter.queue)
		limiter.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("bandwidth waiter queue did not reach %d", want)
}

func freezeBandwidthRefill(limiter *bandwidthLimiter) {
	limiter.mu.Lock()
	limiter.tokens = 0
	limiter.last = time.Now().Add(time.Hour)
	limiter.mu.Unlock()
}

func TestBandwidthLimiterFairFIFOGrants(t *testing.T) {
	limiter := newBandwidthLimiter(1024 * 1024)
	waiters := []*bandwidthWaiter{{}, {}, {}}

	limiter.mu.Lock()
	limiter.queue = append(limiter.queue, waiters...)
	limiter.tokens = 1024
	limiter.last = time.Now().Add(time.Hour)

	if grant := limiter.tryGrantLocked(waiters[1], 1024, time.Now()); grant != 0 {
		limiter.mu.Unlock()
		t.Fatalf("second waiter bypassed FIFO with grant %d", grant)
	}
	if grant := limiter.tryGrantLocked(waiters[0], 1024, time.Now()); grant != 1024 {
		limiter.mu.Unlock()
		t.Fatalf("first waiter grant = %d, want 1024", grant)
	}

	limiter.tokens = 1024
	if grant := limiter.tryGrantLocked(waiters[2], 1024, time.Now()); grant != 0 {
		limiter.mu.Unlock()
		t.Fatalf("third waiter bypassed second with grant %d", grant)
	}
	if grant := limiter.tryGrantLocked(waiters[1], 1024, time.Now()); grant != 1024 {
		limiter.mu.Unlock()
		t.Fatalf("second waiter grant = %d, want 1024", grant)
	}

	limiter.tokens = 1024
	if grant := limiter.tryGrantLocked(waiters[2], 1024, time.Now()); grant != 1024 {
		limiter.mu.Unlock()
		t.Fatalf("third waiter grant = %d, want 1024", grant)
	}
	if len(limiter.queue) != 0 {
		limiter.mu.Unlock()
		t.Fatalf("FIFO queue retained %d waiter(s)", len(limiter.queue))
	}
	limiter.mu.Unlock()
}

func TestBandwidthLimiterLiveUnlimitedRelease(t *testing.T) {
	limiter := newBandwidthLimiter(1024)
	freezeBandwidthRefill(limiter)
	done := make(chan error, 1)
	go func() {
		_, err := limiter.acquire(context.Background(), 1024)
		done <- err
	}()
	waitBandwidthQueue(t, limiter, 1)

	limiter.SetLimit(0)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("switching to unlimited did not release a waiting transfer")
	}
	if limiter.Limit() != 0 {
		t.Fatalf("limit = %d, want unlimited", limiter.Limit())
	}
}

func TestBandwidthLimiterSubNanosecondWaitMakesProgress(t *testing.T) {
	limiter := newBandwidthLimiter(maxBandwidthLimitBytesPerSec)
	limiter.mu.Lock()
	limiter.tokens = 0
	limiter.last = time.Now()
	waiter := &bandwidthWaiter{}
	limiter.queue = append(limiter.queue, waiter)
	wait := limiter.waitDurationLocked(waiter, 1, limiter.last)
	limiter.queue = nil
	limiter.mu.Unlock()
	if wait != time.Nanosecond {
		t.Fatalf("sub-nanosecond token wait = %s, want 1ns minimum", wait)
	}

	done := make(chan int, 1)
	go func() {
		grant, err := limiter.acquire(context.Background(), 1)
		if err != nil {
			done <- -1
			return
		}
		done <- grant
	}()

	select {
	case grant := <-done:
		if grant != 1 {
			t.Fatalf("grant = %d, want 1", grant)
		}
	case <-time.After(time.Second):
		t.Fatal("sub-nanosecond token delay did not make progress")
	}
}

func TestBandwidthReaderAppliesLimitChangedAfterConstruction(t *testing.T) {
	limiter := newBandwidthLimiter(0)
	reader := (&server{bandwidth: limiter}).bandwidthReader(context.Background(), bytes.NewReader([]byte("data")))
	limiter.SetLimit(1024)
	freezeBandwidthRefill(limiter)

	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4)
		_, err := reader.Read(buffer)
		readDone <- err
	}()
	waitBandwidthQueue(t, limiter, 1)

	limiter.SetLimit(0)
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader created while unlimited did not observe the live limit change")
	}
}

func TestBandwidthLimiterRejectsContextAfterQueueing(t *testing.T) {
	limiter := newBandwidthLimiter(1024)
	freezeBandwidthRefill(limiter)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := limiter.acquire(ctx, 1024)
		done <- err
	}()
	waitBandwidthQueue(t, limiter, 1)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled bandwidth wait returned nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled bandwidth wait did not stop")
	}
	waitBandwidthQueue(t, limiter, 0)
}
