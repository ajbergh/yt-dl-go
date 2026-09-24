package main

import (
	"context"
	"io"
	"sync"
	"time"
)

const maxBandwidthLimitBytesPerSec int64 = 1 << 30 // 1 GiB/s safety ceiling.

// Keep this non-zero-sized: Go may reuse pointer addresses for distinct
// zero-sized allocations, which would make FIFO waiter identity ambiguous.
type bandwidthWaiter struct {
	_ byte
}

type bandwidthLimiter struct {
	mu     sync.Mutex
	limit  int64
	tokens float64
	last   time.Time
	queue  []*bandwidthWaiter
	wake   chan struct{}
}

func newBandwidthLimiter(limit int64) *bandwidthLimiter {
	now := time.Now()
	limiter := &bandwidthLimiter{limit: max(int64(0), limit), last: now, wake: make(chan struct{})}
	limiter.tokens = float64(limiter.burstCapacityLocked())
	return limiter
}

func (l *bandwidthLimiter) burstCapacityLocked() int64 {
	if l == nil || l.limit <= 0 {
		return 0
	}
	capacity := l.limit / 20 // at most 50 ms of burst.
	if capacity < 1024 {
		capacity = 1024
	}
	if capacity > 64*1024 {
		capacity = 64 * 1024
	}
	if capacity > l.limit {
		capacity = l.limit
	}
	if capacity < 1 {
		capacity = 1
	}
	return capacity
}

func (l *bandwidthLimiter) refillLocked(now time.Time) {
	if l.limit <= 0 {
		l.tokens = 0
		l.last = now
		return
	}
	if l.last.IsZero() {
		l.last = now
	}
	elapsed := now.Sub(l.last).Seconds()
	if elapsed > 0 {
		l.tokens += elapsed * float64(l.limit)
		capacity := float64(l.burstCapacityLocked())
		if l.tokens > capacity {
			l.tokens = capacity
		}
		l.last = now
	}
}

func (l *bandwidthLimiter) SetLimit(limit int64) {
	if l == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	l.mu.Lock()
	l.limit = limit
	l.last = time.Now()
	l.tokens = float64(l.burstCapacityLocked())
	l.wakeWaitersLocked()
	l.mu.Unlock()
}

// wakeWaitersLocked notifies queued callers that the limit or available tokens
// changed. Replacing a closed channel lets each acquire wait without polling.
func (l *bandwidthLimiter) wakeWaitersLocked() {
	close(l.wake)
	l.wake = make(chan struct{})
}

func (l *bandwidthLimiter) Limit() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

func (l *bandwidthLimiter) removeWaiterLocked(waiter *bandwidthWaiter) {
	for index, queued := range l.queue {
		if queued == waiter {
			copy(l.queue[index:], l.queue[index+1:])
			l.queue = l.queue[:len(l.queue)-1]
			if index == 0 {
				l.wakeWaitersLocked()
			}
			return
		}
	}
}

func (l *bandwidthLimiter) tryGrantLocked(waiter *bandwidthWaiter, requested int, now time.Time) int {
	if l.limit <= 0 {
		l.removeWaiterLocked(waiter)
		return requested
	}
	l.refillLocked(now)
	if len(l.queue) == 0 || l.queue[0] != waiter || l.tokens < 1 {
		return 0
	}
	grant := requested
	if capacity := int(l.burstCapacityLocked()); grant > capacity {
		grant = capacity
	}
	if available := int(l.tokens); grant > available {
		grant = available
	}
	if grant < 1 {
		grant = 1
	}
	l.tokens -= float64(grant)
	l.queue = l.queue[1:]
	l.wakeWaitersLocked()
	return grant
}

// waitDurationLocked returns the time until the FIFO head can receive a useful
// read chunk. Non-head waiters sleep until the queue or limit changes.
func (l *bandwidthLimiter) waitDurationLocked(waiter *bandwidthWaiter, requested int, now time.Time) time.Duration {
	if l.limit <= 0 || len(l.queue) == 0 || l.queue[0] != waiter {
		return 0
	}
	l.refillLocked(now)
	target := min(requested, int(l.burstCapacityLocked()))
	if target <= 0 || l.tokens >= float64(target) {
		return 0
	}
	missing := float64(target) - l.tokens
	wait := time.Duration(missing / float64(l.limit) * float64(time.Second))
	if wait < time.Nanosecond {
		return time.Nanosecond
	}
	return wait
}

// acquire grants read capacity in FIFO order. Each caller re-enters the queue
// after every grant, producing round-robin sharing among concurrent transfers.
func (l *bandwidthLimiter) acquire(ctx context.Context, requested int) (int, error) {
	if requested <= 0 {
		return 0, nil
	}
	if l == nil {
		return requested, nil
	}
	waiter := &bandwidthWaiter{}
	l.mu.Lock()
	if l.limit <= 0 {
		l.mu.Unlock()
		return requested, nil
	}
	l.queue = append(l.queue, waiter)
	l.mu.Unlock()

	for {
		l.mu.Lock()
		if err := ctx.Err(); err != nil {
			l.removeWaiterLocked(waiter)
			l.mu.Unlock()
			return 0, err
		}
		grant := l.tryGrantLocked(waiter, requested, time.Now())
		if grant > 0 {
			l.mu.Unlock()
			return grant, nil
		}
		wake := l.wake
		wait := l.waitDurationLocked(waiter, requested, time.Now())
		l.mu.Unlock()

		if wait <= 0 {
			select {
			case <-ctx.Done():
			case <-wake:
			}
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (l *bandwidthLimiter) refund(bytes int) {
	if l == nil || bytes <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		return
	}
	l.refillLocked(time.Now())
	l.tokens += float64(bytes)
	if capacity := float64(l.burstCapacityLocked()); l.tokens > capacity {
		l.tokens = capacity
	}
	l.wakeWaitersLocked()
}

type bandwidthReader struct {
	ctx     context.Context
	reader  io.Reader
	limiter *bandwidthLimiter
}

func (r bandwidthReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	grant, err := r.limiter.acquire(r.ctx, len(buffer))
	if err != nil {
		return 0, err
	}
	n, readErr := r.reader.Read(buffer[:grant])
	if n < grant {
		r.limiter.refund(grant - max(0, n))
	}
	return n, readErr
}

func (s *server) bandwidthReader(ctx context.Context, reader io.Reader) io.Reader {
	if s == nil || s.bandwidth == nil {
		return reader
	}
	return bandwidthReader{ctx: ctx, reader: reader, limiter: s.bandwidth}
}

func (s *server) bandwidthLimited() bool {
	return s != nil && s.bandwidth != nil && s.bandwidth.Limit() > 0
}
