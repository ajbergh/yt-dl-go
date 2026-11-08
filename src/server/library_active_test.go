package main

import (
	"context"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestLibraryListsFinalizedFilesWhileJobStillRuns(t *testing.T) {
	release := make(chan struct{})
	var first atomic.Int32
	fake := fixtureClient(2)
	fake.streamFn = func(ctx context.Context, video *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
		if first.Add(1) > 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			}
		}
		return io.NopCloser(strings.NewReader(fixtureData)), int64(len(fixtureData)), nil
	}
	s := testServer(t, fake, nil)
	job := createJob(t, s, testPlaylist)
	deadline := time.Now().Add(10 * time.Second)
	for {
		response := request(s, "GET", "/api/library", "", nil)
		if strings.Contains(response.Body.String(), job.ID) {
			break
		}
		if time.Now().After(deadline) {
			var items, sources int
			_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items`).Scan(&items)
			_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM library_sources`).Scan(&sources)
			t.Fatalf("finalized item of a running job never reached the Library: %s items=%d sources=%d status=%s", response.Body.String(), items, sources, request(s, "GET", "/api/jobs/"+job.ID, "", nil).Body.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if request(s, "DELETE", "/api/jobs/"+job.ID+"/all", "", nil).Code != 409 {
		t.Fatal("files of a running job were deletable from the Library")
	}
	close(release)
	done := waitTerminal(t, s, job.ID)
	for _, file := range done.Files {
		if path := trackedOutputPath(t, s, done.ID, file.ID); path != "" {
			_ = os.Remove(path)
		}
	}
}
