package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func newPersistenceTestServer(t *testing.T) *server {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
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
	if err := s.store.savePart("job", 1, "part", 10, 20); err == nil {
		t.Fatal("savePart hid the database error")
	}
	if _, err := s.store.db.Exec(`DROP TRIGGER reject_part_insert`); err != nil {
		t.Fatal(err)
	}
	if err := s.store.savePart("job", 1, "part", 10, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`CREATE TRIGGER reject_part_delete BEFORE DELETE ON download_parts BEGIN SELECT RAISE(FAIL, 'forced delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.store.deletePart("job", 1); err == nil {
		t.Fatal("deletePart hid the database error")
	}
}
