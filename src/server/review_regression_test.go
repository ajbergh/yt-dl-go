package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryOnlyDeletionAllowsNextFile(t *testing.T) {
	for _, route := range []string{"library-items", "library-files"} {
		t.Run(route, func(t *testing.T) {
			s := testServer(t, fixtureClient(2), nil)
			job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
			waitPersistedTerminalJob(t, s, job.ID)
			if response := request(s, "DELETE", "/api/jobs/"+job.ID+"/history", "", nil); response.Code != 204 {
				t.Fatalf("forget history: %d %s", response.Code, response.Body.String())
			}
			for _, file := range job.Files {
				response := request(s, "DELETE", "/api/jobs/"+job.ID+"/"+route+"/"+file.ID, "", nil)
				if response.Code != 204 {
					t.Fatalf("delete %s: %d %s", file.ID, response.Code, response.Body.String())
				}
			}
		})
	}
}

func TestHistoricalMigrationWithInterruptedJobs(t *testing.T) {
	for _, status := range []string{"queued", "downloading", "processing", "paused"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "state.db")
			if err := copyFile(filepath.Join("testdata", "migrations", "v14.db"), path); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`UPDATE jobs SET status=?,dir=?`, status, filepath.Join(root, "fixture-v14"))
			_ = db.Close()
			if err != nil {
				t.Fatal(err)
			}
			store, err := openJobStore(root)
			if err != nil {
				t.Fatalf("upgrade interrupted %s job: %v", status, err)
			}
			defer store.close()
		})
	}
}

func TestInterruptedPlaylistMissingFileReconcilesOnFirstAttempt(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	if err := os.Remove(trackedOutputPath(t, s, job.ID, job.Files[0].ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`UPDATE jobs SET status='downloading' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileLibrary(); err != nil {
		t.Fatalf("first reconciliation: %v", err)
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 2 || jobs[0].Files[0].PublishedAvailable || !jobs[0].Files[1].PublishedAvailable {
		t.Fatalf("lost sibling: %+v %v", jobs, err)
	}
}

func TestSuccessfulItemClearsPriorFailures(t *testing.T) {
	j := &jobState{Job: Job{Failures: []itemFailure{{Index: 1, Error: "old"}, {Index: 2, Error: "keep"}, {Index: 1, Error: "duplicate"}}}}
	setItemFiles(j, 1, []mediaFile{{ID: "success"}})
	if len(j.Failures) != 1 || j.Failures[0].Index != 2 {
		t.Fatalf("failures after success: %+v", j.Failures)
	}
}
