package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
)

// Downloading is network bound and converting is CPU bound, so they queue
// separately. An item gives up its download slot the moment its source is on
// disk, letting the next item start downloading while finished sources wait for
// (or use) a conversion slot. Waiting sources are bounded by the worker pool.
const maxConversionSlots = 4

func (s *server) conversionLimit() int {
	if s.convertSlots > 0 {
		return s.convertSlots
	}
	if value, err := strconv.Atoi(os.Getenv("CONVERT_SLOTS")); err == nil && value >= 1 && value <= 16 {
		return value
	}
	return max(1, min(maxConversionSlots, runtime.NumCPU()/2))
}

// beginConversion frees the item's download slot, then waits for a conversion
// slot. It returns false (with the slot state consistent) when ctx ends first.
// Callers pair a true result with endConversion.
func (s *server) beginConversion(ctx context.Context, j *jobState, index int) bool {
	s.mu.Lock()
	if progress := j.itemProgress[index]; progress != nil && !progress.slotReleased {
		progress.slotReleased = true
		if s.activeItems > 0 {
			s.activeItems--
		}
		s.notifySchedulerLocked()
	}
	if progress := j.itemProgress[index]; progress != nil {
		progress.waiting, progress.speed = true, 0
		s.refreshQueueItemLocked(j, index)
		s.publishJobEventLocked("job-status", j)
	}
	s.mu.Unlock()
	for {
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			return false
		}
		if s.activeConversions < s.conversionLimit() {
			s.activeConversions++
			if progress := j.itemProgress[index]; progress != nil {
				progress.waiting = false
				s.refreshQueueItemLocked(j, index)
				s.publishJobEventLocked("job-status", j)
			}
			s.mu.Unlock()
			return true
		}
		changed := s.scheduleChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
}

func (s *server) endConversion() {
	s.mu.Lock()
	if s.activeConversions > 0 {
		s.activeConversions--
	}
	s.notifySchedulerLocked()
	s.mu.Unlock()
}
