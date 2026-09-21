package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func persistentTestConfig(root string) config {
	return config{
		addr: "127.0.0.1:8080", root: root, token: strings.Repeat("a", 32),
		origins: map[string]bool{"http://localhost:5173": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 8, maxBytes: 1024 * 1024, timeout: 10 * time.Second, retain: 10 * time.Minute,
	}
}

func TestPlaylistSelectionPersistsInQueueItems(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := persistentTestConfig(root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = fixtureClient(4)
	body := `{"url":"` + testPlaylist + `","quality":"best","rightsConfirmed":true,"items":[{"index":2,"id":"00000000002","title":"Item 2"},{"index":4,"id":"00000000004","title":"Item 4"}]}`
	response := request(s, "POST", "/api/jobs", body, nil)
	var created Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create persisted selection: %d %s", response.Code, response.Body.String())
	}
	reorder := request(s, "PUT", "/api/jobs/"+created.ID+"/items", `{"playlistIndexes":[4,2]}`, nil)
	var reordered Job
	if reorder.Code != 200 || json.Unmarshal(reorder.Body.Bytes(), &reordered) != nil {
		t.Fatalf("reorder persisted selection: %d %s", reorder.Code, reorder.Body.String())
	}
	if reordered.Items[0].PlaylistIndex != 4 || reordered.Items[0].Index != 1 || reordered.Items[1].PlaylistIndex != 2 || reordered.Items[1].Index != 2 {
		t.Fatalf("playlist items were not reordered contiguously: %+v", reordered.Items)
	}
	s.stop()

	reloaded, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.stop()
	stored := reloaded.jobs[created.ID]
	if stored == nil || len(stored.Items) != 2 || stored.Items[0].PlaylistIndex != 4 || stored.Items[1].PlaylistIndex != 2 {
		t.Fatalf("playlist selection did not persist through queue_items JSON: %+v", stored)
	}
}

func TestPersistentQueueOrderControlsSchedulerPriority(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := persistentTestConfig(root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	fake := fixtureClient(1)
	started := make(chan string, 3)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		started <- id
		return fixtureVideo(id), nil
	}
	s.engine = fake
	s.settings.MaxConcurrentDownloads = 1
	s.settings.StorageMode = "managed-only"

	first := createJob(t, s, "https://www.youtube.com/watch?v=00000000001")
	second := createJob(t, s, "https://www.youtube.com/watch?v=00000000002")
	third := createJob(t, s, "https://www.youtube.com/watch?v=00000000003")
	response := request(s, "PUT", "/api/queue/order", marshalQueueOrder([]string{third.ID, first.ID, second.ID}), nil)
	if response.Code != 200 {
		t.Fatalf("reorder queued jobs: %d %s", response.Code, response.Body.String())
	}
	if request(s, "POST", "/api/jobs/"+first.ID+"/next", `{}`, nil).Code != 200 {
		t.Fatal("download-next action failed")
	}
	// Download-next moves first ahead of the prior explicit order.
	s.start()
	for _, id := range []string{first.ID, second.ID, third.ID} {
		waitTerminal(t, s, id)
	}
	got := []string{<-started, <-started, <-started}
	want := []string{"00000000001", "00000000003", "00000000002"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("execution order = %v, want %v", got, want)
		}
	}
	s.stop()

	reloaded, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.stop()
	if reloaded.jobs[first.ID].QueuePosition != 1 || reloaded.jobs[third.ID].QueuePosition != 2 || reloaded.jobs[second.ID].QueuePosition != 3 {
		t.Fatalf("queue order did not persist: first=%d third=%d second=%d",
			reloaded.jobs[first.ID].QueuePosition, reloaded.jobs[third.ID].QueuePosition, reloaded.jobs[second.ID].QueuePosition)
	}
}

func TestRetryItemIntentPersistsAcrossRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := persistentTestConfig(root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = fixtureClient(2)
	body := `{"url":"` + testPlaylist + `","quality":"best","rightsConfirmed":true,"items":[{"index":1,"id":"00000000001","title":"Item 1"},{"index":2,"id":"00000000002","title":"Item 2"}]}`
	response := request(s, "POST", "/api/jobs", body, nil)
	var created Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create retry fixture: %d %s", response.Code, response.Body.String())
	}

	s.mu.Lock()
	job := s.jobs[created.ID]
	job.Status = "partial"
	job.Items[0].Status = "completed"
	job.Items[1].Status = "failed"
	job.Items[1].Error = errMetadata.Error()
	job.Failures = []itemFailure{{Index: 2, Error: errMetadata.Error()}}
	if err := s.store.saveJob(job); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	retry := request(s, "POST", "/api/jobs/"+created.ID+"/retry-item", `{"index":2}`, nil)
	var queued Job
	if retry.Code != 202 || json.Unmarshal(retry.Body.Bytes(), &queued) != nil {
		t.Fatalf("queue item retry: %d %s", retry.Code, retry.Body.String())
	}
	if !queued.Items[1].RetryRequested {
		t.Fatalf("retry intent missing before restart: %+v", queued.Items)
	}
	s.stop()

	reloaded, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.stop()
	stored := reloaded.jobs[created.ID]
	if stored == nil || stored.Status != "queued" || len(stored.Items) != 2 || !stored.Items[1].RetryRequested || stored.Items[0].RetryRequested {
		t.Fatalf("single-item retry intent did not persist: %+v", stored)
	}
}

func TestPersistentHistoryAndQueueResume(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := persistentTestConfig(root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	s.engine = fixtureClient(1)
	queued := createJob(t, s, testVideo)
	if err := s.store.saveAppSettings(AppSettings{DefaultQuality: "720"}); err != nil {
		t.Fatal(err)
	}
	s.stop()

	resumed, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	resumed.engine = fixtureClient(1)
	resumed.start()
	completed := waitTerminal(t, resumed, queued.ID)
	if completed.Status != "completed" || len(completed.Files) != 1 || completed.Files[0].Title != "Fixture video" || completed.Files[0].Author != "Fixture channel" {
		t.Fatalf("queued job did not resume: %+v", completed)
	}
	resumed.stop()

	history, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer history.stop()
	if got := len(history.jobs); got != 1 {
		t.Fatalf("history count = %d, want 1", got)
	}
	if history.jobs[queued.ID].Status != "completed" {
		t.Fatalf("history status = %q", history.jobs[queued.ID].Status)
	}
	if history.settings.DefaultQuality != "720" {
		t.Fatalf("application preferences did not persist: %+v", history.settings)
	}
	var configCount int
	if err := history.store.db.QueryRow(`SELECT count(*) FROM config`).Scan(&configCount); err != nil {
		t.Fatal(err)
	}
	if configCount == 0 {
		t.Fatal("effective configuration was not stored")
	}
	var secretCount int
	if err := history.store.db.QueryRow(`SELECT count(*) FROM config WHERE key='api_token'`).Scan(&secretCount); err != nil {
		t.Fatal(err)
	}
	if secretCount != 0 {
		t.Fatal("API token must not be stored in the database")
	}
	if _, err := os.Stat(root + string(os.PathSeparator) + "state.db"); err != nil {
		t.Fatalf("state database missing: %v", err)
	}
}
