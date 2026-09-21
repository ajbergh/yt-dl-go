package main

import (
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
