package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

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
