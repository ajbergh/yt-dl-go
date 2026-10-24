package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kkdai/youtube/v2"
)

func bulkFixtureJob(t *testing.T, s *server, status string) *jobState {
	t.Helper()
	j := &jobState{Job: Job{
		ID: "bulk-fixture", URL: testVideo, Kind: "video", Quality: "best", MediaType: "video", VideoStrategy: "best", Status: status,
		Items: []queueItem{{Index: 1, VideoID: "abcdefghijk", Status: status}},
	}, dir: filepath.Join(s.cfg.root, "bulk-fixture")}
	if err := os.MkdirAll(j.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	s.jobs[j.ID] = j
	s.order = append(s.order, j.ID)
	return j
}

func TestBulkPauseRefreshesItemsAndEvictsRejectedHydration(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := bulkFixtureJob(t, s, "queued")
	response := request(s, "POST", "/api/jobs/bulk", `{"action":"pause","jobIds":["bulk-fixture"]}`, nil)
	var result bulkJobResponse
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Jobs) != 1 || result.Jobs[0].Status != "paused" || result.Jobs[0].Items[0].Status != "paused" {
		t.Fatalf("pause left inconsistent item state: %s", response.Body.String())
	}
	j.Status = "completed"
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.evictTerminalJobLocked(j)
	s.mu.Unlock()
	response = request(s, "POST", "/api/jobs/bulk", `{"action":"pause","jobIds":["bulk-fixture"]}`, nil)
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Failures) != 1 {
		t.Fatalf("expected rejected pause: %s", response.Body.String())
	}
	if s.jobs[j.ID] != nil {
		t.Fatal("rejected bulk action retained hydrated terminal job")
	}
}

func TestBulkCancelPreservesReaderGuard(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := bulkFixtureJob(t, s, "completed")
	j.readers = 1
	response := request(s, "POST", "/api/jobs/bulk", `{"action":"cancel","jobIds":["bulk-fixture"]}`, nil)
	if response.Code != http.StatusOK || s.jobs[j.ID] != j {
		t.Fatal("terminal cancellation evicted a job with an active reader")
	}
	response = request(s, "POST", "/api/jobs/bulk", `{"action":"delete","jobIds":["bulk-fixture"]}`, nil)
	var result bulkJobResponse
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.DeletedIDs) != 0 || len(result.Failures) != 1 {
		t.Fatalf("delete bypassed reader guard: %s", response.Body.String())
	}
	j.readers = 0
	s.mu.Lock()
	s.evictTerminalJobLocked(j)
	s.mu.Unlock()
	if s.jobs[j.ID] != nil {
		t.Fatal("job remained after reader finished")
	}
}

func TestConfigurationDiscardsResumableOutputAndPreservesFinalizedFiles(t *testing.T) {
	for _, transition := range []struct{ media, format, patch string }{
		{"audio", "mp3", `{"audioFormat":"m4a"}`},
		{"audio", "m4a", `{"audioFormat":"mp3","audioBitrate":"320k"}`},
		{"video", "", `{"quality":"720"}`},
	} {
		t.Run(transition.media+transition.format, func(t *testing.T) {
			s := newPersistenceTestServer(t)
			j := bulkFixtureJob(t, s, "paused")
			j.MediaType, j.AudioFormat = transition.media, transition.format
			if transition.media == "audio" {
				j.VideoStrategy = ""
			}
			finalPath := filepath.Join(j.dir, "finished.mp3")
			if err := os.WriteFile(finalPath, []byte("finished"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"unfinished.m4a.part", "000002-id.source.m4a"} {
				partPath := filepath.Join(j.dir, name)
				if err := os.WriteFile(partPath, []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := s.store.savePart(j.ID, 1, name, downloadPart{Path: partPath}); err != nil {
					t.Fatal(err)
				}
			}
			response := request(s, "PUT", "/api/jobs/"+j.ID+"/configuration", transition.patch, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("configuration: %s", response.Body.String())
			}
			for _, name := range []string{"unfinished.m4a.part", "000002-id.source.m4a"} {
				if _, err := os.Stat(filepath.Join(j.dir, name)); !os.IsNotExist(err) {
					t.Fatalf("unfinished file survived: %s %v", name, err)
				}
				if _, err := s.store.loadPart(j.ID, 1, name); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("checkpoint survived: %s %v", name, err)
				}
			}
			if data, err := os.ReadFile(finalPath); err != nil || string(data) != "finished" {
				t.Fatalf("finalized media changed: %s %v", data, err)
			}
		})
	}
}

func TestQueuedCancelDiscardsRestartedParts(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(fmt.Sprint(bulk), func(t *testing.T) {
			s := newPersistenceTestServer(t)
			j := bulkFixtureJob(t, s, "queued")
			partPath := filepath.Join(j.dir, "unfinished.source.m4a")
			if err := os.WriteFile(partPath, []byte("partial"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.store.savePart(j.ID, 1, "mp3-source", downloadPart{Path: partPath}); err != nil {
				t.Fatal(err)
			}
			path, body := "/api/jobs/"+j.ID+"/cancel", `{}`
			if bulk {
				path, body = "/api/jobs/bulk", `{"action":"cancel","jobIds":["bulk-fixture"]}`
			}
			response := request(s, "POST", path, body, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("cancel: %s", response.Body.String())
			}
			if _, err := os.Stat(partPath); !os.IsNotExist(err) {
				t.Fatalf("source survived cancel: %v", err)
			}
			if _, err := s.store.loadPart(j.ID, 1, "mp3-source"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("checkpoint survived cancel: %v", err)
			}
		})
	}
}

func TestBulkPersistenceFailureStopsWorkerWithoutRestoringSnapshot(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := bulkFixtureJob(t, s, "downloading")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j.cancel = cancel
	if _, err := s.store.db.Exec(`CREATE TRIGGER reject_bulk_update BEFORE UPDATE ON jobs BEGIN SELECT RAISE(FAIL, 'forced'); END`); err != nil {
		t.Fatal(err)
	}
	response := request(s, "POST", "/api/jobs/bulk", `{"action":"pause","jobIds":["bulk-fixture"]}`, nil)
	var result bulkJobResponse
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Failures) != 1 {
		t.Fatalf("failure missing: %s", response.Body.String())
	}
	if !j.persistenceFailed || j.Status != "failed" || ctx.Err() == nil {
		t.Fatal("failed write did not stop the worker safely")
	}
}

func TestBulkJobsValidatesIDsAndReportsPerJobFailures(t *testing.T) {
	s := testServer(t, fixtureClient(0), nil)
	first := request(s, http.MethodPost, "/api/jobs", `{"url":"`+testVideo+`","quality":"best","rightsConfirmed":true,"startPaused":true}`, nil)
	second := request(s, http.MethodPost, "/api/jobs", `{"url":"`+testVideo+`","quality":"best","rightsConfirmed":true,"startPaused":true}`, nil)
	var firstJob, secondJob Job
	if json.Unmarshal(first.Body.Bytes(), &firstJob) != nil || json.Unmarshal(second.Body.Bytes(), &secondJob) != nil {
		t.Fatal("could not decode startPaused jobs")
	}
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted || firstJob.Status != "paused" || secondJob.Status != "paused" {
		t.Fatalf("startPaused jobs: first=%d/%s second=%d/%s", first.Code, firstJob.Status, second.Code, secondJob.Status)
	}
	if len(firstJob.Items) != 1 || firstJob.Items[0].Status != "paused" {
		t.Fatalf("new paused job has inconsistent item status: %+v", firstJob.Items)
	}

	duplicate := request(s, http.MethodPost, "/api/jobs/bulk", `{"action":"resume","jobIds":["`+firstJob.ID+`","`+firstJob.ID+`"]}`, nil)
	if duplicate.Code != http.StatusBadRequest || firstJob.Status != "paused" {
		t.Fatalf("duplicate ID request was not rejected before mutation: %d %s", duplicate.Code, duplicate.Body.String())
	}
	response := request(s, http.MethodPost, "/api/jobs/bulk", `{"action":"resume","jobIds":["`+firstJob.ID+`","missing-job"]}`, nil)
	var result bulkJobResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("bulk resume response: %d %s", response.Code, response.Body.String())
	}
	if len(result.Jobs) != 1 || result.Jobs[0].ID != firstJob.ID || result.Jobs[0].Status != "queued" || len(result.Failures) != 1 || result.Failures[0].JobID != "missing-job" || len(result.DeletedIDs) != 0 {
		t.Fatalf("bulk response did not identify each outcome: %+v", result)
	}
	if _, err := s.store.loadJob(s.cfg.root, firstJob.ID); err != nil {
		t.Fatalf("resumed job was not persisted: %v", err)
	}
}

func TestBulkJobsRejectsOversizedIDSet(t *testing.T) {
	s := testServer(t, fixtureClient(0), nil)
	ids := make([]string, maxBulkJobIDs+1)
	for i := range ids {
		ids[i] = `"missing-` + strings.Repeat("x", i+1) + `"`
	}
	response := request(s, http.MethodPost, "/api/jobs/bulk", `{"action":"pause","jobIds":[`+strings.Join(ids, ",")+`]}`, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized bulk request status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBulkDeleteRemovesManagedJobAndPreservesPublishedFile(t *testing.T) {
	s := testServer(t, fixtureClient(0), nil)
	id := "bulk-delete-job"
	dir := filepath.Join(s.cfg.root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	managedPath := filepath.Join(dir, "managed.mp4")
	if err := os.WriteFile(managedPath, []byte("managed"), 0600); err != nil {
		t.Fatal(err)
	}
	publishedPath := filepath.Join(s.settings.DownloadLocation, "published.mp4")
	if err := os.MkdirAll(filepath.Dir(publishedPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publishedPath, []byte("published"), 0600); err != nil {
		t.Fatal(err)
	}
	job := &jobState{Job: Job{
		ID: id, URL: testVideo, Kind: "video", Quality: "best", MediaType: "video", Status: "completed",
		Files: []mediaFile{{ID: "file-id", Name: "managed.mp4", Size: int64(len("managed")), ManagedAvailable: true, PublishedAvailable: true}},
	}, dir: dir}
	s.mu.Lock()
	if err := s.store.saveJob(job); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	response := request(s, http.MethodPost, "/api/jobs/bulk", `{"action":"delete","jobIds":["`+id+`"]}`, nil)
	var result bulkJobResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("bulk delete: %d %s", response.Code, response.Body.String())
	}
	if len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != id || len(result.Failures) != 0 {
		t.Fatalf("unexpected bulk delete result: %+v", result)
	}
	if _, err := os.Stat(managedPath); !os.IsNotExist(err) {
		t.Fatalf("managed copy remains after bulk delete: %v", err)
	}
	if data, err := os.ReadFile(publishedPath); err != nil || string(data) != "published" {
		t.Fatalf("published output was not preserved: %q err=%v", data, err)
	}
}

func TestJobConfigurationCanChangeOnlyQueuedOrPausedSettings(t *testing.T) {
	s := testServer(t, fixtureClient(0), nil)
	created := request(s, http.MethodPost, "/api/jobs", `{"url":"`+testVideo+`","quality":"best","rightsConfirmed":true,"startPaused":true}`, nil)
	var job Job
	if json.Unmarshal(created.Body.Bytes(), &job) != nil || created.Code != http.StatusAccepted {
		t.Fatalf("create paused job: %d %s", created.Code, created.Body.String())
	}
	response := request(s, http.MethodPut, "/api/jobs/"+job.ID+"/configuration", `{"quality":"1080","videoStrategy":"vp9","category":"General"}`, nil)
	var updated Job
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &updated) != nil {
		t.Fatalf("configuration update: %d %s", response.Code, response.Body.String())
	}
	if updated.Quality != "1080" || updated.VideoStrategy != "vp9" || updated.Category != "General" || updated.Status != "paused" {
		t.Fatalf("unexpected configuration update: %+v", updated)
	}
	invalid := request(s, http.MethodPut, "/api/jobs/"+job.ID+"/configuration", `{"audioFormat":"m4a"}`, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("audio-only config accepted on video job: %d %s", invalid.Code, invalid.Body.String())
	}
	if persisted, err := s.store.loadJob(s.cfg.root, job.ID); err != nil || persisted.job.Quality != "1080" || persisted.job.VideoStrategy != "vp9" {
		t.Fatalf("configuration not persisted: job=%+v err=%v", persisted, err)
	}
}

func TestRetryFailedPlaylistItems(t *testing.T) {
	fake := fixtureClient(2)
	var attempts atomic.Int32
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		if attempts.Add(1) <= 2 {
			return nil, errors.New("transient fixture failure")
		}
		return fixtureVideo(id), nil
	}
	s := testServer(t, fake, nil)
	created := createJob(t, s, testPlaylist)
	initial := waitTerminal(t, s, created.ID)
	if initial.Status != "failed" || len(initial.Failures) != 2 {
		t.Fatalf("expected two failed playlist items: status=%s failures=%+v", initial.Status, initial.Failures)
	}
	response := request(s, http.MethodPost, "/api/jobs/"+initial.ID+"/retry-failed", `{}`, nil)
	var queued Job
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &queued) != nil {
		t.Fatalf("retry failed items: %d %s", response.Code, response.Body.String())
	}
	if queued.Status != "queued" || !queued.Items[0].RetryRequested || !queued.Items[1].RetryRequested || len(queued.Failures) != 0 {
		t.Fatalf("retry intents were not applied to every failed item: %+v", queued)
	}
	completed := waitTerminal(t, s, initial.ID)
	if completed.Status != "completed" || len(completed.Files) != 2 || attempts.Load() != 4 {
		t.Fatalf("retry-all did not complete: status=%s files=%d attempts=%d error=%s", completed.Status, len(completed.Files), attempts.Load(), completed.Error)
	}
}
