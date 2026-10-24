package main

import (
	"net/http"
	"time"
)

func (s *server) handleRetryItem(w http.ResponseWriter, r *http.Request, jobID string) {
	var request struct {
		Index int `json:"index"`
	}
	if !decode(w, r, &request) {
		return
	}
	s.retryPlaylistItems(w, jobID, &request.Index)
}

func (s *server) handleRetryFailed(w http.ResponseWriter, _ *http.Request, jobID string) {
	s.retryPlaylistItems(w, jobID, nil)
}

func (s *server) retryPlaylistItems(w http.ResponseWriter, jobID string, requestedIndex *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[jobID]
	if job == nil {
		fail(w, http.StatusNotFound, "Job not found")
		return
	}
	if job.Kind != "playlist" || (job.Status != "partial" && job.Status != "failed" && job.Status != "cancelled") {
		fail(w, http.StatusConflict, "Item retry is available only for stopped playlist jobs")
		return
	}
	if requestedIndex != nil && (*requestedIndex < 1 || *requestedIndex > len(job.Items)) {
		fail(w, http.StatusBadRequest, "index must identify one playlist queue item")
		return
	}
	retry := make(map[int]struct{})
	for itemIndex, item := range job.Items {
		index := item.Index
		if requestedIndex != nil && index != *requestedIndex {
			continue
		}
		if index < 1 || index > len(job.Items) || item.Index != index || (item.Status != "failed" && item.Status != "cancelled") {
			continue
		}
		files := itemFiles(job, index)
		validFinalized := len(files) > 0
		for _, file := range files {
			if !s.validCompletedFile(job, file) {
				validFinalized = false
				break
			}
		}
		if validFinalized {
			continue
		}
		retry[itemIndex] = struct{}{}
	}
	if len(retry) == 0 {
		if requestedIndex != nil {
			fail(w, http.StatusConflict, "Only failed or cancelled playlist items without valid finalized media can be retried")
		} else {
			fail(w, http.StatusConflict, "This playlist has no failed items available to retry")
		}
		return
	}
	if s.activeJobCountLocked() >= s.cfg.maxJobs {
		fail(w, http.StatusTooManyRequests, "Active job capacity reached; wait for a job to finish or cancel one")
		return
	}

	previous := cloneJobStateForPersistence(job)

	for index := range job.Items {
		_, job.Items[index].RetryRequested = retry[index]
	}
	failures := job.Failures[:0]
	for _, failure := range job.Failures {
		if _, retrying := retry[failure.Index-1]; !retrying {
			failures = append(failures, failure)
		}
	}
	job.Failures = failures
	job.Status, job.Error, job.CurrentItem = "queued", "", ""
	job.Progress = nil
	job.DownloadedBytes, job.TotalBytes, job.SpeedBytesPerSec, job.ETASeconds, job.ActiveItemCount = 0, 0, 0, 0, 0
	job.done = time.Time{}
	job.cancelRequested, job.pauseRequested = false, false
	job.processingItems = 0
	job.itemProgress = nil
	job.QueuePosition = s.nextQueuePositionLocked()
	s.refreshAllQueueItemsLocked(job)

	if err := s.persistJobSnapshotLocked(job, "save item retry state"); err != nil {
		*job = *previous
		fail(w, http.StatusInternalServerError, "Could not persist item retry state")
		return
	}
	s.publishJobEventLocked("job-status", job)
	s.notifySchedulerLocked()
	reply(w, http.StatusAccepted, snapshot(job))
}
