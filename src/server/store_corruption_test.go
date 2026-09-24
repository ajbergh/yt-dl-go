package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readQuarantinedValue(t *testing.T, s *server, table, column, key string) []byte {
	t.Helper()
	var raw []byte
	err := s.store.db.QueryRow(`SELECT raw_value FROM corrupt_records WHERE source_table=? AND source_column=? AND record_key=? ORDER BY id DESC LIMIT 1`, table, column, key).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMigrateV21QuarantinesMalformedLegacyQueue(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.store.queueItemsReady = false
	job := &jobState{Job: Job{
		ID: "job-legacy-corrupt-queue", URL: testVideo, Kind: "video", Quality: "best", Status: "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Items:     []queueItem{{Index: 1, VideoID: "legacy-item", Status: "queued"}},
	}, dir: filepath.Join(s.cfg.root, "job-legacy-corrupt-queue")}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	const malformed = `[{"videoId":"legacy-secret"`
	if _, err := s.store.db.Exec(`UPDATE jobs SET queue_items=? WHERE id=?`, malformed, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DROP TABLE queue_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DELETE FROM schema_migrations WHERE version >= 21`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.store.snapshotBeforeMigration(s.cfg.root, 20, 21); err != nil {
		t.Fatalf("snapshot before legacy queue migration: %v", err)
	}
	if err := s.store.migrateV21(); err != nil {
		t.Fatalf("migrate malformed legacy queue: %v", err)
	}
	var version int
	if err := s.store.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 21 {
		t.Fatalf("schema version after legacy migration = %d, want 21", version)
	}
	var rawQueue string
	if err := s.store.db.QueryRow(`SELECT queue_items FROM jobs WHERE id=?`, job.ID).Scan(&rawQueue); err != nil {
		t.Fatal(err)
	}
	if rawQueue != "[]" {
		t.Fatalf("repaired legacy queue = %q, want []", rawQueue)
	}
	var savedJobs int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=?`, job.ID).Scan(&savedJobs); err != nil {
		t.Fatal(err)
	}
	if savedJobs != 1 {
		t.Fatalf("preserved job row count = %d, want 1", savedJobs)
	}
	if got := readQuarantinedValue(t, s, "jobs", "queue_items", job.ID); !bytes.Equal(got, []byte(malformed)) {
		t.Fatalf("quarantined legacy JSON = %q, want original %q", got, malformed)
	}
	backups := migrationSnapshots(t, s.cfg.root)
	foundPreMigration := false
	for _, path := range backups {
		if strings.Contains(filepath.Base(path), "v21-from-v20-") {
			foundPreMigration = true
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var backedUp string
			err = db.QueryRow(`SELECT queue_items FROM jobs WHERE id=?`, job.ID).Scan(&backedUp)
			_ = db.Close()
			if err != nil {
				t.Fatal(err)
			}
			if backedUp != malformed {
				t.Fatalf("pre-v21 snapshot queue = %q, want original malformed value", backedUp)
			}
		}
	}
	if !foundPreMigration {
		t.Fatalf("missing pre-v21 snapshot among %v", backups)
	}
}

func TestLibraryBackfillQuarantinesMalformedLegacyQueueBeforeV21(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.store.queueItemsReady = false
	job := &jobState{Job: Job{
		ID: "job-v20-corrupt-queue", URL: testVideo, Kind: "video", Quality: "best", Status: "completed",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files:     []mediaFile{{ID: "v20-primary", Name: "primary.mp4", Size: 3, MimeType: "video/mp4", SourceItemIndex: 1}},
		Items:     []queueItem{{Index: 1, VideoID: "legacy-corrupt-item", Status: "queued"}},
	}, dir: filepath.Join(s.cfg.root, "job-v20-corrupt-queue"), done: time.Now()}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	const malformed = `[{"videoId":"v20-secret"`
	if _, err := s.store.db.Exec(`UPDATE jobs SET queue_items=? WHERE id=?`, malformed, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DROP TABLE library_items`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`DELETE FROM schema_migrations WHERE version >= 20`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.snapshotBeforeMigration(s.cfg.root, 19, 20); err != nil {
		t.Fatal(err)
	}
	s.store.migrationBackup = true
	err := s.store.migrateV20(s.cfg.root)
	s.store.migrationBackup = false
	if err != nil {
		t.Fatalf("v20 Library backfill with malformed legacy queue: %v", err)
	}
	if got := readQuarantinedValue(t, s, "jobs", "queue_items", job.ID); !bytes.Equal(got, []byte(malformed)) {
		t.Fatalf("v20 backfill quarantine = %q, want original malformed payload", got)
	}
	var repairedQueue string
	if err := s.store.db.QueryRow(`SELECT queue_items FROM jobs WHERE id=?`, job.ID).Scan(&repairedQueue); err != nil {
		t.Fatal(err)
	}
	if repairedQueue != "[]" {
		t.Fatalf("legacy queue after v20 backfill = %q, want []", repairedQueue)
	}
	var libraryRows int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE file_id='v20-primary'`).Scan(&libraryRows); err != nil {
		t.Fatal(err)
	}
	if libraryRows != 1 {
		t.Fatalf("Library rows backfilled despite malformed queue = %d, want 1", libraryRows)
	}
}

func TestSavedJSONCorruptionIsQuarantinedAndRepaired(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "job-corrupt-queue-file-ids", URL: testVideo, Kind: "video", Quality: "best", Status: "queued",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Items:     []queueItem{{Index: 1, VideoID: "queued-item", Status: "queued", FileIDs: []string{"primary"}}},
	}, dir: filepath.Join(s.cfg.root, "job-corrupt-queue-file-ids")}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	const badFileIDs = `{"private":"queue-secret"`
	if _, err := s.store.db.Exec(`UPDATE queue_items SET file_ids_json=? WHERE job_id=?`, badFileIDs, job.ID); err != nil {
		t.Fatal(err)
	}
	const badCategories = `["private-category-secret"`
	if _, err := s.store.db.Exec(`UPDATE app_settings SET user_categories=? WHERE id=1`, badCategories); err != nil {
		t.Fatal(err)
	}

	settings, err := s.store.loadAppSettings()
	if err != nil {
		t.Fatalf("load preferences after quarantine: %v", err)
	}
	if len(settings.UserCategories) != 0 {
		t.Fatalf("repaired user categories = %v, want empty list", settings.UserCategories)
	}
	items, err := s.store.loadQueueItemsQuery(`WHERE job_id=?`, []any{job.ID})
	if err != nil {
		t.Fatalf("load queue after quarantine: %v", err)
	}
	if len(items[job.ID]) != 1 || items[job.ID][0].VideoID != "queued-item" || len(items[job.ID][0].FileIDs) != 0 {
		t.Fatalf("queue item was not preserved with only corrupt IDs cleared: %+v", items[job.ID])
	}
	var storedIDs, storedCategories string
	if err := s.store.db.QueryRow(`SELECT file_ids_json FROM queue_items WHERE job_id=?`, job.ID).Scan(&storedIDs); err != nil {
		t.Fatal(err)
	}
	if err := s.store.db.QueryRow(`SELECT user_categories FROM app_settings WHERE id=1`).Scan(&storedCategories); err != nil {
		t.Fatal(err)
	}
	if storedIDs != "[]" || storedCategories != "[]" {
		t.Fatalf("repaired JSON values: file_ids=%q categories=%q", storedIDs, storedCategories)
	}
	if got := readQuarantinedValue(t, s, "queue_items", "file_ids_json", job.ID+"/item:1"); !bytes.Equal(got, []byte(badFileIDs)) {
		t.Fatalf("quarantined queue IDs = %q, want original %q", got, badFileIDs)
	}
	if got := readQuarantinedValue(t, s, "app_settings", "user_categories", "1"); !bytes.Equal(got, []byte(badCategories)) {
		t.Fatalf("quarantined categories = %q, want original %q", got, badCategories)
	}
	diagnostics, err := s.store.loadCorruptionDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Count != 2 || len(diagnostics.Issues) != 2 {
		t.Fatalf("diagnostics = count %d, issues %d; want 2 each", diagnostics.Count, len(diagnostics.Issues))
	}
	backups := migrationSnapshots(t, s.cfg.root)
	if len(backups) != 2 {
		t.Fatalf("fresh-install plus pre-repair snapshot count = %d, want 2", len(backups))
	}
}

func TestJobSidecarJSONCorruptionPreservesPrimaryFile(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "job-corrupt-sidecars", URL: testVideo, Kind: "video", Quality: "best", Status: "completed",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files:     []mediaFile{{ID: "primary-file", Name: "primary.mp4", Size: 3, MimeType: "video/mp4", SourceItemIndex: 1}},
	}, dir: filepath.Join(s.cfg.root, "job-corrupt-sidecars"), done: time.Now()}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"subtitle_json", "chapters_json", "additional_files_json"} {
		if _, err := s.store.db.Exec(`UPDATE job_files SET `+column+`=? WHERE job_id=? AND item_index=1`, `{"secret":`, job.ID); err != nil {
			t.Fatalf("corrupt %s fixture: %v", column, err)
		}
	}

	loaded, err := s.store.loadJob(s.cfg.root, job.ID)
	if err != nil {
		t.Fatalf("load job after sidecar quarantine: %v", err)
	}
	if loaded == nil || len(loaded.job.Files) != 1 || loaded.job.Files[0].ID != "primary-file" {
		t.Fatalf("primary file was not preserved: %+v", loaded)
	}
	if loaded.job.Files[0].Subtitle != nil || len(loaded.job.Files[0].Chapters) != 0 || len(loaded.job.Files) != 1 {
		t.Fatalf("malformed optional sidecars were not omitted: %+v", loaded.job.Files[0])
	}
	diagnostics, err := s.store.loadCorruptionDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Count != 3 {
		t.Fatalf("sidecar diagnostics count = %d, want 3", diagnostics.Count)
	}
	for _, column := range []string{"subtitle_json", "chapters_json", "additional_files_json"} {
		if got := readQuarantinedValue(t, s, "job_files", column, job.ID+"/1"); !bytes.Equal(got, []byte(`{"secret":`)) {
			t.Errorf("quarantined %s = %q, want original malformed payload", column, got)
		}
	}
}

func TestMalformedLibraryFileIsQuarantinedAndHidden(t *testing.T) {
	s := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "job-corrupt-library-item", URL: testVideo, Kind: "video", Quality: "best", Status: "completed",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files:     []mediaFile{{ID: "corrupt-library-file", Name: "library.mp4", Size: 3, MimeType: "video/mp4", SourceItemIndex: 1}},
	}, dir: filepath.Join(s.cfg.root, "job-corrupt-library-item"), done: time.Now()}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"library_items_category", "library_items_channel", "library_items_effective_category", "library_items_effective_channel"} {
		if _, err := s.store.db.Exec(`DROP INDEX ` + index); err != nil {
			t.Fatalf("drop JSON index %s for malformed-row fixture: %v", index, err)
		}
	}
	const malformed = `{"private":"library-secret"`
	if _, err := s.store.db.Exec(`UPDATE library_items SET file_json=? WHERE file_id=?`, malformed, "corrupt-library-file"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil {
		t.Fatalf("load Library after quarantine: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("Library returned %d corrupted entry jobs, want 0", len(jobs))
	}
	var rowCount int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE file_id=?`, "corrupt-library-file").Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 0 {
		t.Fatalf("corrupt Library row count after quarantine = %d, want 0", rowCount)
	}
	if got := readQuarantinedValue(t, s, "library_items", "file_json", "corrupt-library-file"); !bytes.Equal(got, []byte(malformed)) {
		t.Fatalf("quarantined Library file = %q, want original malformed payload", got)
	}
}

func TestDiagnosticsEndpointIsAuthenticatedAndOmitsRawPayload(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.cfg.token = "diagnostics-token"
	if _, err := s.store.db.Exec(`INSERT INTO corrupt_records(detected_at,source_table,source_column,record_key,error,action,raw_value) VALUES(?,?,?,?,?,?,?)`,
		time.Now().UnixNano(), "queue_items", "file_ids_json", "job/item", "malformed JSON", "Cleared the corrupt field.", []byte("private-raw-payload")); err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	s.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/diagnostics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized diagnostics status = %d, want 401", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/diagnostics", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Authorization", "Bearer diagnostics-token")
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated diagnostics status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var diagnostics CorruptionDiagnostics
	if err := json.Unmarshal(recorder.Body.Bytes(), &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Count != 1 || len(diagnostics.Issues) != 1 || diagnostics.Issues[0].RecordKey != "job/item" {
		t.Fatalf("diagnostics response = %+v", diagnostics)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("private-raw-payload")) {
		t.Fatal("diagnostics endpoint exposed the quarantined raw payload")
	}
}
