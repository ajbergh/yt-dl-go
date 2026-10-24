package main

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const maxBulkJobIDs = 100

type bulkJobFailure struct {
	JobID string `json:"jobId"`
	Error string `json:"error"`
}

type bulkJobResponse struct {
	Jobs       []Job            `json:"jobs"`
	DeletedIDs []string         `json:"deletedIds"`
	Failures   []bulkJobFailure `json:"failures"`
}

func (s *server) handleBulkJobs(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action string   `json:"action"`
		JobIDs []string `json:"jobIds"`
	}
	if !decode(w, r, &request) {
		return
	}
	if request.Action != "pause" && request.Action != "resume" && request.Action != "cancel" && request.Action != "delete" {
		fail(w, http.StatusBadRequest, "action must be pause, resume, cancel, or delete")
		return
	}
	if len(request.JobIDs) == 0 || len(request.JobIDs) > maxBulkJobIDs {
		fail(w, http.StatusBadRequest, "jobIds must contain between 1 and 100 entries")
		return
	}
	seen := make(map[string]struct{}, len(request.JobIDs))
	for _, id := range request.JobIDs {
		if strings.TrimSpace(id) == "" {
			fail(w, http.StatusBadRequest, "jobIds must contain nonempty IDs")
			return
		}
		if _, ok := seen[id]; ok {
			fail(w, http.StatusBadRequest, "jobIds must be unique")
			return
		}
		seen[id] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	response := bulkJobResponse{Jobs: []Job{}, DeletedIDs: []string{}, Failures: []bulkJobFailure{}}
	// Validate every target before mutating any of them. Valid targets are then
	// processed independently; failures are reported per ID as cleanup and DB
	// operations cannot be rolled back together.
	eligible := make(map[string]*jobState, len(request.JobIDs))
	for _, id := range request.JobIDs {
		job, _, loadErr := s.hydrateTerminalJobLocked(id)
		if loadErr != nil {
			response.Failures = append(response.Failures, bulkJobFailure{id, "Could not load saved job: " + loadErr.Error()})
			continue
		}
		if job == nil {
			response.Failures = append(response.Failures, bulkJobFailure{id, "Job not found"})
			continue
		}
		if job.deleting {
			response.Failures = append(response.Failures, bulkJobFailure{id, "Job is being removed or updated"})
			continue
		}
		if message := validateBulkJobAction(job, request.Action); message != "" {
			response.Failures = append(response.Failures, bulkJobFailure{id, message})
			continue
		}
		eligible[id] = job
	}

	for _, id := range request.JobIDs {
		job := eligible[id]
		if job == nil {
			continue
		}
		message := validateBulkJobAction(job, request.Action)
		if job.deleting || message != "" {
			if message == "" {
				message = "Job is being removed or updated"
			}
			response.Failures = append(response.Failures, bulkJobFailure{id, message})
			continue
		}
		if request.Action == "delete" {
			job.deleting = true
			if err := removeManagedCopies(job); err != nil {
				job.deleting = false
				response.Failures = append(response.Failures, bulkJobFailure{id, "Could not remove the job's private managed files: " + err.Error()})
				continue
			}
			if err := s.persistOperationLocked("delete job", job.ID, func(store *jobStore) error {
				return store.deleteJob(job.ID)
			}); err != nil {
				job.deleting = false
				s.recordPersistenceFailure("delete job", job.ID, err)
				response.Failures = append(response.Failures, bulkJobFailure{id, "Managed files were removed, but job history could not be deleted: " + err.Error()})
				continue
			}
			s.recordPersistenceSuccess()
			s.forgetJobLocked(job)
			response.DeletedIDs = append(response.DeletedIDs, id)
			continue
		}

		previous := cloneJobStateForPersistence(job)
		var cancel context.CancelFunc
		switch request.Action {
		case "pause":
			if job.Status == "queued" {
				job.Status, job.Error = "paused", ""
				s.notifySchedulerLocked()
			} else if job.Status == "downloading" || job.Status == "processing" {
				job.pauseRequested = true
				job.Error = "Pausing after the current download or conversion stops"
				cancel = job.cancel
			}
		case "resume":
			job.pauseRequested, job.cancelRequested = false, false
			job.Status, job.Error, job.done = "queued", "", time.Time{}
			job.QueuePosition = s.nextQueuePositionLocked()
			s.refreshAllQueueItemsLocked(job)
		case "cancel":
			if job.Status == "paused" {
				if err := discardPausedParts(job); err != nil {
					*job = *previous
					response.Failures = append(response.Failures, bulkJobFailure{id, "Could not remove paused temporary output: " + err.Error()})
					continue
				}
				if err := s.persistOperationLocked("delete resumable download state", job.ID, func(store *jobStore) error {
					return store.deletePartsForJob(job.ID)
				}); err != nil {
					*job = *previous
					s.recordPersistenceFailure("delete resumable download state", job.ID, err)
					response.Failures = append(response.Failures, bulkJobFailure{id, "Temporary output was removed, but resumable state could not be cleared: " + err.Error()})
					continue
				}
				s.recordPersistenceSuccess()
			}
			job.pauseRequested = false
			job.cancelRequested = true
			if job.cancel != nil {
				job.Error = "Cancellation requested; waiting for the downloader to stop"
				cancel = job.cancel
			} else if !terminal(job.Status) {
				job.Status, job.Error, job.done = "cancelled", "Cancelled before download started", time.Now()
				s.notifySchedulerLocked()
			}
			s.refreshAllQueueItemsLocked(job)
		}

		if err := s.persistJobSnapshotLocked(job, "save bulk job action"); err != nil {
			*job = *previous
			response.Failures = append(response.Failures, bulkJobFailure{id, "Could not persist job state: " + err.Error()})
			continue
		}
		s.publishJobEventLocked("job-status", job)
		if cancel != nil {
			cancel()
		}
		if request.Action == "resume" || request.Action == "cancel" {
			s.notifySchedulerLocked()
		}
		response.Jobs = append(response.Jobs, snapshot(job))
		if request.Action == "cancel" {
			s.evictTerminalJobLocked(job)
		}
	}
	reply(w, http.StatusOK, response)
}

func validateBulkJobAction(job *jobState, action string) string {
	switch action {
	case "pause":
		if job.Status != "queued" && job.Status != "downloading" && job.Status != "processing" && job.Status != "paused" {
			return "Only queued, downloading, processing, or paused jobs can be paused"
		}
	case "resume":
		if job.Status != "paused" {
			return "Only paused jobs can be resumed"
		}
	case "cancel":
		// Cancellation of terminal jobs is idempotent.
	case "delete":
		if !terminal(job.Status) {
			return "Only stopped jobs can be deleted"
		}
		if job.readers > 0 {
			return "This job has an active file transfer"
		}
	default:
		return "Unsupported action"
	}
	return ""
}

func (s *server) handleJobConfiguration(w http.ResponseWriter, r *http.Request, jobID string) {
	var request struct {
		Quality       *string `json:"quality"`
		VideoStrategy *string `json:"videoStrategy"`
		AudioFormat   *string `json:"audioFormat"`
		AudioBitrate  *string `json:"audioBitrate"`
		Category      *string `json:"category"`
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
	if job.deleting {
		fail(w, http.StatusConflict, "This job is being removed or updated")
		return
	}
	if job.Status != "queued" && job.Status != "paused" {
		fail(w, http.StatusConflict, "Only queued or paused jobs can be reconfigured")
		return
	}
	if request.Quality == nil && request.VideoStrategy == nil && request.AudioFormat == nil && request.AudioBitrate == nil && request.Category == nil {
		fail(w, http.StatusBadRequest, "At least one configuration field is required")
		return
	}

	updated := job.Job
	if request.Quality != nil {
		updated.Quality = strings.TrimSpace(*request.Quality)
	}
	if updated.Quality != "best" && updated.Quality != "2160" && updated.Quality != "1440" && updated.Quality != "1080" && updated.Quality != "720" && updated.Quality != "480" {
		fail(w, http.StatusBadRequest, "quality must be best, 2160, 1440, 1080, 720, or 480")
		return
	}
	if request.VideoStrategy != nil {
		updated.VideoStrategy = strings.ToLower(strings.TrimSpace(*request.VideoStrategy))
	}
	if updated.MediaType == "video" {
		if updated.VideoStrategy == "" {
			updated.VideoStrategy = mergeAppSettings(defaultAppSettings(), s.settings).DefaultVideoStrategy
		}
		if !validVideoStrategy(updated.VideoStrategy) {
			fail(w, http.StatusBadRequest, "videoStrategy must be best, compatibility, vp9, or av1")
			return
		}
	} else if request.VideoStrategy != nil {
		fail(w, http.StatusBadRequest, "videoStrategy is valid only for video jobs")
		return
	}
	if request.AudioFormat != nil {
		updated.AudioFormat = strings.ToLower(strings.TrimSpace(*request.AudioFormat))
		if updated.AudioFormat == "" {
			updated.AudioFormat = "mp3"
		}
	}
	if request.AudioBitrate != nil {
		updated.AudioBitrate = strings.TrimSpace(*request.AudioBitrate)
	}
	if updated.MediaType == "audio" {
		if updated.AudioFormat == "" {
			updated.AudioFormat = "mp3"
		}
		if updated.AudioFormat != "mp3" && updated.AudioFormat != "m4a" {
			fail(w, http.StatusBadRequest, "audioFormat must be mp3 or m4a")
			return
		}
		if updated.AudioFormat == "mp3" {
			if updated.AudioBitrate == "" {
				updated.AudioBitrate = "192k"
			}
			if updated.AudioBitrate != "128k" && updated.AudioBitrate != "192k" && updated.AudioBitrate != "256k" && updated.AudioBitrate != "320k" {
				fail(w, http.StatusBadRequest, "audioBitrate must be 128k, 192k, 256k, or 320k")
				return
			}
		} else {
			if request.AudioBitrate != nil && strings.TrimSpace(*request.AudioBitrate) != "" {
				fail(w, http.StatusBadRequest, "audioBitrate is valid only for MP3 output")
				return
			}
			updated.AudioBitrate = ""
		}
	} else {
		if request.AudioFormat != nil || request.AudioBitrate != nil {
			fail(w, http.StatusBadRequest, "audioFormat and audioBitrate are valid only for audio jobs")
			return
		}
	}
	if request.Category != nil {
		category, err := resolveJobCategory(mergeAppSettings(defaultAppSettings(), s.settings), *request.Category)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		updated.Category = category
	}

	previous := job.Job
	job.Job = updated
	if err := s.persistJobSnapshotLocked(job, "save queued job configuration"); err != nil {
		job.Job = previous
		fail(w, http.StatusInternalServerError, "Could not persist job configuration")
		return
	}
	s.publishJobEventLocked("job-status", job)
	reply(w, http.StatusOK, snapshot(job))
}
