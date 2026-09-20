package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func persistentTestConfig(root string) config {
	return config{
		addr: "127.0.0.1:8080", root: root, token: strings.Repeat("a", 32),
		origins: map[string]bool{"http://localhost:5173": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 8, maxBytes: 1024 * 1024, timeout: 10 * time.Second, retain: 10 * time.Minute,
	}
}

func TestPersistentHistoryAndQueueResume(t *testing.T) {
	root := t.TempDir()
	c := persistentTestConfig(root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	s.settings.DownloadLocation = filepath.Join(root, "published")
	if err := s.store.saveAppSettings(s.settings); err != nil {
		s.stop()
		t.Fatal(err)
	}
	s.engine = fixtureClient(1)
	queued := createJob(t, s, testVideo)
	settings := s.settings
	settings.DefaultQuality = "720"
	if err := s.store.saveAppSettings(settings); err != nil {
		t.Fatal(err)
	}
	s.settings = settings
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
