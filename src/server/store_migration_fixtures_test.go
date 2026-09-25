package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoricalMigrationFixturesReachHead(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		version int
	}{
		{name: "v1", version: 1},
		{name: "v6", version: 6},
		{name: "v10", version: 10},
		{name: "v14", version: 14},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			fixturePath := filepath.Join("testdata", "migrations", fixture.name+".db")
			if err := copyFile(fixturePath, filepath.Join(root, "state.db")); err != nil {
				t.Fatal(err)
			}
			database, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			jobID := "fixture-" + fixture.name
			if _, err := database.Exec(`UPDATE jobs SET dir=? WHERE id=?`, filepath.Join(root, jobID), jobID); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := openJobStore(root)
			if err != nil {
				t.Fatalf("migrate fixture from v%d: %v", fixture.version, err)
			}
			defer func() { _ = store.close() }()

			var version int
			if err := store.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != 30 {
				t.Fatalf("fixture migrated to version %d, want 30", version)
			}
			var title, mediaType, audioFormat string
			if err := store.db.QueryRow(`SELECT title,media_type,audio_format FROM jobs WHERE id=?`, "fixture-"+fixture.name).Scan(&title, &mediaType, &audioFormat); err != nil {
				t.Fatal(err)
			}
			if title != "Historical migration fixture" {
				t.Fatalf("migrated job title = %q", title)
			}
			if fixture.version == 10 && (mediaType != "audio" || audioFormat != "mp3") {
				t.Fatalf("v10 audio defaults = %q/%q, want audio/mp3", mediaType, audioFormat)
			}
			var libraryCount int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM library_items WHERE file_id=?`, "file-"+fixture.name).Scan(&libraryCount); err != nil {
				t.Fatal(err)
			}
			if libraryCount != 1 {
				t.Fatalf("migrated Library rows = %d, want 1", libraryCount)
			}
			var configTableCount int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='config'`).Scan(&configTableCount); err != nil {
				t.Fatal(err)
			}
			if configTableCount != 0 {
				t.Fatalf("legacy config table count = %d, want 0", configTableCount)
			}
			var queueCount int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM queue_items WHERE job_id=?`, "fixture-"+fixture.name).Scan(&queueCount); err != nil {
				t.Fatal(err)
			}
			wantQueueCount := 0
			if fixture.version >= 6 {
				wantQueueCount = 1
			}
			if queueCount != wantQueueCount {
				t.Fatalf("migrated queue rows = %d, want %d", queueCount, wantQueueCount)
			}
		})
	}
}

// TestWriteHistoricalMigrationFixtures refreshes the checked-in SQLite
// snapshots when UPDATE_MIGRATION_FIXTURES=1 is set.
func TestWriteHistoricalMigrationFixtures(t *testing.T) {
	if os.Getenv("UPDATE_MIGRATION_FIXTURES") != "1" {
		t.Skip("set UPDATE_MIGRATION_FIXTURES=1 to refresh the historical SQLite snapshots")
	}
	fixtureDir := filepath.Join("testdata", "migrations")
	if err := os.MkdirAll(fixtureDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []int{1, 6, 10, 14} {
		if err := writeMigrationFixture(filepath.Join(fixtureDir, fmt.Sprintf("v%d.db", target)), target); err != nil {
			t.Fatalf("write v%d fixture: %v", target, err)
		}
	}
}

func writeMigrationFixture(path string, target int) error {
	fixtureID := fmt.Sprintf("fixture-v%d", target)
	fileID := fmt.Sprintf("file-v%d", target)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	closeDB := func() error { return db.Close() }
	for _, statement := range versionOneSchema {
		if _, err := db.Exec(statement); err != nil {
			_ = closeDB()
			return err
		}
	}
	if _, err := db.Exec(legacyConfigSchema); err != nil {
		_ = closeDB()
		return err
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		_ = closeDB()
		return err
	}
	if _, err := db.Exec(`INSERT INTO jobs(id,url,kind,quality,status,title,progress,current_item,completed_count,total_count,error,created_at,note,dir,cancel_requested,done_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		fixtureID, testVideo, "video", "best", "completed", "Historical migration fixture", nil, "", 1, 1, "", time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano), "fixture", "", 0, 0, 1); err != nil {
		_ = closeDB()
		return err
	}
	if _, err := db.Exec(`INSERT INTO job_files(job_id,item_index,file_id,name,size,height,mime_type) VALUES(?,?,?,?,?,?,?)`,
		fixtureID, 1, fileID, "fixture.mp4", 1234, 720, "video/mp4"); err != nil {
		_ = closeDB()
		return err
	}
	store := &jobStore{db: db}
	for _, migration := range transactionalMigrations {
		if migration.version > target {
			break
		}
		if migration.version == 9 && target >= 10 {
			if _, err := db.Exec(`UPDATE jobs SET media_type='audio' WHERE id=?`, fixtureID); err != nil {
				_ = closeDB()
				return err
			}
		}
		if err := store.applyMigration(migration); err != nil {
			_ = closeDB()
			return err
		}
	}
	if target >= 4 {
		queue, err := json.Marshal([]queueItem{{Index: 1, VideoID: "fixture-video", Title: "Fixture queue item", Status: "completed", FileID: fileID}})
		if err != nil {
			_ = closeDB()
			return err
		}
		if _, err := db.Exec(`UPDATE jobs SET queue_items=? WHERE id=?`, string(queue), fixtureID); err != nil {
			_ = closeDB()
			return err
		}
	}
	if err := closeDB(); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0600)
}
