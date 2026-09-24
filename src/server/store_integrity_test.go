package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenJobStoreAppliesPragmasWhenConnectionsAreRecycled(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data with spaces")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	store.db.SetMaxIdleConns(0)

	var busyTimeout, foreignKeys int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	var journalMode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 || foreignKeys != 1 || !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("recycled connection pragmas = busy_timeout:%d foreign_keys:%d journal_mode:%q", busyTimeout, foreignKeys, journalMode)
	}
}

func TestOpenJobStoreRejectsCorruptionBeforeSchemaInitialization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "state.db")
	corruptDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE damaged(value TEXT)`,
		`INSERT INTO damaged(value) VALUES(NULL)`,
		`PRAGMA writable_schema=ON`,
		`UPDATE sqlite_master SET sql='CREATE TABLE damaged(value TEXT NOT NULL)' WHERE type='table' AND name='damaged'`,
		`PRAGMA writable_schema=OFF`,
	} {
		if _, err := corruptDB.Exec(statement); err != nil {
			_ = corruptDB.Close()
			t.Fatalf("prepare corrupt SQLite fixture with %q: %v", statement, err)
		}
	}
	if err := corruptDB.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := openJobStore(root)
	if err == nil {
		_ = store.close()
		t.Fatal("openJobStore accepted a database that fails quick_check")
	}
	var integrityErr *databaseIntegrityError
	if !errors.As(err, &integrityErr) || !strings.Contains(err.Error(), "NULL value in damaged.value") {
		t.Fatalf("openJobStore error = %v, want a quick_check integrity error", err)
	}

	checkDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = checkDB.Close() }()
	var migrationTableCount int
	if err := checkDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&migrationTableCount); err != nil {
		t.Fatal(err)
	}
	if migrationTableCount != 0 {
		t.Fatalf("startup created %d migration table(s) before rejecting corrupt database", migrationTableCount)
	}
}
