package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestRetryableJobsSurviveQueueRefreshAfterEviction(t *testing.T) {
	for _, status := range []string{"failed", "partial", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			s := newPersistenceTestServer(t)
			job := &jobState{Job: Job{
				ID: "retryable-" + status, URL: testVideo, Kind: "video", Quality: "best",
				Status: status, Title: "Interrupted download", Error: errRead.Error(),
				CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
				Items:     []queueItem{{Index: 1, Status: status, Error: errRead.Error()}},
			}, dir: filepath.Join(s.cfg.root, "retryable-"+status), done: time.Now()}
			if err := s.store.saveJob(job); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.jobs[job.ID], s.order = job, []string{job.ID}
			s.evictTerminalJobLocked(job)
			s.mu.Unlock()
			for range 2 {
				response := request(s, http.MethodGet, "/api/jobs", "", nil)
				var result struct {
					Jobs []Job `json:"jobs"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Jobs) != 1 {
					t.Fatalf("queue refresh lost %s job: %d %s", status, response.Code, response.Body.String())
				}
				if result.Jobs[0].ID != job.ID || result.Jobs[0].Error != errRead.Error() || len(result.Jobs[0].Items) != 1 {
					t.Fatalf("queue did not preserve failure details: %+v", result.Jobs[0])
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/events", nil).WithContext(ctx)
			writer := newSSETestWriter()
			done := make(chan struct{})
			go func() { s.ServeHTTP(writer, req); close(done) }()
			select {
			case <-writer.flushed:
			case <-time.After(5 * time.Second):
				t.Fatal("SSE snapshot did not flush")
			}
			if !strings.Contains(writer.String(), job.ID) || !strings.Contains(writer.String(), errRead.Error()) {
				t.Fatalf("SSE reconnect lost retryable job: %s", writer.String())
			}
			cancel()
			<-done
			if len(s.jobs) != 0 {
				t.Fatal("queue reads retained finished jobs in scheduler memory")
			}
			deleted := request(s, http.MethodDelete, "/api/jobs/"+job.ID+"/history", "", nil)
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("forget history: %d %s", deleted.Code, deleted.Body.String())
			}
			response := request(s, http.MethodGet, "/api/jobs", "", nil)
			if strings.Contains(response.Body.String(), job.ID) {
				t.Fatal("forgotten job reappeared in queue")
			}
		})
	}
}

// Queue snapshots are read by HTTP and persistence consumers after the
// scheduler lock has been released. Nested fields must not alias live state.
func TestQueueItemSnapshotsAreDetached(t *testing.T) {
	progress := 25.0
	j := &jobState{Job: Job{Items: []queueItem{
		{Index: 1, Status: "downloading", Progress: &progress, FileIDs: []string{"first"}},
	}}}
	got := snapshot(j)
	j.Items[0].FileIDs[0] = "changed"
	*j.Items[0].Progress = 50
	if got.Items[0].FileIDs[0] != "first" || *got.Items[0].Progress != 25 {
		t.Fatalf("snapshot retained references to mutable queue state: %+v", got.Items[0])
	}
	got.Items[0].FileIDs[0] = "response-only"
	*got.Items[0].Progress = 75
	if j.Items[0].FileIDs[0] != "changed" || *j.Items[0].Progress != 50 {
		t.Fatal("editing a snapshot mutated the original queue item")
	}
}

// Reproduce a cancellation arriving after playlist enumeration begins, but
// before run() initializes its queue items. This was the racing sequence in
// issue #92: setQueueItems previously cloned j.Items even for fresh jobs.
func TestCancelDuringPlaylistQueueInitialization(t *testing.T) {
	for i := 0; i < 50; i++ {
		t.Run(fmt.Sprintf("iteration-%02d", i), func(t *testing.T) {
			client := fixtureClient(3)
			entered := make(chan struct{})
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			client.listFn = func(context.Context) (*youtube.Playlist, error) {
				close(entered)
				<-release
				return client.playlist, nil
			}

			s := testServer(t, client, nil)
			job := createJob(t, s, testPlaylist)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("playlist enumeration did not start")
			}
			response := request(s, "POST", "/api/jobs/"+job.ID+"/cancel", "", nil)
			if response.Code != 200 {
				t.Fatalf("cancel during initialization: %d %s", response.Code, response.Body.String())
			}
			close(release)
			final := waitTerminal(t, s, job.ID)
			if final.Status != "cancelled" {
				t.Fatalf("unexpected final status: %s (%s)", final.Status, final.Error)
			}
			if len(final.Items) != 3 {
				t.Fatalf("expected three initialized items; got %+v", final.Items)
			}
			for _, item := range final.Items {
				if item.Status != "cancelled" {
					t.Fatalf("cancelled queue item has status %q: %+v", item.Status, item)
				}
			}
			waitPersistedTerminalJob(t, s, job.ID)
		})
	}
}
