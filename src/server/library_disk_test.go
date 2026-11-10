package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kkdai/youtube/v2"
)

func restartLibraryServer(t *testing.T, s *server) *server {
	t.Helper()
	c := s.cfg
	s.stop()
	s.stop = func() {}
	restarted, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.stop)
	return restarted
}

func TestStartupReconcilesDeletedFilesAndPreservesSurvivors(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	missing := trackedOutputPath(t, s, job.ID, job.Files[0].ID)
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	// Reconciliation must use the historical folder, even after a setting change.
	s.settings.DownloadLocation = filepath.Join(s.cfg.root, "new-library")
	if err := s.store.saveAppSettings(s.settings); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	jobs, err := restarted.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 2 || jobs[0].Files[0].PublishedAvailable || !jobs[0].Files[1].PublishedAvailable {
		t.Fatalf("reconciled Library = %+v, err=%v", jobs, err)
	}
	loaded, err := restarted.store.loadJob(restarted.cfg.root, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	j := &jobState{Job: loaded.job, dir: loaded.dir, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}
	if err := restarted.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	jobs, err = restarted.store.loadLibraryJobs()
	if err != nil || len(jobs[0].Files) != 2 {
		t.Fatal("history save removed retained metadata")
	}
	path := ticketPath(t, restarted, job.ID, `{"fileId":"`+job.Files[1].ID+`","inline":true}`)
	response := request(restarted, "GET", path, "", map[string]string{"Range": "bytes=0-6"})
	if response.Code != 206 || response.Body.String() != "fixture" {
		t.Fatalf("folder preview failed: %d %s", response.Code, response.Body.String())
	}
}

func TestStartupPreservesEmptyLibraryAndImports(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	if err := os.Remove(trackedOutputPath(t, s, job.ID, job.Files[0].ID)); err != nil {
		t.Fatal(err)
	}
	manifest, records := sampleLibraryImport()
	if _, err := s.store.importLibraryRecords(manifest, records); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	jobs, err := restarted.store.loadLibraryJobs()
	if err != nil || len(jobs) != 2 || jobs[0].ID != job.ID || len(jobs[0].Files) != 1 || jobs[0].Files[0].PublishedAvailable {
		t.Fatalf("Library after restart: %+v %v", jobs, err)
	}
}

func TestStartupReconcilesMissingCaption(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	s.captionFetcher = func(context.Context, youtube.CaptionTrack, string) ([]byte, error) {
		return []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nCaption\n"), nil
	}
	response := request(s, "POST", "/api/jobs", `{"url":"`+testVideo+`","quality":"best","rightsConfirmed":true,"subtitleLanguage":"en","subtitleFormat":"vtt"}`, nil)
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	var created Job
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	job := waitTerminal(t, s, created.ID)
	waitPersistedTerminalJob(t, s, job.ID)
	loaded, err := s.store.loadJob(s.cfg.root, job.ID)
	if err != nil || loaded.job.Files[0].Subtitle == nil {
		t.Fatalf("caption fixture: %v", err)
	}
	if err := os.Remove(loaded.job.Files[0].Subtitle.OutputPath); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	jobs, err := restarted.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Files[0].Subtitle != nil {
		t.Fatalf("caption reconciliation: %+v %v", jobs, err)
	}
}

func TestStartupMigratesLegacyCopies(t *testing.T) {
	for _, mode := range []string{"managed-only", "managed-published", "published-only"} {
		t.Run(mode, func(t *testing.T) {
			s := testServer(t, fixtureClient(1), nil)
			job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
			waitPersistedTerminalJob(t, s, job.ID)
			loaded, err := s.store.loadJob(s.cfg.root, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			file := loaded.job.Files[0]
			legacyDir := filepath.Join(s.cfg.root, job.ID)
			if err := os.MkdirAll(legacyDir, 0700); err != nil {
				t.Fatal(err)
			}
			if mode != "published-only" {
				data, err := os.ReadFile(file.OutputPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(legacyDir, file.Name), data, 0600); err != nil {
					t.Fatal(err)
				}
				file.ManagedAvailable = true
			}
			if mode == "managed-only" {
				if err := os.Remove(file.OutputPath); err != nil {
					t.Fatal(err)
				}
				file.OutputPath, file.OutputName, file.OutputRelativePath, file.PublishedAvailable = "", "", "", false
			}
			j := &jobState{Job: loaded.job, dir: legacyDir, done: loaded.done, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}
			j.StorageMode = mode
			j.Files[0] = file
			syncStoredFile(j, file)
			if err := s.store.saveJob(j); err != nil {
				t.Fatal(err)
			}
			restarted := restartLibraryServer(t, s)
			jobs, err := restarted.store.loadLibraryJobs()
			if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 {
				t.Fatalf("migrated Library: %+v %v", jobs, err)
			}
			file = jobs[0].Files[0]
			if file.ManagedAvailable || !file.PublishedAvailable || !strings.HasPrefix(file.OutputPath, s.settings.DownloadLocation) {
				t.Fatalf("migration: %+v", file)
			}
			if _, err := os.Stat(filepath.Join(legacyDir, file.Name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("legacy duplicate retained")
			}
			path := ticketPath(t, restarted, job.ID, `{"fileId":"`+file.ID+`","inline":true}`)
			if response := request(restarted, "GET", path, "", nil); response.Code != 200 || response.Body.String() != fixtureData {
				t.Fatalf("migrated preview: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestStartupPreservesUnavailableLibraryRoot(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	location := s.settings.DownloadLocation
	detached := location + "-detached"
	if err := os.Rename(location, detached); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	jobs, err := restarted.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Files[0].ID != job.Files[0].ID {
		t.Fatalf("unavailable drive lost metadata: %+v %v", jobs, err)
	}
	if err := os.Rename(detached, location); err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcileLibrary(); err != nil {
		t.Fatal(err)
	}
	path := ticketPath(t, restarted, job.ID, `{"fileId":"`+job.Files[0].ID+`","inline":true}`)
	if response := request(restarted, "GET", path, "", nil); response.Code != 200 {
		t.Fatalf("reconnected library playback: %d %s", response.Code, response.Body.String())
	}
}

// Turn a completed fixture into a pre-upgrade managed-only record.
func legacyLibraryFixture(t *testing.T, s *server) *jobState {
	t.Helper()
	job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	loaded, err := s.store.loadJob(s.cfg.root, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	j := &jobState{Job: loaded.job, dir: filepath.Join(s.cfg.root, job.ID), done: loaded.done, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}
	if err := os.MkdirAll(j.dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := j.Files[0]
	if err := os.Rename(file.OutputPath, filepath.Join(j.dir, file.Name)); err != nil {
		t.Fatal(err)
	}
	file.OutputPath, file.OutputName, file.OutputRelativePath = "", "", ""
	file.ManagedAvailable, file.PublishedAvailable = true, false
	j.Files[0] = file
	syncStoredFile(j, file)
	j.StorageMode = "managed-only"
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestLegacyMigrationMissingCaptionDoesNotBlockOrDuplicate(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	j := legacyLibraryFixture(t, s)
	file := j.Files[0]
	file.Subtitle = &subtitleFile{Name: "missing.en.vtt", LanguageCode: "en", Format: "vtt", Size: 10, ManagedAvailable: true}
	j.Files[0] = file
	syncStoredFile(j, file)
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	for i := 0; i < 2; i++ {
		if err := restarted.reconcileLibrary(); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := restarted.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Files[0].Subtitle != nil || jobs[0].Files[0].SubtitleError == "" {
		t.Fatalf("migration: %+v %v", jobs, err)
	}
	entries, err := filepath.Glob(filepath.Join(filepath.Dir(jobs[0].Files[0].OutputPath), "*.mp4"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("migration duplicated media: %v %v", entries, err)
	}
}

func TestLegacyMigrationRollsBackOutputsOnSaveFailure(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	j := legacyLibraryFixture(t, s)
	if _, err := s.store.db.Exec(`CREATE TRIGGER reject_migration BEFORE UPDATE ON jobs BEGIN SELECT RAISE(FAIL, 'forced migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileLibrary(); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(j.dir, j.Files[0].Name)); err != nil {
		t.Fatal("migration lost original", err)
	}
	var outputs []string
	if err := filepath.WalkDir(s.settings.DownloadLocation, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".mp4" {
			outputs = append(outputs, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 0 {
		t.Fatalf("failed migration retained outputs: %v", outputs)
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || jobs[0].Files[0].OutputPath != "" {
		t.Fatalf("failed migration saved a partial path: %+v %v", jobs, err)
	}
	if _, err := s.store.db.Exec(`DROP TRIGGER reject_migration`); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileLibrary(); err != nil {
		t.Fatal(err)
	}
}

func TestRestartKeepsFinalizedFolderFilesWhenResuming(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	job := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	waitPersistedTerminalJob(t, s, job.ID)
	loaded, err := s.store.loadJob(s.cfg.root, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	j := &jobState{Job: loaded.job, dir: loaded.dir, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}
	j.Status = "queued"
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	restarted := restartLibraryServer(t, s)
	var transfers atomic.Int32
	engine := fixtureClient(2)
	engine.streamFn = func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error) {
		transfers.Add(1)
		return nil, 0, errors.New("completed files must not be downloaded again")
	}
	restarted.engine = engine
	disableTestThumbnailCapture(restarted)
	restarted.start()
	completed := waitTerminal(t, restarted, job.ID)
	if completed.Status != "completed" || len(completed.Files) != 2 || transfers.Load() != 0 {
		t.Fatalf("resume repeated finalized downloads: %+v transfers=%d", completed, transfers.Load())
	}
}
