package main

import (
	"context"
	"testing"
	"time"
)

func waitBandwidthQueue(t *testing.T, limiter *bandwidthLimiter, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
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

func grantBandwidth(limiter *bandwidthLimiter, bytes int) {
	limiter.mu.Lock()
	limiter.tokens = float64(bytes)
	limiter.mu.Unlock()
}

func TestBandwidthLimiterFairFIFOGrants(t *testing.T) {
	limiter := newBandwidthLimiter(1024 * 1024)
	freezeBandwidthRefill(limiter)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := []chan int{make(chan int, 1), make(chan int, 1), make(chan int, 1)}
	for index := range results {
		go func(result chan int) {
			n, err := limiter.acquire(ctx, 1024)
			if err != nil {
				result <- -1
				return
			}
			result <- n
		}(results[index])
		waitBandwidthQueue(t, limiter, index+1)
	}

	for index, result := range results {
		grantBandwidth(limiter, 1024)
		select {
		case n := <-result:
			if n != 1024 {
				t.Fatalf("waiter %d grant = %d, want 1024", index+1, n)
			}
		case <-time.After(time.Second):
			t.Fatalf("waiter %d was not granted in FIFO order", index+1)
		}
		for later := index + 1; later < len(results); later++ {
			select {
			case n := <-results[later]:
				t.Fatalf("waiter %d bypassed FIFO with grant %d", later+1, n)
			default:
			}
		}
	}
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
		t.Fatal("cancelled bandwidth wait did not stop")
	}
	waitBandwidthQueue(t, limiter, 0)
}
