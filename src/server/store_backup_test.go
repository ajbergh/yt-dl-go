package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newSnapshotTestRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func migrationSnapshots(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "backups", "state-before-migration-*.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func snapshotMigrationVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestOpenJobStoreCreatesOneSnapshotForFreshDatabase(t *testing.T) {
	root := newSnapshotTestRoot(t)
	store, err := openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	snapshots := migrationSnapshots(t, root)
	if len(snapshots) != 1 {
		t.Fatalf("fresh database created %d migration snapshots, want exactly one: %v", len(snapshots), snapshots)
	}
	if version := snapshotMigrationVersion(t, snapshots[0]); version != 1 {
		t.Fatalf("fresh database snapshot version = %d, want 1", version)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{
			filepath.Join(root, "backups"): 0700,
			snapshots[0]:                   0600,
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != want {
				t.Errorf("backup permissions for %s = %04o, want %04o", path, got, want)
			}
		}
	}

	store, err = openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	if got := len(migrationSnapshots(t, root)); got != 1 {
		t.Fatalf("reopening current database created %d snapshots, want 1 total", got)
	}
}

func TestOpenJobStoreSnapshotsEachPendingMigration(t *testing.T) {
	root := newSnapshotTestRoot(t)
	store, err := openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`ALTER TABLE library_sources DROP COLUMN download_location`); err != nil {
		_ = store.close()
		t.Fatalf("prepare v25 database by removing the v26 column: %v", err)
	}
	if _, err := store.db.Exec(`DROP TABLE library_item_exclusions`); err != nil {
		_ = store.close()
		t.Fatalf("prepare v25 database by removing the v27 table: %v", err)
	}
	for _, column := range []string{"retention", "max_job_bytes", "job_timeout", "chrome_path", "download_slots"} {
		if _, err := store.db.Exec(`ALTER TABLE app_settings DROP COLUMN ` + column); err != nil {
			_ = store.close()
			t.Fatalf("prepare v25 database by removing the v30 %s column: %v", column, err)
		}
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version > 25`); err != nil {
		_ = store.close()
		t.Fatalf("prepare v25 migration history: %v", err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	store, err = openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	snapshots := migrationSnapshots(t, root)
	if len(snapshots) != 6 { // initial v1 plus upgrades before v26 through v30.
		t.Fatalf("migration snapshot count = %d, want 6: %v", len(snapshots), snapshots)
	}
	foundV26, foundV27, foundV28, foundV29, foundV30 := false, false, false, false, false
	for _, path := range snapshots {
		name := filepath.Base(path)
		switch {
		case strings.Contains(name, "v26-from-v25-"):
			foundV26 = true
			if version := snapshotMigrationVersion(t, path); version != 25 {
				t.Errorf("v26 snapshot records schema version %d, want 25", version)
			}
		case strings.Contains(name, "v27-from-v26-"):
			foundV27 = true
			if version := snapshotMigrationVersion(t, path); version != 26 {
				t.Errorf("v27 snapshot records schema version %d, want 26", version)
			}
		case strings.Contains(name, "v28-from-v27-"):
			foundV28 = true
			if version := snapshotMigrationVersion(t, path); version != 27 {
				t.Errorf("v28 snapshot records schema version %d, want 27", version)
			}
		case strings.Contains(name, "v29-from-v28-"):
			foundV29 = true
			if version := snapshotMigrationVersion(t, path); version != 28 {
				t.Errorf("v29 snapshot records schema version %d, want 28", version)
			}
		case strings.Contains(name, "v30-from-v29-"):
			foundV30 = true
			if version := snapshotMigrationVersion(t, path); version != 29 {
				t.Errorf("v30 snapshot records schema version %d, want 29", version)
			}
		}
	}
	if !foundV26 || !foundV27 || !foundV28 || !foundV29 || !foundV30 {
		t.Fatalf("missing per-migration snapshots: v26=%v v27=%v v28=%v v29=%v v30=%v; paths=%v", foundV26, foundV27, foundV28, foundV29, foundV30, snapshots)
	}
}

func TestOpenJobStoreSnapshotsV20RepairBeforeRecreatingLibraryTable(t *testing.T) {
	root := newSnapshotTestRoot(t)
	store, err := openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP TABLE library_items`); err != nil {
		_ = store.close()
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	store, err = openJobStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	var tableCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='library_items'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatalf("library_items table count after repair = %d, want 1", tableCount)
	}
	for _, object := range []struct {
		kind string
		name string
	}{
		{kind: "index", name: "library_items_source_job"},
		{kind: "index", name: "library_items_category"},
		{kind: "index", name: "library_items_channel"},
		{kind: "index", name: "library_items_effective_category"},
		{kind: "index", name: "library_items_effective_channel"},
		{kind: "trigger", name: "library_items_search_ai"},
		{kind: "trigger", name: "library_items_search_ad"},
		{kind: "trigger", name: "library_items_search_au"},
	} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?`, object.kind, object.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("repaired %s %q count = %d, want 1", object.kind, object.name, count)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO library_items(file_id,source_job_id,source_item_index,file_json,output_path,created_at) VALUES('backupprobe','backupprobejob',1,'{"name":"snapshotprobetoken"}','',0)`); err != nil {
		t.Fatalf("insert probe row after Library schema repair: %v", err)
	}
	var matches int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM library_search_files WHERE library_search_files MATCH 'snapshotprobetoken'`).Scan(&matches); err != nil {
		t.Fatalf("search repaired Library FTS index: %v", err)
	}
	if matches != 1 {
		t.Fatalf("repaired Library FTS matches = %d, want 1", matches)
	}
	if _, err := store.db.Exec(`DELETE FROM library_items WHERE file_id='backupprobe'`); err != nil {
		t.Fatalf("delete probe row after Library schema repair: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM library_search_files WHERE library_search_files MATCH 'snapshotprobetoken'`).Scan(&matches); err != nil {
		t.Fatalf("search repaired Library FTS index after delete: %v", err)
	}
	if matches != 0 {
		t.Fatalf("repaired Library FTS retained deleted row, match count = %d", matches)
	}

	snapshots := migrationSnapshots(t, root)
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count after v20 repair = %d, want 2: %v", len(snapshots), snapshots)
	}
	foundRepairSnapshot := false
	for _, path := range snapshots {
		if !strings.Contains(filepath.Base(path), "v20-from-v30-") {
			continue
		}
		foundRepairSnapshot = true
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var preexistingTableCount int
		err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='library_items'`).Scan(&preexistingTableCount)
		_ = db.Close()
		if err != nil {
			t.Fatal(err)
		}
		if preexistingTableCount != 0 {
			t.Fatalf("v20 repair snapshot already contains library_items (count %d)", preexistingTableCount)
		}
	}
	if !foundRepairSnapshot {
		t.Fatalf("missing pre-repair snapshot: %v", snapshots)
	}
}

func TestOpenJobStoreDoesNotMigrateWhenSnapshotCreationFails(t *testing.T) {
	root := newSnapshotTestRoot(t)
	if err := os.WriteFile(filepath.Join(root, "backups"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := openJobStore(root); err == nil {
		_ = store.close()
		t.Fatal("openJobStore succeeded despite failure to create a pre-migration snapshot")
	}

	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("schema version after snapshot creation failure = %d, want 1", version)
	}
	var v2TableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='job_settings'`).Scan(&v2TableCount); err != nil {
		t.Fatal(err)
	}
	if v2TableCount != 0 {
		t.Fatalf("migration v2 ran despite snapshot failure (job_settings table count %d)", v2TableCount)
	}
}
