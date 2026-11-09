package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLowDiskSpaceThreshold(t *testing.T) {
	for _, tc := range []struct {
		space diskSpace
		want  bool
	}{
		{diskSpace{}, false}, {diskSpace{Available: 0, Total: 100}, true},
		{diskSpace{Available: 4, Total: 100}, true}, {diskSpace{Available: 5, Total: 100}, false},
		{diskSpace{Available: 6, Total: 100}, false}, {diskSpace{Available: 5, Total: 101}, true},
		{diskSpace{Available: math.MaxUint64 / 20, Total: math.MaxUint64}, true},
		{diskSpace{Available: math.MaxUint64, Total: math.MaxUint64}, false},
	} {
		if got := lowDiskSpace(tc.space); got != tc.want {
			t.Errorf("lowDiskSpace(%+v) = %v, want %v", tc.space, got, tc.want)
		}
	}
}

func TestDiskSpaceForNewDestination(t *testing.T) {
	root := t.TempDir()
	space, err := diskSpaceForTarget(filepath.Join(root, "new", "library"))
	if err != nil || space.Total == 0 || space.Available > space.Total {
		t.Fatalf("native disk stats = %+v, %v", space, err)
	}
}

func TestStorageWarningUsesCapturedDestinationAndClears(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	captured := filepath.Join(t.TempDir(), "original-destination")
	s.mu.Lock()
	s.jobs["paused"] = &jobState{Job: Job{ID: "paused", Status: "paused", DownloadLocation: captured}}
	s.jobs["finished"] = &jobState{Job: Job{ID: "finished", Status: "completed", DownloadLocation: filepath.Join(t.TempDir(), "old")}}
	s.mu.Unlock()
	low := true
	s.diskSpaceReader = func(path string) (diskSpace, error) {
		if path == captured && low {
			return diskSpace{Available: 4, Total: 100}, nil
		}
		return diskSpace{Available: 5, Total: 100}, nil
	}
	read := func() []storageWarning {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleStorage(w, httptest.NewRequest(http.MethodGet, "/api/storage", nil))
		var result struct {
			Warnings []storageWarning `json:"warnings"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("storage response: %d %s", w.Code, w.Body.String())
		}
		return result.Warnings
	}
	warnings := read()
	if len(warnings) != 1 || warnings[0].Path != captured || warnings[0].FreePercent != 4 {
		t.Fatalf("warnings = %+v", warnings)
	}
	low = false
	if warnings := read(); len(warnings) != 0 {
		t.Fatalf("recovered warnings = %+v", warnings)
	}
	s.diskSpaceReader = func(string) (diskSpace, error) { return diskSpace{}, errors.New("unavailable") }
	if warnings := read(); len(warnings) != 0 {
		t.Fatalf("unknown stats warned: %+v", warnings)
	}
	s.mu.Lock()
	delete(s.jobs, "paused")
	delete(s.jobs, "finished")
	s.mu.Unlock()
}

func TestLegacyCapIgnoredAndLowSpaceDoesNotStopPlaylist(t *testing.T) {
	t.Setenv("MAX_JOB_BYTES", "1")
	s := testServer(t, fixtureClient(3), nil)
	if _, err := s.store.db.Exec(`UPDATE app_settings SET max_job_bytes=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	saved, err := s.store.loadAppSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.settings = saved
	s.mu.Unlock()
	settingsJSON, err := json.Marshal(saved)
	if err != nil || strings.Contains(string(settingsJSON), "maxJobBytes") {
		t.Fatalf("retired setting exposed: %s (%v)", settingsJSON, err)
	}
	s.diskSpaceReader = func(string) (diskSpace, error) { return diskSpace{Available: 1, Total: 100}, nil }
	w := httptest.NewRecorder()
	s.handleStorage(w, httptest.NewRequest(http.MethodGet, "/api/storage", nil))
	if !strings.Contains(w.Body.String(), "freePercent") {
		t.Fatalf("missing warning: %s", w.Body.String())
	}
	j := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	if j.Status != "completed" || j.CompletedCount != 3 || j.Error != "" {
		t.Fatalf("advisory space/cap stopped playlist: %+v", j)
	}
	assertFinalFiles(t, s, j)
	// All three fixture entries share a title; publication must preserve each.
	paths := map[string]bool{}
	loaded, err := s.store.loadJob(s.cfg.root, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range loaded.job.Files {
		if paths[file.OutputPath] {
			t.Fatal("duplicate publication path")
		}
		paths[file.OutputPath] = true
		if _, err := os.Stat(file.OutputPath); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetiredStorageEnvironmentIgnored(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:8080")
	t.Setenv("MAX_JOB_BYTES", "no-longer-a-setting")
	if _, err := loadConfig(); err != nil {
		t.Fatalf("retired storage limit affected startup: %v", err)
	}
}
