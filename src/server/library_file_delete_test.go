package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteSelectedLibraryFilePreservesPlaylistSiblingsAndHistory(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	if len(job.Files) != 2 {
		t.Fatalf("fixture should contain two media files, got %d", len(job.Files))
	}
	first := job.Files[0]
	second := job.Files[1]
	firstPath := trackedOutputPath(t, s, job.ID, first.ID)
	secondPath := trackedOutputPath(t, s, job.ID, second.ID)
	if filepath.Clean(firstPath) == filepath.Clean(secondPath) {
		t.Fatal("test fixture has two entries pointing at the same output")
	}
	response := request(s, "DELETE", "/api/jobs/"+job.ID+"/library-files/"+first.ID, "", nil)
	if response.Code != 204 {
		t.Fatalf("delete selected file: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Lstat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected file still exists: %v", err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("sibling media was removed: %v", err)
	}
	if result := request(s, "GET", "/api/jobs/"+job.ID, "", nil); result.Code != 200 {
		t.Fatalf("download job history was lost: %d", result.Code)
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 || jobs[0].Files[0].ID != second.ID {
		t.Fatalf("selected file deletion affected sibling index: jobs=%+v err=%v", jobs, err)
	}
	// A subsequent persisted job save cannot resurrect the deleted media.
	s.mu.Lock()
	if active := s.jobs[job.ID]; active != nil {
		if err := s.persistJobLocked(active); err != nil {
			s.mu.Unlock()
			t.Fatal(err)
		}
	}
	s.mu.Unlock()
	jobs, err = s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 {
		t.Fatalf("job save resurrected deleted file: jobs=%+v err=%v", jobs, err)
	}
	restarted := restartLibraryServer(t, s)
	jobs, err = restarted.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 || jobs[0].Files[0].ID != second.ID {
		t.Fatalf("restarted library lost sibling or resurrected file: jobs=%+v err=%v", jobs, err)
	}
	if result := request(restarted, "GET", "/api/jobs/"+job.ID, "", nil); result.Code != 200 {
		t.Fatalf("history missing after restart: %d", result.Code)
	}
}

func TestDeleteSelectedLibraryFileRejectsModifiedOrMissingFiles(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	first, sibling := job.Files[0], job.Files[1]
	filePath := trackedOutputPath(t, s, job.ID, first.ID)
	siblingPath := trackedOutputPath(t, s, job.ID, sibling.ID)
	original, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, append(append([]byte(nil), original...), 'x'), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/jobs/" + job.ID + "/library-files/" + first.ID
	if response := request(s, "DELETE", path, "", nil); response.Code != 409 {
		t.Fatalf("changed media should block deletion, got %d: %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("altered file was deleted: %v", err)
	}
	if _, err := os.Stat(siblingPath); err != nil {
		t.Fatalf("sibling file was deleted: %v", err)
	}
	if err := os.WriteFile(filePath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if response := request(s, "DELETE", "/api/jobs/"+job.ID+"/library-files/no-such-file", "", nil); response.Code != 404 {
		t.Fatalf("unknown file should return 404, got %d", response.Code)
	}
	if response := request(s, "DELETE", path, "", map[string]string{"Authorization": "Bearer invalid"}); response.Code != 401 {
		t.Fatalf("unauthorized deletion should return 401, got %d", response.Code)
	}
	if response := request(s, "GET", path, "", nil); response.Code != 404 {
		t.Fatalf("unsupported GET should return 404, got %d", response.Code)
	}
	// Another indexed record claiming the same output must block deletion,
	// even when that record belongs to a separate source job.
	if _, err := s.store.db.Exec(`INSERT INTO library_items(file_id,source_job_id,source_item_index,file_json,output_path,created_at)
		VALUES(?,?,?,?,?,?)`, "alias-probe", "other-job", 1, `{"title":"alias"}`, filePath, 1); err != nil {
		t.Fatal(err)
	}
	if result := request(s, "DELETE", path, "", nil); result.Code != 409 {
		t.Fatalf("shared tracked output should block deletion: %d %s", result.Code, result.Body.String())
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("shared output was deleted: %v", err)
	}
	if _, err := s.store.db.Exec(`DELETE FROM library_items WHERE file_id=?`, "alias-probe"); err != nil {
		t.Fatal(err)
	}
	if result := request(s, "DELETE", path, "", nil); result.Code != 204 {
		t.Fatalf("valid deletion following rejection: %d %s", result.Code, result.Body.String())
	}
	if _, err := os.Stat(siblingPath); err != nil {
		t.Fatalf("sibling lost after selected deletion: %v", err)
	}
}

func TestDeleteSelectedLibraryFileRejectsSymlinkWithoutTouchingItsTarget(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	filePath := trackedOutputPath(t, s, job.ID, job.Files[0].ID)
	siblingPath := trackedOutputPath(t, s, job.ID, job.Files[1].ID)
	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(siblingPath, filePath); err != nil {
		t.Skipf("symlink creation is not available on this platform: %v", err)
	}
	result := request(s, "DELETE", "/api/jobs/"+job.ID+"/library-files/"+job.Files[0].ID, "", nil)
	if result.Code != 409 {
		t.Fatalf("symlinked output must be rejected: %d %s", result.Code, result.Body.String())
	}
	if info, err := os.Lstat(filePath); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was altered during rejected delete: %v %v", info, err)
	}
	if _, err := os.Stat(siblingPath); err != nil {
		t.Fatalf("symlink target/sibling was deleted: %v", err)
	}
	if strings.Contains(result.Body.String(), siblingPath) {
		t.Fatal("absolute private file path leaked in response")
	}
}
