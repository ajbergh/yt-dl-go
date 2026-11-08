package main

import (
	"context"
	"encoding/json"
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
	var revealed string
	s.filesystemOpener = func(_ context.Context, action, path string) error {
		if action != "reveal" {
			t.Errorf("unexpected filesystem action: %s", action)
		}
		revealed = path
		return nil
	}
	job := createJob(t, s, testPlaylist)
	deadline := time.Now().Add(10 * time.Second)
	var finalized mediaFile
	for {
		response := request(s, "GET", "/api/library", "", nil)
		if strings.Contains(response.Body.String(), job.ID) {
			var library struct{ Jobs []Job }
			if json.Unmarshal(response.Body.Bytes(), &library) != nil || len(library.Jobs) != 1 || len(library.Jobs[0].Files) != 1 || terminal(library.Jobs[0].Status) {
				t.Fatalf("expected one finalized file in an active playlist: %s", response.Body.String())
			}
			finalized = library.Jobs[0].Files[0]
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
	outputPath := trackedOutputPath(t, s, job.ID, finalized.ID)
	page := requestLibraryFilesPage(t, s, "/api/library/files?limit=1&sort=title&source="+job.ID)
	if page.TotalFiles != 1 || len(page.Jobs) != 1 || page.Jobs[0].Files[0].ID != finalized.ID {
		t.Fatalf("active file missing from file-level paging: %+v", page)
	}
	key := job.ID + ":" + finalized.ID
	if _, _, err := s.store.saveLibraryOrganization(libraryOrganization{Favorites: []string{key},
		Collections: []libraryCollection{{ID: "c_active", Name: "Active tracks", ItemKeys: []string{key}}}}); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"favorites=1", "collection=c_active"} {
		page := requestLibraryFilesPage(t, s, "/api/library/files?limit=1&sort=title&"+filter)
		if page.TotalFiles != 1 || len(page.Jobs) != 1 || page.Jobs[0].Files[0].ID != finalized.ID {
			t.Fatalf("active file missing from organization filter %s: %+v", filter, page)
		}
	}
	for _, suffix := range []string{"/library-items/", "/library-files/"} {
		if result := request(s, "DELETE", "/api/jobs/"+job.ID+suffix+finalized.ID, "", nil); result.Code != 409 {
			t.Fatalf("active file mutation accepted: %s %d", suffix, result.Code)
		}
	}
	response := request(s, "POST", "/api/jobs/"+job.ID+"/filesystem", `{"fileId":"`+finalized.ID+`","action":"copy-path"}`, nil)
	var copied struct{ Path string }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &copied) != nil || copied.Path != outputPath {
		t.Fatalf("copy path for active Library file: %d %s", response.Code, response.Body.String())
	}
	response = request(s, "POST", "/api/jobs/"+job.ID+"/filesystem", `{"fileId":"`+finalized.ID+`","action":"reveal"}`, nil)
	if response.Code != 200 || revealed != outputPath {
		t.Fatalf("reveal active Library file: %d %s", response.Code, response.Body.String())
	}
	previewPath := ticketPath(t, s, job.ID, `{"fileId":"`+finalized.ID+`","inline":true}`)
	response = request(s, "GET", previewPath, "", map[string]string{"Authorization": "", "Range": "bytes=0-6"})
	if response.Code != 206 || response.Body.String() != "fixture" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("preview active Library file: %d %s", response.Code, response.Body.String())
	}
	if request(s, "POST", "/api/jobs/"+job.ID+"/ticket", `{"fileId":"unfinished","inline":true}`, nil).Code != 404 {
		t.Fatal("preview accepted an unfinished file")
	}
	for _, body := range []string{`{}`, `{"fileIds":["` + finalized.ID + `"]}`} {
		if request(s, "POST", "/api/jobs/"+job.ID+"/ticket", body, nil).Code != 409 {
			t.Fatal("archive ticket accepted for a running job")
		}
	}
	if request(s, "DELETE", "/api/jobs/"+job.ID+"/all", "", nil).Code != 409 {
		t.Fatal("files of a running job were deletable from the Library")
	}
	close(release)
	done := waitTerminal(t, s, job.ID)
	response = request(s, "GET", previewPath, "", nil)
	if response.Code != 200 || response.Body.String() != fixtureData {
		t.Fatalf("preview ticket did not survive playlist completion: %d %s", response.Code, response.Body.String())
	}
	for _, file := range done.Files {
		if path := trackedOutputPath(t, s, done.ID, file.ID); path != "" {
			_ = os.Remove(path)
		}
	}
}

func TestActiveLibraryFileBackfillSurvivesRestart(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	// Simulate an older version with a paused playlist's finalized item in the
	// durable index but no library_sources row until the job becomes terminal.
	if _, err := s.store.db.Exec(`UPDATE jobs SET status='paused' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DELETE FROM library_sources WHERE source_job_id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	page := requestLibraryFilesPage(t, restarted, "/api/library/files?limit=1&sort=title")
	if page.TotalFiles != 1 || len(page.Jobs) != 1 || page.Jobs[0].Status != "paused" || page.Jobs[0].Files[0].ID != job.Files[0].ID {
		t.Fatalf("active file was not backfilled after restart: %+v", page)
	}
}
