package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIncompleteJobDatabase(t *testing.T, root string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`,
		`INSERT INTO schema_migrations(version) VALUES(19)`,
		`CREATE TABLE jobs(id TEXT PRIMARY KEY, dir TEXT NOT NULL)`,
		`INSERT INTO jobs VALUES('job1','legacy-job')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenJobStoreRejectsIncompleteSchemaWithoutMigrating(t *testing.T) {
	root := t.TempDir()
	writeIncompleteJobDatabase(t, root)
	store, err := openJobStore(root)
	if err == nil {
		_ = store.close()
		t.Fatal("accepted an incomplete jobs schema")
	}
	for _, want := range []string{"incompatible state database", "created_at", filepath.Join(root, "state.db"), "restore a known-good backup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var tables, version, jobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id='job1' AND dir='legacy-job'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if tables != 2 || version != 19 || jobs != 1 {
		t.Fatalf("rejection changed existing data: tables=%d version=%d jobs=%d", tables, version, jobs)
	}
	if _, err := os.Stat(filepath.Join(root, "backups")); !os.IsNotExist(err) {
		t.Fatalf("unexpected migration backup before schema rejection: %v", err)
	}
}

func TestLegacyMigrationRejectsIncompleteSchemaBeforeInstalling(t *testing.T) {
	legacy := t.TempDir()
	writeIncompleteJobDatabase(t, legacy)
	target := filepath.Join(t.TempDir(), "destination")
	err := migrateLegacyData(legacy, target)
	if err == nil || !strings.Contains(err.Error(), "incompatible state database") {
		t.Fatalf("migration error = %v, want incompatible schema diagnostic", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("invalid legacy data was installed: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(legacy, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var jobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id='job1'`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("source data was not preserved: count=%d err=%v", jobs, err)
	}
}
