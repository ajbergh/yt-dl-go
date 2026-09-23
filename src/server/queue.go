package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

func (s *server) orderedQueuedJobsLocked() []*jobState {
	queued := make([]*jobState, 0)
	for _, job := range s.jobs {
		if job != nil && job.Status == "queued" {
			queued = append(queued, job)
		}
	}
	sort.SliceStable(queued, func(i, j int) bool {
		left, right := queued[i], queued[j]
		if left.QueuePosition <= 0 && right.QueuePosition > 0 {
			return false
		}
		if right.QueuePosition <= 0 && left.QueuePosition > 0 {
			return true
		}
		if left.QueuePosition != right.QueuePosition {
			return left.QueuePosition < right.QueuePosition
		}
		if left.CreatedAt != right.CreatedAt {
			return left.CreatedAt < right.CreatedAt
		}
		return left.ID < right.ID
	})
	return queued
}

func (s *server) nextQueuedJobLocked() *jobState {
	queued := s.orderedQueuedJobsLocked()
	if len(queued) == 0 {
		return nil
	}
	return queued[0]
}

func (s *server) nextQueuePositionLocked() int64 {
	var maxPosition int64
	for _, job := range s.jobs {
		if job != nil && job.QueuePosition > maxPosition {
			maxPosition = job.QueuePosition
		}
	}
	return maxPosition + 1
}

func queueSnapshots(jobs []*jobState) []Job {
	result := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		result = append(result, snapshot(job))
	}
	return result
}

func validateExactJobOrder(queued []*jobState, requested []string) error {
	if len(requested) != len(queued) {
		return errors.New("queue order must contain every currently queued job exactly once")
	}
	expected := make(map[string]struct{}, len(queued))
	for _, job := range queued {
		expected[job.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		if id == "" {
			return errors.New("queue order contains an empty job ID")
		}
		if _, ok := expected[id]; !ok {
			return errors.New("queue order contains a job that is not currently queued")
		}
		if _, duplicate := seen[id]; duplicate {
			return errors.New("queue order contains a duplicate job")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *server) applyQueueOrderLocked(jobIDs []string) error {
	if err := s.store.saveQueueOrder(jobIDs); err != nil {
		s.recordPersistenceFailure("save queue order", "", err)
		return err
	}
	s.recordPersistenceSuccess()
	for index, id := range jobIDs {
		if job := s.jobs[id]; job != nil {
			job.QueuePosition = int64(index + 1)
			s.publishJobEventLocked("job-status", job)
		}
	}
	s.notifySchedulerLocked()
	return nil
}

func (s *server) handleQueueOrder(w http.ResponseWriter, r *http.Request) {
	var request struct {
		JobIDs []string `json:"jobIds"`
	}
	if !decodeWithLimit(w, r, &request, 128<<10, "128 KiB") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := s.orderedQueuedJobsLocked()
	if err := validateExactJobOrder(queued, request.JobIDs); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.applyQueueOrderLocked(request.JobIDs); err != nil {
		fail(w, http.StatusInternalServerError, "Could not persist queue order")
		return
	}
	reply(w, http.StatusOK, map[string]any{"jobs": queueSnapshots(s.orderedQueuedJobsLocked())})
}

func (s *server) handleDownloadNext(w http.ResponseWriter, r *http.Request, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.jobs[jobID]
	if target == nil {
		fail(w, http.StatusNotFound, "Job not found")
		return
	}
	if target.Status != "queued" {
		fail(w, http.StatusConflict, "Only queued jobs can be moved next")
		return
	}
	queued := s.orderedQueuedJobsLocked()
	ids := make([]string, 0, len(queued))
	ids = append(ids, target.ID)
	for _, job := range queued {
		if job.ID != target.ID {
			ids = append(ids, job.ID)
		}
	}
	if err := s.applyQueueOrderLocked(ids); err != nil {
		fail(w, http.StatusInternalServerError, "Could not persist queue priority")
		return
	}
	reply(w, http.StatusOK, map[string]any{"jobs": queueSnapshots(s.orderedQueuedJobsLocked())})
}

func (s *server) handleItemOrder(w http.ResponseWriter, r *http.Request, jobID string) {
	var request struct {
		PlaylistIndexes []int `json:"playlistIndexes"`
	}
	if !decodeWithLimit(w, r, &request, 128<<10, "128 KiB") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[jobID]
	if job == nil {
		fail(w, http.StatusNotFound, "Job not found")
		return
	}
	if job.Status != "queued" || job.Kind != "playlist" {
		fail(w, http.StatusConflict, "Only queued playlist items can be reordered")
		return
	}
	if len(request.PlaylistIndexes) != len(job.Items) || len(job.Items) < 2 {
		fail(w, http.StatusConflict, "Item order must contain every queued playlist item exactly once")
		return
	}
	byOriginalIndex := make(map[int]queueItem, len(job.Items))
	for _, item := range job.Items {
		if item.PlaylistIndex < 1 {
			fail(w, http.StatusConflict, "This playlist job does not have durable original item indexes")
			return
		}
		if _, duplicate := byOriginalIndex[item.PlaylistIndex]; duplicate {
			fail(w, http.StatusConflict, "Queued playlist metadata contains duplicate original indexes")
			return
		}
		byOriginalIndex[item.PlaylistIndex] = item
	}
	reordered := make([]queueItem, 0, len(job.Items))
	seen := make(map[int]struct{}, len(request.PlaylistIndexes))
	for _, playlistIndex := range request.PlaylistIndexes {
		item, ok := byOriginalIndex[playlistIndex]
		if !ok {
			fail(w, http.StatusConflict, "Item order contains an item that is not currently queued")
			return
		}
		if _, duplicate := seen[playlistIndex]; duplicate {
			fail(w, http.StatusConflict, "Item order contains a duplicate playlist item")
			return
		}
		seen[playlistIndex] = struct{}{}
		item.Index = len(reordered) + 1
		reordered = append(reordered, item)
	}
	previous := job.Items
	job.Items = reordered
	if err := s.store.saveJob(job); err != nil {
		s.recordPersistenceFailure("save playlist item order", job.ID, err)
		job.Items = previous
		fail(w, http.StatusInternalServerError, "Could not persist playlist item order")
		return
	}
	s.recordPersistenceSuccess()
	s.publishJobEventLocked("job-status", job)
	reply(w, http.StatusOK, snapshot(job))
}

// marshalQueueOrder is used only by tests that need a compact validated body.
func marshalQueueOrder(ids []string) string {
	body, _ := json.Marshal(map[string]any{"jobIds": ids})
	return string(body)
}
