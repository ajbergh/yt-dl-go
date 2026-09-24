package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newPersistenceTestServer(t *testing.T) *server {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	prepareTestDataDir(t, root)
	s, err := newServer(config{
		addr: "127.0.0.1:8080", root: root, token: "",
		origins: map[string]bool{}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 8, maxBytes: 1 << 20, timeout: time.Minute, retain: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.stop)
	return s
}

func TestPersistenceFailuresAreLoggedAndExposeDegradedHealth(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := &jobState{Job: Job{
		ID: "job-persist-failure", URL: testVideo, Kind: "video", Quality: "best",
		Status: "queued", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, dir: filepath.Join(s.cfg.root, "job-persist-failure")}
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`CREATE TRIGGER reject_job_updates BEFORE UPDATE ON jobs BEGIN SELECT RAISE(FAIL, 'forced persistence failure'); END`); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	for range persistenceDegradedThreshold {
		if err := s.persistJobLocked(j); err == nil {
			s.mu.Unlock()
			t.Fatal("persistJobLocked succeeded despite the rejecting trigger")
		}
	}
	s.mu.Unlock()
	if j.Status != "failed" || !j.persistenceFailed {
		t.Fatalf("job state after failed persistence = status %q, persistenceFailed %t", j.Status, j.persistenceFailed)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/health", nil)
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	var health struct {
		Degraded    bool `json:"degraded"`
		Persistence struct {
			Degraded            bool   `json:"degraded"`
			ConsecutiveFailures uint64 `json:"consecutiveFailures"`
		} `json:"persistence"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if !health.Degraded || !health.Persistence.Degraded || health.Persistence.ConsecutiveFailures != persistenceDegradedThreshold {
		t.Fatalf("health did not report degraded persistence: %+v", health)
	}

	if _, err := s.store.db.Exec(`DROP TRIGGER reject_job_updates`); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if err := s.persistJobLocked(j); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	if degraded, failures := s.persistenceDegraded(); degraded || failures != 0 {
		t.Fatalf("successful persistence did not clear consecutive failures: degraded=%t failures=%d", degraded, failures)
	}
}

func TestRuntimeSettingsPersistAndReportEffectiveSource(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.cfg.maxBytes = 10 << 30
	s.cfg.timeout = 0
	s.cfg.retain = 0
	s.cfg.browserPath = ""
	s.cfg.downloadSlots = 4
	get := httptest.NewRecorder()
	s.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/settings", nil))
	var response struct {
		Settings AppSettings       `json:"settings"`
		Sources  map[string]string `json:"sources"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &response) != nil {
		t.Fatalf("get runtime settings: %d %s", get.Code, get.Body.String())
	}
	if response.Settings.Retention != "never" || response.Settings.MaxJobBytes != 10<<30 || response.Settings.JobTimeout != "none" || response.Settings.DownloadSlots != 4 {
		t.Fatalf("runtime setting defaults = %+v", response.Settings)
	}
	for _, key := range []string{"retention", "maxJobBytes", "jobTimeout", "chromePath", "downloadSlots"} {
		if response.Sources[key] != "SQLite (active)" {
			t.Fatalf("default %s source = %q", key, response.Sources[key])
		}
	}

	body := `{"retention":"720h","maxJobBytes":123456789,"jobTimeout":"2h","chromePath":"C:\\Tools\\Chrome\\chrome.exe","downloadSlots":8}`
	put := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:8080/api/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(put, req)
	if put.Code != http.StatusOK {
		t.Fatalf("save runtime settings: %d %s", put.Code, put.Body.String())
	}
	if err := json.Unmarshal(put.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Settings.Retention != "720h0m0s" || response.Settings.MaxJobBytes != 123456789 || response.Settings.JobTimeout != "2h0m0s" || response.Settings.DownloadSlots != 8 {
		t.Fatalf("saved runtime settings = %+v", response.Settings)
	}
	for _, key := range []string{"retention", "maxJobBytes", "jobTimeout", "chromePath", "downloadSlots"} {
		if response.Sources[key] != "SQLite (restart required)" {
			t.Fatalf("edited %s source = %q, want restart required", key, response.Sources[key])
		}
	}
	if s.cfg.maxBytes != 10<<30 || s.cfg.timeout != 0 || s.cfg.retain != 0 || s.cfg.browserPath != "" || s.cfg.downloadSlots != 4 {
		t.Fatalf("runtime values changed before restart: %+v", s.cfg)
	}
	persisted, err := s.store.loadAppSettings()
	if err != nil || !reflect.DeepEqual(persisted, response.Settings) {
		t.Fatalf("runtime settings did not round-trip to SQLite: persisted=%+v response=%+v err=%v", persisted, response.Settings, err)
	}

	restartRoot := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(restartRoot, 0700); err != nil {
		t.Fatal(err)
	}
	prepareTestDataDir(t, restartRoot)
	restartStore, err := openJobStore(restartRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := restartStore.saveAppSettings(persisted); err != nil {
		_ = restartStore.close()
		t.Fatal(err)
	}
	if err := restartStore.close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newServer(config{
		addr: "127.0.0.1:8080", root: restartRoot,
		origins: map[string]bool{}, hosts: map[string]bool{"127.0.0.1:8080": true}, maxJobs: 8, runtimeSettingsFromDB: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stop()
	if restarted.cfg.maxBytes != persisted.MaxJobBytes || restarted.cfg.timeout != 2*time.Hour || restarted.cfg.retain != 720*time.Hour || restarted.cfg.browserPath != persisted.ChromePath || restarted.cfg.downloadSlots != persisted.DownloadSlots {
		t.Fatalf("saved runtime settings did not activate after restart: cfg=%+v settings=%+v", restarted.cfg, persisted)
	}

	envConfig := s.cfg
	envConfig.retainEnv, envConfig.maxBytesEnv, envConfig.timeoutEnv, envConfig.browserPathEnv, envConfig.downloadSlotsEnv = true, true, true, true, true
	sources := runtimeSettingSources(persisted, envConfig)
	for key, variable := range map[string]string{"retention": "RETENTION", "maxJobBytes": "MAX_JOB_BYTES", "jobTimeout": "JOB_TIMEOUT", "chromePath": "CHROME_PATH", "downloadSlots": "DOWNLOAD_SLOTS"} {
		if sources[key] != "Environment variable "+variable {
			t.Fatalf("%s environment source = %q", key, sources[key])
		}
	}
	for _, invalid := range []string{
		`{"retention":"3m"}`, `{"maxJobBytes":0}`, `{"jobTimeout":"500ms"}`, `{"downloadSlots":17}`,
	} {
		bad := httptest.NewRecorder()
		badRequest := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:8080/api/settings", strings.NewReader(invalid))
		badRequest.Header.Set("Content-Type", "application/json")
		s.ServeHTTP(bad, badRequest)
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("invalid runtime setting %s status = %d, body=%s", invalid, bad.Code, bad.Body.String())
		}
	}
}

func TestDownloadCheckpointMethodsReturnDatabaseErrors(t *testing.T) {
	s := newPersistenceTestServer(t)
	if err := s.store.saveJob(&jobState{Job: Job{
		ID: "job", URL: testVideo, Kind: "video", Quality: "best", Status: "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`CREATE TRIGGER reject_part_insert BEFORE INSERT ON download_parts BEGIN SELECT RAISE(FAIL, 'forced insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	checkpoint := downloadPart{Path: "part", CompletedBytes: 10, ExpectedBytes: 20, Itag: 1, SourceFingerprint: "fingerprint", Method: "native-range"}
	if err := s.store.savePart("job", 1, "video", checkpoint); err == nil {
		t.Fatal("savePart hid the database error")
	}
	if _, err := s.store.db.Exec(`DROP TRIGGER reject_part_insert`); err != nil {
		t.Fatal(err)
	}
	if err := s.store.savePart("job", 1, "video", checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`CREATE TRIGGER reject_part_delete BEFORE DELETE ON download_parts BEGIN SELECT RAISE(FAIL, 'forced delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.store.deletePart("job", 1, "video"); err == nil {
		t.Fatal("deletePart hid the database error")
	}
}

func TestFileChaptersPersistThroughSQLite(t *testing.T) {
	s := newPersistenceTestServer(t)
	chapters := []mediaChapter{{StartMs: 0, EndMs: 30_000, Title: "Opening"}, {StartMs: 30_000, EndMs: 90_000, Title: "第二章"}}
	job := &jobState{Job: Job{
		ID: "job-chapters", URL: testVideo, Kind: "video", Quality: "best", Status: "completed",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files:     []mediaFile{{ID: "file-chapters", Name: "chapters.mp4", Size: 123, MimeType: "video/mp4", Chapters: chapters}},
	}, dir: filepath.Join(s.cfg.root, "job-chapters")}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.store.loadJobs(s.cfg.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range loaded {
		if stored.job.ID == job.ID {
			if len(stored.job.Files) != 1 || !reflect.DeepEqual(stored.job.Files[0].Chapters, chapters) {
				t.Fatalf("persisted chapters = %+v", stored.job.Files)
			}
			return
		}
	}
	t.Fatal("job with chapters was not loaded from SQLite")
}

func TestLibraryItemsMirrorEveryFinalizedFileAndRemoveStaleRows(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "job-library-items", URL: testVideo, Kind: "video", Quality: "best", Status: "partial",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files: []mediaFile{
			{ID: "file-primary", Name: "primary.mp3", Size: 123, MimeType: "audio/mpeg", Title: "Primary", OutputPath: filepath.Join(s.cfg.root, "published-primary.mp3"), SourceItemIndex: 1},
			{ID: "file-chapter", Name: "chapter-2.mp3", Size: 45, MimeType: "audio/mpeg", Title: "Primary", OutputPath: filepath.Join(s.cfg.root, "published-chapter.mp3"), SourceItemIndex: 1, ChapterIndex: 2, ChapterTitle: "Second chapter"},
			{ID: "file-next-item", Name: "next.mp3", Size: 67, MimeType: "audio/mpeg", Title: "Next item", SourceItemIndex: 2},
		},
	}, dir: filepath.Join(s.cfg.root, "job-library-items"), done: time.Now()}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE source_job_id=?`, job.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("Library row count = %d, want one row for each finalized file", count)
	}
	var fileJSON, outputPath string
	if err := s.store.db.QueryRow(`SELECT file_json,output_path FROM library_items WHERE file_id='file-chapter'`).Scan(&fileJSON, &outputPath); err != nil {
		t.Fatal(err)
	}
	var chapter mediaFile
	if err := json.Unmarshal([]byte(fileJSON), &chapter); err != nil {
		t.Fatal(err)
	}
	if chapter.ChapterIndex != 2 || chapter.ChapterTitle != "Second chapter" || chapter.SourceItemIndex != 1 || outputPath != job.Files[1].OutputPath {
		t.Fatalf("persisted Library chapter metadata = %+v output path %q", chapter, outputPath)
	}

	job.Files = []mediaFile{job.Files[0], job.Files[2]}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE source_job_id=?`, job.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("Library row count after finalized-file removal = %d, want 2", count)
	}
	if err := s.store.deleteJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE source_job_id=?`, job.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("Library rows remained after job-history deletion: %d", count)
	}
}

func TestLibraryItemsMigrationBackfillsGroupedFiles(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "job-library-backfill", URL: testVideo, Kind: "video", Quality: "best", Status: "completed",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files: []mediaFile{
			{ID: "backfill-primary", Name: "primary.mp3", Size: 123, MimeType: "audio/mpeg", SourceItemIndex: 1},
			{ID: "backfill-chapter", Name: "chapter.mp3", Size: 45, MimeType: "audio/mpeg", SourceItemIndex: 1, ChapterIndex: 1, ChapterTitle: "Opening"},
		},
	}, dir: filepath.Join(s.cfg.root, "job-library-backfill"), done: time.Now()}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DROP TABLE library_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DELETE FROM schema_migrations WHERE version=20`); err != nil {
		t.Fatal(err)
	}
	if err := s.store.migrateV20(s.cfg.root); err != nil {
		t.Fatalf("backfill Library items: %v", err)
	}
	var count int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE source_job_id=?`, job.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("backfilled Library row count = %d, want both primary and chapter files", count)
	}
	var foreignKeys int
	rows, err := s.store.db.Query(`PRAGMA foreign_key_list(library_items)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		foreignKeys++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	_ = rows.Close()
	if foreignKeys != 0 {
		t.Fatalf("Library table has %d foreign keys; job deletion must not cascade implicitly", foreignKeys)
	}
}

func TestDownloadPartCheckpointsAreIndependentPerTrack(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "job-checkpoints", URL: testVideo, Kind: "video", Quality: "best", Status: "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	video := downloadPart{Path: "video.part", CompletedBytes: 10, ExpectedBytes: 100, Itag: 137, SourceFingerprint: "video-fingerprint", Method: "native-range"}
	audio := downloadPart{Path: "audio.part", CompletedBytes: 20, ExpectedBytes: 200, Itag: 140, SourceFingerprint: "audio-fingerprint", Method: "native-range"}
	if err := s.store.savePart(job.ID, 1, "video", video); err != nil {
		t.Fatal(err)
	}
	if err := s.store.savePart(job.ID, 1, "audio", audio); err != nil {
		t.Fatal(err)
	}
	gotVideo, err := s.store.loadPart(job.ID, 1, "video")
	if err != nil || gotVideo != video {
		t.Fatalf("video checkpoint = %+v, error = %v", gotVideo, err)
	}
	gotAudio, err := s.store.loadPart(job.ID, 1, "audio")
	if err != nil || gotAudio != audio {
		t.Fatalf("audio checkpoint = %+v, error = %v", gotAudio, err)
	}
	if err := s.store.deletePart(job.ID, 1, "video"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.loadPart(job.ID, 1, "audio"); err != nil {
		t.Fatalf("deleting the video checkpoint also deleted audio: %v", err)
	}
}
