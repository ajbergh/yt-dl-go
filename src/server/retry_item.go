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

	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[jobID]
	if job == nil {
		fail(w, http.StatusNotFound, "Job not found")
		return
	}
	if job.Kind != "playlist" || (job.Status != "partial" && job.Status != "failed" && job.Status != "cancelled") {
		fail(w, http.StatusConflict, "Single-item retry is available only for stopped playlist jobs")
		return
	}
	if request.Index < 1 || request.Index > len(job.Items) {
		fail(w, http.StatusBadRequest, "index must identify one playlist queue item")
		return
	}
	item := job.Items[request.Index-1]
	if item.Index != request.Index || (item.Status != "failed" && item.Status != "cancelled") {
		fail(w, http.StatusConflict, "Only failed or cancelled playlist items can be retried")
		return
	}
	if files := itemFiles(job, request.Index); len(files) > 0 {
		valid := true
		for _, file := range files {
			if !s.validCompletedFile(job, file) {
				valid = false
				break
			}
		}
		if valid {
			fail(w, http.StatusConflict, "This playlist item already has valid finalized media")
			return
		}
	}
	if s.activeJobCountLocked() >= s.cfg.maxJobs {
		fail(w, http.StatusTooManyRequests, "Active job capacity reached; wait for a job to finish or cancel one")
		return
	}

	oldJob := job.Job
	oldItems := append([]queueItem(nil), job.Items...)
	oldFailures := append([]itemFailure(nil), job.Failures...)
	oldDone := job.done
	oldCancelRequested, oldPauseRequested := job.cancelRequested, job.pauseRequested

	for index := range job.Items {
		job.Items[index].RetryRequested = index == request.Index-1
	}
	failures := job.Failures[:0]
	for _, failure := range job.Failures {
		if failure.Index != request.Index {
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

	if err := s.store.saveJob(job); err != nil {
		s.recordPersistenceFailure("save item retry state", job.ID, err)
		job.Job = oldJob
		job.Items = oldItems
		job.Failures = oldFailures
		job.done = oldDone
		job.cancelRequested, job.pauseRequested = oldCancelRequested, oldPauseRequested
		fail(w, http.StatusInternalServerError, "Could not persist item retry state")
		return
	}
	s.recordPersistenceSuccess()
	s.publishJobEventLocked("job-status", job)
	s.notifySchedulerLocked()
	reply(w, http.StatusAccepted, snapshot(job))
}
