// store.go persists jobs, finalized-file metadata, preferences, and resumable
// transfer state in the pure-Go SQLite database under DATA_DIR.
package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// jobStore is the durable source of truth for job history, completed items,
// preferences, and resume state; live queue and cancellation handles stay in RAM.
type jobStore struct {
	db                *sql.DB
	queueItemsReady   bool
	queueItemsCacheMu sync.Mutex
	queueItemsCache   map[string]map[string][]byte
	jobFilesCache     map[string]map[int][]byte
	jobFailuresCache  map[string]map[int]string
	libraryItemsCache map[string]map[string][32]byte
}

type AppSettings struct {
	DefaultQuality            string   `json:"defaultQuality"`
	DefaultVideoStrategy      string   `json:"defaultVideoStrategy"`
	Allow360pFallback         bool     `json:"allow360pFallback"`
	MaxConcurrentDownloads    int      `json:"maxConcurrentDownloads"`
	BandwidthLimitBytesPerSec int64    `json:"bandwidthLimitBytesPerSec"`
	NotificationsEnabled      bool     `json:"notificationsEnabled"`
	DownloadLocation          string   `json:"downloadLocation"`
	NamingPattern             string   `json:"namingPattern"`
	SubfolderSorting          string   `json:"subfolderSorting"`
	OutputFileMode            string   `json:"outputFileMode"`
	OutputFolderMode          string   `json:"outputFolderMode"`
	DefaultCategory           string   `json:"defaultCategory"`
	UserCategories            []string `json:"userCategories"`
	StorageMode               string   `json:"storageMode"`
}

type storedJob struct {
	job       Job
	dir       string
	done      time.Time
	items     map[int][]mediaFile
	resuming  bool
	cancelled bool
}

type additionalStoredFile struct {
	File       mediaFile `json:"file"`
	OutputPath string    `json:"outputPath,omitempty"`
}

type databaseIntegrityError struct {
	detail string
}

func (e *databaseIntegrityError) Error() string {
	return "state database failed PRAGMA quick_check: " + e.detail
}

func stateDatabaseDSN(root string) (string, error) {
	databasePath, err := filepath.Abs(filepath.Join(root, "state.db"))
	if err != nil {
		return "", err
	}
	return sqliteDatabaseDSN(databasePath), nil
}

func sqliteDatabaseDSN(databasePath string) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(ON)")
	if filepath.VolumeName(databasePath) != "" {
		// Encoding a Windows path as a file URI keeps drive, UNC, and extended-
		// length prefixes in the URI path instead of interpreting '?' in \\?\ as
		// the start of the DSN query.
		return "file:" + url.PathEscape(databasePath) + "?" + query.Encode()
	}
	uriPath := filepath.ToSlash(databasePath)
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String()
}

func checkDatabaseIntegrity(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA quick_check`)
	if err != nil {
		return fmt.Errorf("run PRAGMA quick_check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	checked := false
	for rows.Next() {
		checked = true
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("read PRAGMA quick_check result: %w", err)
		}
		if result != "ok" {
			return &databaseIntegrityError{detail: result}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read PRAGMA quick_check results: %w", err)
	}
	if !checked {
		return &databaseIntegrityError{detail: "no result returned"}
	}
	return nil
}

func stateDatabaseIsNew(root string) (bool, error) {
	info, err := os.Stat(filepath.Join(root, "state.db"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size() == 0, nil
}

func (s *jobStore) snapshotBeforeMigration(root string, fromVersion, targetVersion int) (string, error) {
	backupDir := filepath.Join(root, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("create state backup directory: %w", err)
	}
	if err := os.Chmod(backupDir, 0700); err != nil {
		return "", fmt.Errorf("restrict state backup directory: %w", err)
	}
	destination := filepath.Join(backupDir, fmt.Sprintf("state-before-migration-v%d-from-v%d-%s.sqlite", targetVersion, fromVersion, randomID(8)))
	partialPath := destination + ".partial"
	quotedPartialPath := strings.ReplaceAll(partialPath, "'", "''")
	if _, err := s.db.Exec(`VACUUM INTO '` + quotedPartialPath + `'`); err != nil {
		return "", removeFailedSnapshot(partialPath, fmt.Errorf("snapshot state before migration v%d: %w", targetVersion, err))
	}
	if err := os.Chmod(partialPath, 0600); err != nil {
		return "", removeFailedSnapshot(partialPath, fmt.Errorf("restrict state backup file: %w", err))
	}
	if err := os.Rename(partialPath, destination); err != nil {
		return "", removeFailedSnapshot(partialPath, fmt.Errorf("publish state backup: %w", err))
	}
	return destination, nil
}

func removeFailedSnapshot(path string, cause error) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("remove incomplete state backup: %w", err))
	}
	return cause
}

// openJobStore opens DATA_DIR/state.db, applies SQLite runtime pragmas, creates
// missing tables, and runs schema migrations before any jobs are loaded.
func openJobStore(root string) (*jobStore, error) {
	freshDatabase, err := stateDatabaseIsNew(root)
	if err != nil {
		return nil, fmt.Errorf("inspect state database before open: %w", err)
	}
	dsn, err := stateDatabaseDSN(root)
	if err != nil {
		return nil, fmt.Errorf("resolve state database path: %w", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &jobStore{db: db}
	if err := checkDatabaseIntegrity(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			url TEXT NOT NULL,
			kind TEXT NOT NULL,
			quality TEXT NOT NULL,
			status TEXT NOT NULL,
			title TEXT NOT NULL,
			progress REAL,
			current_item TEXT NOT NULL,
			completed_count INTEGER NOT NULL,
			total_count INTEGER,
			error TEXT NOT NULL,
			created_at TEXT NOT NULL,
			note TEXT NOT NULL,
			dir TEXT NOT NULL,
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			done_at INTEGER,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS job_files (
			job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
			item_index INTEGER NOT NULL,
			file_id TEXT NOT NULL,
			name TEXT NOT NULL,
			size INTEGER NOT NULL,
			height INTEGER NOT NULL,
			mime_type TEXT NOT NULL,
			PRIMARY KEY (job_id, item_index),
			UNIQUE (job_id, file_id)
		)`,
		`CREATE TABLE IF NOT EXISTS download_parts (
			job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
			item_index INTEGER NOT NULL,
			path TEXT NOT NULL,
			completed_bytes INTEGER NOT NULL,
			expected_bytes INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (job_id, item_index)
		)`,
		`CREATE TABLE IF NOT EXISTS job_failures (
			job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
			item_index INTEGER NOT NULL,
			error TEXT NOT NULL,
			PRIMARY KEY (job_id, item_index)
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize state database: %w", err)
		}
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES (1)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("record state database migration: %w", err)
	}
	freshSnapshotMade := false
	runMigration := func(target int, migration func() error) error {
		var version int
		if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
			return err
		}
		pending := version < target
		if target == 20 && !pending {
			var libraryItemsTableCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='library_items'`).Scan(&libraryItemsTableCount); err != nil {
				return err
			}
			pending = libraryItemsTableCount == 0
		}
		if pending && (!freshDatabase || !freshSnapshotMade) {
			if _, err := store.snapshotBeforeMigration(root, version, target); err != nil {
				return err
			}
			freshSnapshotMade = freshSnapshotMade || freshDatabase
		}
		return migration()
	}
	if err := runMigration(2, store.migrateV2); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(3, store.migrateV3); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(4, store.migrateV4); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(5, store.migrateV5); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(6, store.migrateV6); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(7, store.migrateV7); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(8, store.migrateV8); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(9, store.migrateV9); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(10, store.migrateV10); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(11, store.migrateV11); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(12, store.migrateV12); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(13, store.migrateV13); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(14, store.migrateV14); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(15, store.migrateV15); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(16, store.migrateV16); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(17, store.migrateV17); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(18, store.migrateV18); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(19, store.migrateV19); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(20, func() error { return store.migrateV20(root) }); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(21, store.migrateV21); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(22, store.migrateV22); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(23, store.migrateV23); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(24, store.migrateV24); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(25, store.migrateV25); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(26, store.migrateV26); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := runMigration(27, store.migrateV27); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	store.queueItemsReady = true
	return store, nil
}

func (s *jobStore) migrateV22() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 22 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS jobs_library_page ON jobs(status,created_at DESC,id DESC)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS library_items_category ON library_items(json_extract(file_json,'$.category'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS library_items_channel ON library_items(json_extract(file_json,'$.author'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (22)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *jobStore) migrateV23() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 23 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS jobs_library_category ON jobs(category)`,
		`CREATE INDEX IF NOT EXISTS jobs_library_type ON jobs(media_type)`,
		`CREATE INDEX IF NOT EXISTS library_items_effective_category ON library_items(COALESCE(NULLIF(json_extract(file_json,'$.category'),''),'Uncategorized'))`,
		`CREATE INDEX IF NOT EXISTS library_items_effective_channel ON library_items(COALESCE(NULLIF(TRIM(json_extract(file_json,'$.author')),''),'Unknown channel'))`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (23)`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV24 gives durable Library rows their own compact source metadata so
// Library reads no longer depend on the transient jobs table.
func (s *jobStore) migrateV24() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 24 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS library_sources (
		source_job_id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		kind TEXT NOT NULL,
		quality TEXT NOT NULL,
		video_strategy TEXT NOT NULL,
		allow_360p_fallback INTEGER NOT NULL,
		media_type TEXT NOT NULL,
		audio_format TEXT NOT NULL,
		audio_bitrate INTEGER NOT NULL,
		subtitle_language TEXT NOT NULL,
		subtitle_format TEXT NOT NULL,
		split_by_chapter INTEGER NOT NULL,
		status TEXT NOT NULL,
		title TEXT NOT NULL,
		progress REAL,
		current_item TEXT NOT NULL,
		completed_count INTEGER NOT NULL,
		total_count INTEGER,
		error TEXT NOT NULL,
		created_at TEXT NOT NULL,
		note TEXT NOT NULL,
		category TEXT NOT NULL,
		storage_mode TEXT NOT NULL,
		queue_position INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS library_sources_page ON library_sources(status,created_at DESC,source_job_id DESC)`,
		`CREATE INDEX IF NOT EXISTS library_sources_category ON library_sources(category)`,
		`CREATE INDEX IF NOT EXISTS library_sources_type ON library_sources(media_type)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO library_sources (
		source_job_id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,split_by_chapter,status,title,progress,current_item,completed_count,total_count,error,created_at,note,category,storage_mode,queue_position
	)
	SELECT j.id,j.url,j.kind,j.quality,j.video_strategy,j.allow_360p_fallback,j.media_type,j.audio_format,j.audio_bitrate,j.subtitle_language,j.subtitle_format,j.split_by_chapter,j.status,j.title,j.progress,j.current_item,j.completed_count,j.total_count,j.error,j.created_at,j.note,j.category,j.storage_mode,j.queue_position
	FROM jobs j WHERE j.status IN ('completed','partial','failed','cancelled') AND EXISTS (SELECT 1 FROM library_items li WHERE li.source_job_id=j.id)
	ON CONFLICT(source_job_id) DO NOTHING`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (24)`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV25 adds trigram indexes as candidate filters while leaving the
// existing substring predicate authoritative for Library search semantics.
func (s *jobStore) migrateV25() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 25 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`ALTER TABLE library_sources ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE library_sources SET search_text=
		COALESCE(title,'')||' '||COALESCE(url,'')||' '||COALESCE(category,'')||' '||COALESCE(audio_format,'')||' '||COALESCE(subtitle_language,'')`); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE VIRTUAL TABLE library_search_jobs USING fts5(search_text,content='library_sources',content_rowid='rowid',tokenize='trigram')`,
		`CREATE VIRTUAL TABLE library_search_files USING fts5(file_json,content='library_items',content_rowid='rowid',tokenize='trigram')`,
		`CREATE TRIGGER library_sources_search_ai AFTER INSERT ON library_sources BEGIN
			INSERT INTO library_search_jobs(rowid,search_text) VALUES(new.rowid,new.search_text);
		END`,
		`CREATE TRIGGER library_sources_search_ad AFTER DELETE ON library_sources BEGIN
			INSERT INTO library_search_jobs(library_search_jobs,rowid,search_text) VALUES('delete',old.rowid,old.search_text);
		END`,
		`CREATE TRIGGER library_sources_search_au AFTER UPDATE OF search_text ON library_sources BEGIN
			INSERT INTO library_search_jobs(library_search_jobs,rowid,search_text) VALUES('delete',old.rowid,old.search_text);
			INSERT INTO library_search_jobs(rowid,search_text) VALUES(new.rowid,new.search_text);
		END`,
		`CREATE TRIGGER library_items_search_ai AFTER INSERT ON library_items BEGIN
			INSERT INTO library_search_files(rowid,file_json) VALUES(new.rowid,new.file_json);
		END`,
		`CREATE TRIGGER library_items_search_ad AFTER DELETE ON library_items BEGIN
			INSERT INTO library_search_files(library_search_files,rowid,file_json) VALUES('delete',old.rowid,old.file_json);
		END`,
		`CREATE TRIGGER library_items_search_au AFTER UPDATE OF file_json ON library_items BEGIN
			INSERT INTO library_search_files(library_search_files,rowid,file_json) VALUES('delete',old.rowid,old.file_json);
			INSERT INTO library_search_files(rowid,file_json) VALUES(new.rowid,new.file_json);
		END`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO library_search_jobs(library_search_jobs) VALUES('rebuild')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO library_search_files(library_search_files) VALUES('rebuild')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (25)`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV26 captures each Library source's original published-output root so
// its files remain safely manageable after download history is removed.
func (s *jobStore) migrateV26() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 26 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`ALTER TABLE library_sources ADD COLUMN download_location TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE library_sources SET download_location=COALESCE((SELECT output_location FROM jobs WHERE jobs.id=library_sources.source_job_id),'')`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (26)`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV27 records Library files intentionally removed while their source
// job remains available for history and retry inspection.
func (s *jobStore) migrateV27() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 27 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`CREATE TABLE library_item_exclusions (
			source_job_id TEXT NOT NULL,
			file_id TEXT NOT NULL,
			removed_at INTEGER NOT NULL,
			PRIMARY KEY(source_job_id,file_id)
		)`,
		`CREATE INDEX library_item_exclusions_source ON library_item_exclusions(source_job_id)`,
		`INSERT INTO schema_migrations(version) VALUES (27)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV21() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 21 {
		return nil
	}
	type legacyQueue struct {
		jobID string
		data  string
	}
	rows, err := s.db.Query(`SELECT id,queue_items FROM jobs ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	var legacy []legacyQueue
	for rows.Next() {
		var item legacyQueue
		if err := rows.Scan(&item.jobID, &item.data); err != nil {
			_ = rows.Close()
			return err
		}
		legacy = append(legacy, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS queue_items (
		job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
		item_key TEXT NOT NULL,
		position INTEGER NOT NULL,
		playlist_index INTEGER NOT NULL,
		video_id TEXT NOT NULL,
		title TEXT NOT NULL,
		author TEXT NOT NULL,
		duration_seconds INTEGER NOT NULL,
		thumbnail_url TEXT NOT NULL,
		status TEXT NOT NULL,
		progress REAL,
		downloaded_bytes INTEGER NOT NULL,
		total_bytes INTEGER NOT NULL,
		speed_bytes_per_sec INTEGER NOT NULL,
		eta_seconds INTEGER NOT NULL,
		error TEXT NOT NULL,
		file_id TEXT NOT NULL,
		file_ids_json TEXT NOT NULL,
		retry_requested INTEGER NOT NULL,
		PRIMARY KEY(job_id,item_key)
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS queue_items_job_position ON queue_items(job_id,position)`); err != nil {
		return err
	}
	for _, saved := range legacy {
		var items []queueItem
		if err := json.Unmarshal([]byte(saved.data), &items); err != nil {
			return fmt.Errorf("decode legacy queue items for job %s: %w", saved.jobID, err)
		}
		if err := replaceQueueItems(tx, saved.jobID, items); err != nil {
			return fmt.Errorf("migrate queue items for job %s: %w", saved.jobID, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (21)`); err != nil {
		return err
	}
	return tx.Commit()
}

func queueItemKey(item queueItem) string {
	if item.PlaylistIndex > 0 {
		return fmt.Sprintf("playlist:%d", item.PlaylistIndex)
	}
	return fmt.Sprintf("item:%d", item.Index)
}

func replaceQueueItems(tx *sql.Tx, jobID string, items []queueItem) error {
	existing := make(map[string]struct{})
	rows, err := tx.Query(`SELECT item_key FROM queue_items WHERE job_id=?`, jobID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return err
		}
		existing[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for position, item := range items {
		key := queueItemKey(item)
		if err := upsertQueueItem(tx, jobID, key, position+1, item); err != nil {
			return err
		}
		delete(existing, key)
	}
	for key := range existing {
		if _, err := tx.Exec(`DELETE FROM queue_items WHERE job_id=? AND item_key=?`, jobID, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *jobStore) loadQueueItemsQuery(where string, args []any) (map[string][]queueItem, error) {
	query := `SELECT job_id,position,playlist_index,video_id,title,author,duration_seconds,thumbnail_url,status,progress,downloaded_bytes,total_bytes,speed_bytes_per_sec,eta_seconds,error,file_id,file_ids_json,retry_requested
		FROM queue_items ` + where + ` ORDER BY job_id,position`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]queueItem)
	for rows.Next() {
		var jobID, fileIDsJSON string
		var item queueItem
		var progress sql.NullFloat64
		var retryRequested int
		if err := rows.Scan(&jobID, &item.Index, &item.PlaylistIndex, &item.VideoID, &item.Title, &item.Author, &item.DurationSeconds, &item.ThumbnailURL, &item.Status, &progress, &item.DownloadedBytes, &item.TotalBytes, &item.SpeedBytesPerSec, &item.ETASeconds, &item.Error, &item.FileID, &fileIDsJSON, &retryRequested); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if progress.Valid {
			value := progress.Float64
			item.Progress = &value
		}
		if err := json.Unmarshal([]byte(fileIDsJSON), &item.FileIDs); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("decode saved queue file IDs: %w", err)
		}
		if retryRequested != 0 {
			item.RetryRequested = true
		}
		result[jobID] = append(result[jobID], item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func saveChangedQueueItems(tx *sql.Tx, jobID string, items []queueItem, cached map[string][]byte) (map[string][]byte, error) {
	existing := make(map[string]struct{}, len(cached))
	for key := range cached {
		existing[key] = struct{}{}
	}
	if cached == nil {
		rows, err := tx.Query(`SELECT item_key FROM queue_items WHERE job_id=?`, jobID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				_ = rows.Close()
				return nil, err
			}
			existing[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	updated := make(map[string][]byte, len(items))
	for position, item := range items {
		key := queueItemKey(item)
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		updated[key] = encoded
		delete(existing, key)
		if previous, ok := cached[key]; ok && bytes.Equal(previous, encoded) {
			continue
		}
		if err := upsertQueueItem(tx, jobID, key, position+1, item); err != nil {
			return nil, err
		}
	}
	for key := range existing {
		if _, err := tx.Exec(`DELETE FROM queue_items WHERE job_id=? AND item_key=?`, jobID, key); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

func upsertQueueItem(tx *sql.Tx, jobID, key string, position int, item queueItem) error {
	fileIDs, err := json.Marshal(item.FileIDs)
	if err != nil {
		return err
	}
	progress := any(nil)
	if item.Progress != nil {
		progress = *item.Progress
	}
	retryRequested := 0
	if item.RetryRequested {
		retryRequested = 1
	}
	_, err = tx.Exec(`INSERT INTO queue_items(job_id,item_key,position,playlist_index,video_id,title,author,duration_seconds,thumbnail_url,status,progress,downloaded_bytes,total_bytes,speed_bytes_per_sec,eta_seconds,error,file_id,file_ids_json,retry_requested)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(job_id,item_key) DO UPDATE SET position=excluded.position,playlist_index=excluded.playlist_index,video_id=excluded.video_id,title=excluded.title,author=excluded.author,duration_seconds=excluded.duration_seconds,thumbnail_url=excluded.thumbnail_url,status=excluded.status,progress=excluded.progress,downloaded_bytes=excluded.downloaded_bytes,total_bytes=excluded.total_bytes,speed_bytes_per_sec=excluded.speed_bytes_per_sec,eta_seconds=excluded.eta_seconds,error=excluded.error,file_id=excluded.file_id,file_ids_json=excluded.file_ids_json,retry_requested=excluded.retry_requested
		WHERE queue_items.position IS NOT excluded.position OR queue_items.playlist_index IS NOT excluded.playlist_index OR queue_items.video_id IS NOT excluded.video_id OR queue_items.title IS NOT excluded.title OR queue_items.author IS NOT excluded.author OR queue_items.duration_seconds IS NOT excluded.duration_seconds OR queue_items.thumbnail_url IS NOT excluded.thumbnail_url OR queue_items.status IS NOT excluded.status OR queue_items.progress IS NOT excluded.progress OR queue_items.downloaded_bytes IS NOT excluded.downloaded_bytes OR queue_items.total_bytes IS NOT excluded.total_bytes OR queue_items.speed_bytes_per_sec IS NOT excluded.speed_bytes_per_sec OR queue_items.eta_seconds IS NOT excluded.eta_seconds OR queue_items.error IS NOT excluded.error OR queue_items.file_id IS NOT excluded.file_id OR queue_items.file_ids_json IS NOT excluded.file_ids_json OR queue_items.retry_requested IS NOT excluded.retry_requested`,
		jobID, key, position, item.PlaylistIndex, item.VideoID, item.Title, item.Author, item.DurationSeconds, item.ThumbnailURL, item.Status, progress, item.DownloadedBytes, item.TotalBytes, item.SpeedBytesPerSec, item.ETASeconds, item.Error, item.FileID, string(fileIDs), retryRequested)
	return err
}

func (s *jobStore) loadLegacyQueueItems() (map[string][]queueItem, error) {
	rows, err := s.db.Query(`SELECT id,queue_items FROM jobs ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]queueItem)
	for rows.Next() {
		var jobID, data string
		var items []queueItem
		if err := rows.Scan(&jobID, &data); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &items); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("decode saved queue entries for job %s: %w", jobID, err)
		}
		result[jobID] = items
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

// migrateV20 introduces durable per-file Library rows alongside the existing
// job history. Job deletion semantics remain explicit because library_items
// deliberately has no foreign key to jobs.
func (s *jobStore) migrateV20(root string) error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	repairingExistingLibrary := false
	if version >= 20 {
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='library_items'`).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return nil
		}
		repairingExistingLibrary = true
	}
	createTx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = createTx.Rollback() }()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS library_items (
			file_id TEXT PRIMARY KEY,
			source_job_id TEXT NOT NULL,
			source_item_index INTEGER NOT NULL,
			file_json TEXT NOT NULL,
			output_path TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS library_items_source_job ON library_items(source_job_id, source_item_index)`,
	} {
		if _, err := createTx.Exec(statement); err != nil {
			return err
		}
	}
	if err := createTx.Commit(); err != nil {
		return err
	}

	// Reuse the canonical row loader so the migration includes every finalized
	// primary and grouped file, including chapter outputs stored in JSON.
	loaded, err := s.loadJobs(root)
	if err != nil {
		return err
	}
	backfillTx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = backfillTx.Rollback() }()
	for _, saved := range loaded {
		createdAt := int64(0)
		if !saved.done.IsZero() {
			createdAt = saved.done.UnixNano()
		}
		for itemIndex, group := range saved.items {
			for _, file := range group {
				if err := upsertLibraryItem(backfillTx, saved.job.ID, itemIndex, file, createdAt); err != nil {
					return err
				}
			}
		}
	}
	if repairingExistingLibrary {
		if err := restoreLibraryItemsSchema(backfillTx, version); err != nil {
			return err
		}
	}
	if _, err := backfillTx.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES (20)`); err != nil {
		return err
	}
	return backfillTx.Commit()
}

func restoreLibraryItemsSchema(tx *sql.Tx, version int) error {
	if version >= 22 {
		for _, statement := range []string{
			`CREATE INDEX IF NOT EXISTS library_items_category ON library_items(json_extract(file_json,'$.category'))`,
			`CREATE INDEX IF NOT EXISTS library_items_channel ON library_items(json_extract(file_json,'$.author'))`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	}
	if version >= 23 {
		for _, statement := range []string{
			`CREATE INDEX IF NOT EXISTS library_items_effective_category ON library_items(COALESCE(NULLIF(json_extract(file_json,'$.category'),''),'Uncategorized'))`,
			`CREATE INDEX IF NOT EXISTS library_items_effective_channel ON library_items(COALESCE(NULLIF(TRIM(json_extract(file_json,'$.author')),''),'Unknown channel'))`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	}
	if version < 25 {
		return nil
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS library_search_files USING fts5(file_json,content='library_items',content_rowid='rowid',tokenize='trigram')`); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS library_items_search_ai AFTER INSERT ON library_items BEGIN
			INSERT INTO library_search_files(rowid,file_json) VALUES(new.rowid,new.file_json);
		END`,
		`CREATE TRIGGER IF NOT EXISTS library_items_search_ad AFTER DELETE ON library_items BEGIN
			INSERT INTO library_search_files(library_search_files,rowid,file_json) VALUES('delete',old.rowid,old.file_json);
		END`,
		`CREATE TRIGGER IF NOT EXISTS library_items_search_au AFTER UPDATE OF file_json ON library_items BEGIN
			INSERT INTO library_search_files(library_search_files,rowid,file_json) VALUES('delete',old.rowid,old.file_json);
			INSERT INTO library_search_files(rowid,file_json) VALUES(new.rowid,new.file_json);
		END`,
		`INSERT INTO library_search_files(library_search_files) VALUES('rebuild')`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func upsertLibraryItem(tx *sql.Tx, sourceJobID string, sourceItemIndex int, file mediaFile, createdAt int64) error {
	if sourceItemIndex < 1 {
		sourceItemIndex = file.SourceItemIndex
	}
	if sourceItemIndex < 1 {
		sourceItemIndex = 1
	}
	file.SourceItemIndex = sourceItemIndex
	fileID, data, _, err := prepareLibraryItem(sourceJobID, sourceItemIndex, file)
	if err != nil {
		return err
	}
	if createdAt == 0 {
		createdAt = time.Now().UnixNano()
	}
	_, err = tx.Exec(`INSERT INTO library_items(file_id,source_job_id,source_item_index,file_json,output_path,created_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(file_id) DO UPDATE SET source_job_id=excluded.source_job_id,source_item_index=excluded.source_item_index,file_json=excluded.file_json,output_path=excluded.output_path`,
		fileID, sourceJobID, sourceItemIndex, string(data), file.OutputPath, createdAt)
	return err
}

func prepareLibraryItem(sourceJobID string, sourceItemIndex int, file mediaFile) (string, []byte, [32]byte, error) {
	if sourceItemIndex < 1 {
		sourceItemIndex = file.SourceItemIndex
	}
	if sourceItemIndex < 1 {
		sourceItemIndex = 1
	}
	file.ID = libraryItemID(sourceJobID, sourceItemIndex, file)
	file.SourceItemIndex = sourceItemIndex
	data, err := json.Marshal(file)
	if err != nil {
		return "", nil, [32]byte{}, err
	}
	hasher := sha256.New()
	_, _ = hasher.Write(data)
	_, _ = hasher.Write([]byte{0})
	_, _ = io.WriteString(hasher, file.OutputPath)
	var signature [32]byte
	copy(signature[:], hasher.Sum(nil))
	return file.ID, data, signature, nil
}

func libraryItemID(sourceJobID string, sourceItemIndex int, file mediaFile) string {
	if file.ID != "" {
		return file.ID
	}
	if sourceItemIndex < 1 {
		sourceItemIndex = file.SourceItemIndex
	}
	if sourceItemIndex < 1 {
		sourceItemIndex = 1
	}
	return fmt.Sprintf("%s:%d:%s", sourceJobID, sourceItemIndex, file.Name)
}

func (s *jobStore) migrateV19() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 19 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN split_by_chapter INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE job_files ADD COLUMN additional_files_json TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO schema_migrations(version) VALUES (19)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV18() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 18 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE job_files ADD COLUMN chapters_json TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO schema_migrations(version) VALUES (18)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV17() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 17 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE app_settings ADD COLUMN output_file_mode TEXT NOT NULL DEFAULT '0600'`,
		`ALTER TABLE app_settings ADD COLUMN output_folder_mode TEXT NOT NULL DEFAULT '0700'`,
		`ALTER TABLE jobs ADD COLUMN output_file_mode TEXT NOT NULL DEFAULT '0600'`,
		`ALTER TABLE jobs ADD COLUMN output_folder_mode TEXT NOT NULL DEFAULT '0700'`,
		`INSERT INTO schema_migrations(version) VALUES (17)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV16() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 16 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`DROP TABLE download_parts`,
		`CREATE TABLE download_parts (
			job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
			item_index INTEGER NOT NULL,
			part_key TEXT NOT NULL,
			path TEXT NOT NULL,
			completed_bytes INTEGER NOT NULL,
			expected_bytes INTEGER NOT NULL,
			itag INTEGER NOT NULL,
			source_fingerprint TEXT NOT NULL,
			method TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (job_id, item_index, part_key)
		)`,
		`INSERT INTO schema_migrations(version) VALUES (16)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV15() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 15 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN allow_360p_fallback INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE app_settings ADD COLUMN allow_360p_fallback INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO schema_migrations(version) VALUES (15)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV14() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 14 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN video_strategy TEXT NOT NULL DEFAULT 'best'`,
		`ALTER TABLE app_settings ADD COLUMN default_video_strategy TEXT NOT NULL DEFAULT 'best'`,
		`INSERT INTO schema_migrations(version) VALUES (14)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV13() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 13 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN subtitle_language TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN subtitle_format TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN subtitle_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN subtitle_error TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO schema_migrations(version) VALUES (13)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV12() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 12 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE app_settings ADD COLUMN notifications_enabled INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO schema_migrations(version) VALUES (12)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV11() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 11 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE app_settings ADD COLUMN bandwidth_limit_bytes_per_sec INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO schema_migrations(version) VALUES (11)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV10() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 10 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN queue_position INTEGER NOT NULL DEFAULT 0`,
		`UPDATE jobs SET queue_position=rowid WHERE queue_position=0`,
		`INSERT INTO schema_migrations(version) VALUES (10)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV9() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 9 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN audio_format TEXT NOT NULL DEFAULT ''`,
		`UPDATE jobs SET audio_format='mp3' WHERE media_type='audio' AND audio_format=''`,
		`INSERT INTO schema_migrations(version) VALUES (9)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV8() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 8 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE job_files ADD COLUMN thumbnail_mime_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN thumbnail_local_available INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO schema_migrations(version) VALUES (8)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV7() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 7 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE app_settings ADD COLUMN storage_mode TEXT NOT NULL DEFAULT 'managed-published'`,
		`ALTER TABLE jobs ADD COLUMN storage_mode TEXT NOT NULL DEFAULT 'managed-published'`,
		`INSERT INTO schema_migrations(version) VALUES (7)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) migrateV6() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 6 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE job_files ADD COLUMN managed_available INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE job_files ADD COLUMN published_available INTEGER NOT NULL DEFAULT 0`,
		`UPDATE job_files SET published_available = CASE WHEN output_path <> '' THEN 1 ELSE 0 END`,
		`INSERT INTO schema_migrations(version) VALUES (6)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migrateV5 persists output preferences, each job's captured preferences, and
// the user-visible destination for every finalized media file.
func (s *jobStore) migrateV5() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 5 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN output_location TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN naming_pattern TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN subfolder_sorting TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN category TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN output_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN output_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN output_relative_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE app_settings ADD COLUMN download_location TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE app_settings ADD COLUMN naming_pattern TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE app_settings ADD COLUMN subfolder_sorting TEXT NOT NULL DEFAULT 'channel'`,
		`ALTER TABLE app_settings ADD COLUMN default_category TEXT NOT NULL DEFAULT 'General'`,
		`ALTER TABLE app_settings ADD COLUMN user_categories TEXT NOT NULL DEFAULT '["Tech","Science","Coding","Music","Education","Gaming","Podcasts","Archival","General"]'`,
		`INSERT INTO schema_migrations(version) VALUES (5)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migrateV4 persists the playlist entries shown as individual queue rows.
func (s *jobStore) migrateV4() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 4 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN queue_items TEXT NOT NULL DEFAULT '[]'`,
		`INSERT INTO schema_migrations(version) VALUES (4)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migrateV3 stores each job's media selection and the user's bounded scheduler limit.
func (s *jobStore) migrateV3() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 3 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE jobs ADD COLUMN media_type TEXT NOT NULL DEFAULT 'video'`,
		`ALTER TABLE jobs ADD COLUMN audio_bitrate TEXT NOT NULL DEFAULT '192k'`,
		`ALTER TABLE app_settings ADD COLUMN max_concurrent_downloads INTEGER NOT NULL DEFAULT 3`,
		`INSERT INTO schema_migrations(version) VALUES (3)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migrateV2 atomically adds persisted file metadata and the singleton UI
// preferences row to databases at schema version 1.
func (s *jobStore) migrateV2() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 2 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`ALTER TABLE job_files ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN author TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN duration_seconds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE job_files ADD COLUMN thumbnail_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN publish_date TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN category TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE app_settings (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			default_quality TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`INSERT INTO app_settings(id, default_quality, updated_at) VALUES(1, 'best', 0)`,
		`INSERT INTO schema_migrations(version) VALUES (2)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *jobStore) saveConfig(c config) error {
	values := map[string]string{
		"addr":          c.addr,
		"data_dir":      c.root,
		"max_jobs":      fmt.Sprint(c.maxJobs),
		"max_job_bytes": fmt.Sprint(c.maxBytes),
		"job_timeout":   c.timeout.String(),
		"retention":     c.retain.String(),
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for key, value := range values {
		if _, err := tx.Exec(`INSERT INTO config(key,value,updated_at) VALUES(?,?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now().UnixNano()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *jobStore) loadAppSettings() (AppSettings, error) {
	settings := defaultAppSettings()
	var categories string
	if err := s.db.QueryRow(`SELECT default_quality,default_video_strategy,allow_360p_fallback,max_concurrent_downloads,bandwidth_limit_bytes_per_sec,notifications_enabled,download_location,naming_pattern,subfolder_sorting,default_category,user_categories,storage_mode,output_file_mode,output_folder_mode FROM app_settings WHERE id=1`).Scan(
		&settings.DefaultQuality, &settings.DefaultVideoStrategy, &settings.Allow360pFallback, &settings.MaxConcurrentDownloads, &settings.BandwidthLimitBytesPerSec, &settings.NotificationsEnabled, &settings.DownloadLocation, &settings.NamingPattern,
		&settings.SubfolderSorting, &settings.DefaultCategory, &categories, &settings.StorageMode, &settings.OutputFileMode, &settings.OutputFolderMode); err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(categories), &settings.UserCategories); err != nil {
		return settings, fmt.Errorf("decode saved categories: %w", err)
	}
	settings = mergeAppSettings(defaultAppSettings(), settings)
	return settings, nil
}

func (s *jobStore) saveAppSettings(settings AppSettings) error {
	settings = mergeAppSettings(defaultAppSettings(), settings)
	if settings.MaxConcurrentDownloads == 0 {
		settings.MaxConcurrentDownloads = 3
	}
	categories, err := json.Marshal(settings.UserCategories)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE app_settings SET default_quality=?,default_video_strategy=?,allow_360p_fallback=?,max_concurrent_downloads=?,bandwidth_limit_bytes_per_sec=?,notifications_enabled=?,download_location=?,naming_pattern=?,subfolder_sorting=?,default_category=?,user_categories=?,storage_mode=?,output_file_mode=?,output_folder_mode=?,updated_at=? WHERE id=1`,
		settings.DefaultQuality, settings.DefaultVideoStrategy, settings.Allow360pFallback, settings.MaxConcurrentDownloads, settings.BandwidthLimitBytesPerSec, settings.NotificationsEnabled, settings.DownloadLocation, settings.NamingPattern,
		settings.SubfolderSorting, settings.DefaultCategory, string(categories), settings.StorageMode, settings.OutputFileMode, settings.OutputFolderMode, time.Now().UnixNano())
	return err
}

func groupedJobFiles(j *jobState) map[int][]mediaFile {
	groups := make(map[int][]mediaFile, len(j.fileGroups)+len(j.fileItems))
	for index, files := range j.fileGroups {
		groups[index] = append([]mediaFile(nil), files...)
	}
	for index, file := range j.fileItems {
		if len(groups[index]) == 0 {
			groups[index] = []mediaFile{file}
		}
	}
	if len(groups) == 0 {
		for index, file := range j.Files {
			itemIndex := file.SourceItemIndex
			if itemIndex < 1 {
				itemIndex = index + 1
			}
			groups[itemIndex] = append(groups[itemIndex], file)
		}
	}
	return groups
}

func (s *jobStore) saveJob(j *jobState) error {
	if s == nil {
		return nil
	}
	s.queueItemsCacheMu.Lock()
	defer s.queueItemsCacheMu.Unlock()
	if s.queueItemsCache == nil {
		s.queueItemsCache = make(map[string]map[string][]byte)
	}
	if s.jobFilesCache == nil {
		s.jobFilesCache = make(map[string]map[int][]byte)
	}
	if s.jobFailuresCache == nil {
		s.jobFailuresCache = make(map[string]map[int]string)
	}
	if s.libraryItemsCache == nil {
		s.libraryItemsCache = make(map[string]map[string][32]byte)
	}
	var progress any
	if j.Progress != nil {
		progress = *j.Progress
	}
	var total any
	if j.TotalCount != nil {
		total = *j.TotalCount
	}
	var done any
	if !j.done.IsZero() {
		done = j.done.UnixNano()
	}
	cancelRequested := 0
	if j.cancelRequested {
		cancelRequested = 1
	}
	legacyQueueItems := "[]"
	if !s.queueItemsReady {
		encoded, err := json.Marshal(j.Items)
		if err != nil {
			return err
		}
		legacyQueueItems = string(encoded)
	}
	var updatedQueueCache map[string][]byte
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`INSERT INTO jobs
		(id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,split_by_chapter,status,title,progress,current_item,completed_count,total_count,error,created_at,note,dir,cancel_requested,done_at,updated_at,queue_items,output_location,naming_pattern,subfolder_sorting,category,storage_mode,queue_position,output_file_mode,output_folder_mode)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET url=excluded.url,kind=excluded.kind,quality=excluded.quality,video_strategy=excluded.video_strategy,
		allow_360p_fallback=excluded.allow_360p_fallback,
		media_type=excluded.media_type,audio_format=excluded.audio_format,audio_bitrate=excluded.audio_bitrate,
		subtitle_language=excluded.subtitle_language,subtitle_format=excluded.subtitle_format,split_by_chapter=excluded.split_by_chapter,
		status=excluded.status,title=excluded.title,progress=excluded.progress,current_item=excluded.current_item,
		completed_count=excluded.completed_count,total_count=excluded.total_count,error=excluded.error,
		created_at=excluded.created_at,note=excluded.note,dir=excluded.dir,cancel_requested=excluded.cancel_requested,
		done_at=excluded.done_at,updated_at=excluded.updated_at,queue_items=excluded.queue_items,
		output_location=excluded.output_location,naming_pattern=excluded.naming_pattern,subfolder_sorting=excluded.subfolder_sorting,category=excluded.category,storage_mode=excluded.storage_mode,queue_position=excluded.queue_position,output_file_mode=excluded.output_file_mode,output_folder_mode=excluded.output_folder_mode`,
		j.ID, j.URL, j.Kind, j.Quality, j.VideoStrategy, j.Allow360pFallback, j.MediaType, j.AudioFormat, j.AudioBitrate, j.SubtitleLanguage, j.SubtitleFormat, j.SplitByChapter, j.Status, j.Title, progress, j.CurrentItem, j.CompletedCount, total,
		j.Error, j.CreatedAt, j.Note, j.dir, cancelRequested, done, time.Now().UnixNano(), legacyQueueItems,
		j.DownloadLocation, j.NamingPattern, j.SubfolderSorting, j.Category, j.StorageMode, j.QueuePosition, j.OutputFileMode, j.OutputFolderMode)
	if err != nil {
		return err
	}
	if s.queueItemsReady {
		updatedQueueCache, err = saveChangedQueueItems(tx, j.ID, j.Items, s.queueItemsCache[j.ID])
		if err != nil {
			return err
		}
	}
	groups := groupedJobFiles(j)
	fileCache := s.jobFilesCache[j.ID]
	existingFileIndexes := make(map[int]struct{}, len(fileCache))
	for index := range fileCache {
		existingFileIndexes[index] = struct{}{}
	}
	if fileCache == nil {
		fileRows, err := tx.Query(`SELECT item_index FROM job_files WHERE job_id=?`, j.ID)
		if err != nil {
			return err
		}
		for fileRows.Next() {
			var index int
			if err := fileRows.Scan(&index); err != nil {
				_ = fileRows.Close()
				return err
			}
			existingFileIndexes[index] = struct{}{}
		}
		if err := fileRows.Err(); err != nil {
			_ = fileRows.Close()
			return err
		}
		if err := fileRows.Close(); err != nil {
			return err
		}
	}
	updatedFileCache := make(map[int][]byte, len(groups))
	libraryCreatedAt := time.Now().UnixNano()
	if !j.done.IsZero() {
		libraryCreatedAt = j.done.UnixNano()
	}
	itemIndexes := make([]int, 0, len(groups))
	for itemIndex := range groups {
		itemIndexes = append(itemIndexes, itemIndex)
	}
	sort.Ints(itemIndexes)
	for _, itemIndex := range itemIndexes {
		group := groups[itemIndex]
		if len(group) == 0 {
			continue
		}
		delete(existingFileIndexes, itemIndex)
		rowSignature, err := json.Marshal(group)
		if err != nil {
			return err
		}
		updatedFileCache[itemIndex] = rowSignature
		if previous, exists := fileCache[itemIndex]; exists && bytes.Equal(previous, rowSignature) {
			continue
		}
		file := group[0]
		managedAvailable, publishedAvailable, thumbnailLocalAvailable := 0, 0, 0
		if file.ManagedAvailable {
			managedAvailable = 1
		}
		if file.PublishedAvailable {
			publishedAvailable = 1
		}
		if file.ThumbnailLocalAvailable {
			thumbnailLocalAvailable = 1
		}
		subtitleJSON := ""
		if file.Subtitle != nil {
			encoded, marshalErr := json.Marshal(file.Subtitle)
			if marshalErr != nil {
				return marshalErr
			}
			subtitleJSON = string(encoded)
		}
		chaptersJSON := ""
		if len(file.Chapters) > 0 {
			encoded, marshalErr := json.Marshal(file.Chapters)
			if marshalErr != nil {
				return marshalErr
			}
			chaptersJSON = string(encoded)
		}
		additionalFilesJSON := ""
		if len(group) > 1 {
			additional := make([]additionalStoredFile, 0, len(group)-1)
			for _, extra := range group[1:] {
				additional = append(additional, additionalStoredFile{File: extra, OutputPath: extra.OutputPath})
			}
			encoded, marshalErr := json.Marshal(additional)
			if marshalErr != nil {
				return marshalErr
			}
			additionalFilesJSON = string(encoded)
		}
		if _, err = tx.Exec(`INSERT INTO job_files(job_id,item_index,file_id,name,size,height,mime_type,title,author,duration_seconds,thumbnail_url,thumbnail_mime_type,thumbnail_local_available,publish_date,category,output_name,output_path,output_relative_path,managed_available,published_available,subtitle_json,subtitle_error,chapters_json,additional_files_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(job_id,item_index) DO UPDATE SET file_id=excluded.file_id,name=excluded.name,size=excluded.size,height=excluded.height,mime_type=excluded.mime_type,title=excluded.title,author=excluded.author,duration_seconds=excluded.duration_seconds,thumbnail_url=excluded.thumbnail_url,thumbnail_mime_type=excluded.thumbnail_mime_type,thumbnail_local_available=excluded.thumbnail_local_available,publish_date=excluded.publish_date,category=excluded.category,output_name=excluded.output_name,output_path=excluded.output_path,output_relative_path=excluded.output_relative_path,managed_available=excluded.managed_available,published_available=excluded.published_available,subtitle_json=excluded.subtitle_json,subtitle_error=excluded.subtitle_error,chapters_json=excluded.chapters_json,additional_files_json=excluded.additional_files_json
			WHERE job_files.file_id IS NOT excluded.file_id OR job_files.name IS NOT excluded.name OR job_files.size IS NOT excluded.size OR job_files.height IS NOT excluded.height OR job_files.mime_type IS NOT excluded.mime_type OR job_files.title IS NOT excluded.title OR job_files.author IS NOT excluded.author OR job_files.duration_seconds IS NOT excluded.duration_seconds OR job_files.thumbnail_url IS NOT excluded.thumbnail_url OR job_files.thumbnail_mime_type IS NOT excluded.thumbnail_mime_type OR job_files.thumbnail_local_available IS NOT excluded.thumbnail_local_available OR job_files.publish_date IS NOT excluded.publish_date OR job_files.category IS NOT excluded.category OR job_files.output_name IS NOT excluded.output_name OR job_files.output_path IS NOT excluded.output_path OR job_files.output_relative_path IS NOT excluded.output_relative_path OR job_files.managed_available IS NOT excluded.managed_available OR job_files.published_available IS NOT excluded.published_available OR job_files.subtitle_json IS NOT excluded.subtitle_json OR job_files.subtitle_error IS NOT excluded.subtitle_error OR job_files.chapters_json IS NOT excluded.chapters_json OR job_files.additional_files_json IS NOT excluded.additional_files_json`,
			j.ID, itemIndex, file.ID, file.Name, file.Size, file.Height, file.MimeType, file.Title, file.Author,
			file.DurationSeconds, file.ThumbnailURL, file.ThumbnailMimeType, thumbnailLocalAvailable, file.PublishDate, file.Category, file.OutputName, file.OutputPath, file.OutputRelativePath, managedAvailable, publishedAvailable, subtitleJSON, file.SubtitleError, chaptersJSON, additionalFilesJSON); err != nil {
			return err
		}
	}
	for index := range existingFileIndexes {
		if _, err := tx.Exec(`DELETE FROM job_files WHERE job_id=? AND item_index=?`, j.ID, index); err != nil {
			return err
		}
	}
	libraryState := *j
	libraryState.librarySignatures = s.libraryItemsCache[j.ID]
	if libraryState.librarySignatures == nil {
		libraryState.librarySignatures = j.librarySignatures
	}
	newLibrarySignatures, err := syncLibraryItems(tx, &libraryState, groups, libraryCreatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM library_sources WHERE source_job_id=?`, j.ID); err != nil {
		return err
	}
	if j.Status == "completed" || j.Status == "partial" || j.Status == "failed" || j.Status == "cancelled" {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO library_sources (
			source_job_id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,split_by_chapter,status,title,progress,current_item,completed_count,total_count,error,created_at,note,category,storage_mode,queue_position,search_text,download_location
		)
		SELECT id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,split_by_chapter,status,title,progress,current_item,completed_count,total_count,error,created_at,note,category,storage_mode,queue_position,
			COALESCE(title,'')||' '||COALESCE(url,'')||' '||COALESCE(category,'')||' '||COALESCE(audio_format,'')||' '||COALESCE(subtitle_language,''),output_location
		FROM jobs WHERE id=? AND EXISTS (SELECT 1 FROM library_items li WHERE li.source_job_id=jobs.id)`, j.ID); err != nil {
			return err
		}
	}
	failureCache := s.jobFailuresCache[j.ID]
	existingFailureIndexes := make(map[int]struct{}, len(failureCache))
	for index := range failureCache {
		existingFailureIndexes[index] = struct{}{}
	}
	if failureCache == nil {
		failureRows, err := tx.Query(`SELECT item_index FROM job_failures WHERE job_id=?`, j.ID)
		if err != nil {
			return err
		}
		for failureRows.Next() {
			var index int
			if err := failureRows.Scan(&index); err != nil {
				_ = failureRows.Close()
				return err
			}
			existingFailureIndexes[index] = struct{}{}
		}
		if err := failureRows.Err(); err != nil {
			_ = failureRows.Close()
			return err
		}
		if err := failureRows.Close(); err != nil {
			return err
		}
	}
	updatedFailureCache := make(map[int]string, len(j.Failures))
	for _, failure := range j.Failures {
		delete(existingFailureIndexes, failure.Index)
		updatedFailureCache[failure.Index] = failure.Error
		if previous, exists := failureCache[failure.Index]; exists && previous == failure.Error {
			continue
		}
		if _, err = tx.Exec(`INSERT INTO job_failures(job_id,item_index,error) VALUES(?,?,?) ON CONFLICT(job_id,item_index) DO UPDATE SET error=excluded.error WHERE job_failures.error IS NOT excluded.error`, j.ID, failure.Index, failure.Error); err != nil {
			return err
		}
	}
	for index := range existingFailureIndexes {
		if _, err := tx.Exec(`DELETE FROM job_failures WHERE job_id=? AND item_index=?`, j.ID, index); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.queueItemsReady {
		s.queueItemsCache[j.ID] = updatedQueueCache
	}
	s.jobFilesCache[j.ID] = updatedFileCache
	s.jobFailuresCache[j.ID] = updatedFailureCache
	s.libraryItemsCache[j.ID] = newLibrarySignatures
	j.librarySignatures = newLibrarySignatures
	return nil
}

func syncLibraryItems(tx *sql.Tx, j *jobState, groups map[int][]mediaFile, createdAt int64) (map[string][32]byte, error) {
	current := make(map[string][32]byte, len(j.Files))
	excludedRows, err := tx.Query(`SELECT file_id FROM library_item_exclusions WHERE source_job_id=?`, j.ID)
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]struct{})
	for excludedRows.Next() {
		var fileID string
		if err := excludedRows.Scan(&fileID); err != nil {
			_ = excludedRows.Close()
			return nil, err
		}
		excluded[fileID] = struct{}{}
	}
	if err := excludedRows.Err(); err != nil {
		_ = excludedRows.Close()
		return nil, err
	}
	if err := excludedRows.Close(); err != nil {
		return nil, err
	}
	for itemIndex, group := range groups {
		for _, file := range group {
			fileID, data, signature, err := prepareLibraryItem(j.ID, itemIndex, file)
			if err != nil {
				return nil, err
			}
			if _, isExcluded := excluded[fileID]; isExcluded {
				continue
			}
			current[fileID] = signature
			if prior, exists := j.librarySignatures[fileID]; exists && prior == signature {
				continue
			}
			if createdAt == 0 {
				createdAt = time.Now().UnixNano()
			}
			if _, err := tx.Exec(`INSERT INTO library_items(file_id,source_job_id,source_item_index,file_json,output_path,created_at)
				VALUES(?,?,?,?,?,?)
				ON CONFLICT(file_id) DO UPDATE SET source_job_id=excluded.source_job_id,source_item_index=excluded.source_item_index,file_json=excluded.file_json,output_path=excluded.output_path`,
				fileID, j.ID, itemIndex, string(data), file.OutputPath, createdAt); err != nil {
				return nil, err
			}
		}
	}

	var stale []string
	if j.librarySignatures == nil {
		rows, err := tx.Query(`SELECT file_id FROM library_items WHERE source_job_id=?`, j.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var fileID string
			if err := rows.Scan(&fileID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, exists := current[fileID]; !exists {
				stale = append(stale, fileID)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	} else {
		for fileID := range j.librarySignatures {
			if _, exists := current[fileID]; !exists {
				stale = append(stale, fileID)
			}
		}
	}
	for _, fileID := range stale {
		if _, err := tx.Exec(`DELETE FROM library_items WHERE file_id=? AND source_job_id=?`, fileID, j.ID); err != nil {
			return nil, err
		}
	}
	return current, nil
}

func (s *jobStore) saveQueueOrder(jobIDs []string) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixNano()
	for index, jobID := range jobIDs {
		result, err := tx.Exec(`UPDATE jobs SET queue_position=?,updated_at=? WHERE id=?`, index+1, now, jobID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return errors.New("queue order referenced an unknown job")
		}
	}
	return tx.Commit()
}

type downloadPart struct {
	Path              string
	CompletedBytes    int64
	ExpectedBytes     int64
	Itag              int
	SourceFingerprint string
	Method            string
}

func (s *jobStore) savePart(jobID string, itemIndex int, partKey string, part downloadPart) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO download_parts(job_id,item_index,part_key,path,completed_bytes,expected_bytes,itag,source_fingerprint,method,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,item_index,part_key) DO UPDATE SET path=excluded.path,
		completed_bytes=excluded.completed_bytes,expected_bytes=excluded.expected_bytes,itag=excluded.itag,
		source_fingerprint=excluded.source_fingerprint,method=excluded.method,updated_at=excluded.updated_at`,
		jobID, itemIndex, partKey, part.Path, part.CompletedBytes, part.ExpectedBytes, part.Itag, part.SourceFingerprint, part.Method, time.Now().UnixNano())
	return err
}

func (s *jobStore) loadPart(jobID string, itemIndex int, partKey string) (downloadPart, error) {
	var part downloadPart
	err := s.db.QueryRow(`SELECT path,completed_bytes,expected_bytes,itag,source_fingerprint,method
		FROM download_parts WHERE job_id=? AND item_index=? AND part_key=?`, jobID, itemIndex, partKey).Scan(
		&part.Path, &part.CompletedBytes, &part.ExpectedBytes, &part.Itag, &part.SourceFingerprint, &part.Method)
	return part, err
}

func (s *jobStore) deletePart(jobID string, itemIndex int, partKey string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM download_parts WHERE job_id=? AND item_index=? AND part_key=?`, jobID, itemIndex, partKey)
	return err
}

func (s *jobStore) deletePartsForJob(jobID string) error {
	if s == nil {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM download_parts WHERE job_id=?`, jobID)
	return err
}

func (s *jobStore) deleteJob(jobID string) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM library_sources WHERE source_job_id=?`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM library_items WHERE source_job_id=?`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM library_item_exclusions WHERE source_job_id=?`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM jobs WHERE id=?`, jobID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.releaseJobCaches(jobID)
	return nil
}

// deleteJobHistory removes only the finished work record and its job-scoped
// tables. Library rows and their compact source metadata have no jobs FK and
// deliberately survive this operation.
func (s *jobStore) deleteJobHistory(jobID string) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM jobs WHERE id=?`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM library_item_exclusions WHERE source_job_id=?`, jobID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.releaseJobCaches(jobID)
	return nil
}

func (s *jobStore) deleteLibraryItem(j *jobState, fileID string) error {
	if s == nil || j == nil || fileID == "" {
		return sql.ErrNoRows
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var itemIndex int
	if err := tx.QueryRow(`SELECT source_item_index FROM library_items WHERE source_job_id=? AND file_id=?`, j.ID, fileID).Scan(&itemIndex); err != nil {
		return err
	}
	groups := groupedJobFiles(j)
	group, ok := groups[itemIndex]
	if !ok || len(group) == 0 {
		return sql.ErrNoRows
	}
	found := false
	for _, file := range group {
		if file.ID == fileID {
			found = true
			break
		}
	}
	if !found {
		return sql.ErrNoRows
	}
	var historyExists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE id=?)`, j.ID).Scan(&historyExists); err != nil {
		return err
	}
	if historyExists != 0 {
		if _, err := tx.Exec(`INSERT INTO library_item_exclusions(source_job_id,file_id,removed_at) VALUES(?,?,?)
			ON CONFLICT(source_job_id,file_id) DO UPDATE SET removed_at=excluded.removed_at`, j.ID, fileID, time.Now().UnixNano()); err != nil {
			return err
		}
		primary := group[0]
		var subtitleJSON string
		if primary.Subtitle != nil {
			encoded, err := json.Marshal(primary.Subtitle)
			if err != nil {
				return err
			}
			subtitleJSON = string(encoded)
		}
		additional := make([]additionalStoredFile, 0, len(group)-1)
		for _, extra := range group[1:] {
			additional = append(additional, additionalStoredFile{File: extra, OutputPath: extra.OutputPath})
		}
		additionalJSON := ""
		if len(additional) > 0 {
			encoded, err := json.Marshal(additional)
			if err != nil {
				return err
			}
			additionalJSON = string(encoded)
		}
		result, err := tx.Exec(`UPDATE job_files SET managed_available=?,thumbnail_local_available=?,subtitle_json=?,additional_files_json=? WHERE job_id=? AND item_index=?`,
			primary.ManagedAvailable, primary.ThumbnailLocalAvailable, subtitleJSON, additionalJSON, j.ID, itemIndex)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return sql.ErrNoRows
		}
	}
	result, err := tx.Exec(`DELETE FROM library_items WHERE source_job_id=? AND file_id=?`, j.ID, fileID)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM library_sources WHERE source_job_id=? AND NOT EXISTS (SELECT 1 FROM library_items WHERE source_job_id=?)`, j.ID, j.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *jobStore) hasLibraryItem(jobID, fileID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM library_items WHERE source_job_id=? AND file_id=?)`, jobID, fileID).Scan(&exists)
	return exists != 0, err
}

func (s *jobStore) saveLibraryJob(j *jobState) error {
	if s == nil || j == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, file := range j.Files {
		fileID, data, _, err := prepareLibraryItem(j.ID, file.SourceItemIndex, file)
		if err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE library_items SET file_json=?,output_path=? WHERE file_id=? AND source_job_id=?`, string(data), file.OutputPath, fileID, j.ID)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			var excluded int
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM library_item_exclusions WHERE source_job_id=? AND file_id=?)`, j.ID, fileID).Scan(&excluded); err != nil {
				return err
			}
			if excluded != 0 {
				continue
			}
			return sql.ErrNoRows
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *jobStore) releaseJobCaches(jobID string) {
	s.queueItemsCacheMu.Lock()
	delete(s.queueItemsCache, jobID)
	delete(s.jobFilesCache, jobID)
	delete(s.jobFailuresCache, jobID)
	delete(s.libraryItemsCache, jobID)
	s.queueItemsCacheMu.Unlock()
}

func (s *jobStore) loadJobs(root string) ([]*storedJob, error) {
	return s.loadJobsFiltered(root, "", "all")
}

func (s *jobStore) loadActiveJobs(root string) ([]*storedJob, error) {
	return s.loadJobsFiltered(root, "", "active")
}

func (s *jobStore) loadJob(root, jobID string) (*storedJob, error) {
	jobs, err := s.loadJobsFiltered(root, jobID, "one")
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return jobs[0], nil
}

func (s *jobStore) loadRetentionCandidateIDs(now time.Time, retention time.Duration) ([]string, error) {
	query := `SELECT j.id FROM jobs j WHERE j.status IN ('completed','partial','failed','cancelled') AND j.done_at IS NOT NULL`
	var cutoff time.Time
	if retention > 0 {
		cutoff = now.Add(-retention)
		query += ` AND (EXISTS (SELECT 1 FROM job_files f WHERE f.job_id=j.id) OR j.status IN ('failed','cancelled'))`
	} else {
		cutoff = now.Add(-failedScratchRetention)
		query += ` AND j.status IN ('failed','cancelled') AND NOT EXISTS (SELECT 1 FROM job_files f WHERE f.job_id=j.id)`
	}
	query += ` AND j.done_at<=? ORDER BY j.done_at`
	rows, err := s.db.Query(query, cutoff.UnixNano())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *jobStore) loadJobsFiltered(root, jobID, selection string) ([]*storedJob, error) {
	currentSettings, err := s.loadAppSettings()
	if err != nil {
		return nil, err
	}
	var queueItemsByJob map[string][]queueItem
	if s.queueItemsReady {
		switch selection {
		case "active":
			queueItemsByJob, err = s.loadQueueItemsQuery(`WHERE job_id IN (SELECT id FROM jobs WHERE status IN ('queued','downloading','processing','paused'))`, nil)
		case "one":
			queueItemsByJob, err = s.loadQueueItemsQuery(`WHERE job_id=?`, []any{jobID})
		default:
			queueItemsByJob, err = s.loadQueueItemsQuery("", nil)
		}
	} else {
		queueItemsByJob, err = s.loadLegacyQueueItems()
	}
	if err != nil {
		return nil, err
	}
	query := `SELECT id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,split_by_chapter,status,title,progress,current_item,completed_count,total_count,error,created_at,note,dir,cancel_requested,done_at,output_location,naming_pattern,subfolder_sorting,category,storage_mode,queue_position,output_file_mode,output_folder_mode FROM jobs`
	args := []any{}
	switch selection {
	case "active":
		query += ` WHERE status IN ('queued','downloading','processing','paused')`
	case "one":
		query += ` WHERE id=?`
		args = append(args, jobID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	type jobRow struct {
		job             Job
		dir             string
		cancelRequested int
		doneAt          sql.NullInt64
	}
	var savedRows []jobRow
	for rows.Next() {
		var j Job
		var progress sql.NullFloat64
		var totalCount sql.NullInt64
		var dir string
		var cancelRequested int
		var doneAt sql.NullInt64
		if err := rows.Scan(&j.ID, &j.URL, &j.Kind, &j.Quality, &j.VideoStrategy, &j.Allow360pFallback, &j.MediaType, &j.AudioFormat, &j.AudioBitrate, &j.SubtitleLanguage, &j.SubtitleFormat, &j.SplitByChapter, &j.Status, &j.Title, &progress, &j.CurrentItem,
			&j.CompletedCount, &totalCount, &j.Error, &j.CreatedAt, &j.Note, &dir, &cancelRequested, &doneAt,
			&j.DownloadLocation, &j.NamingPattern, &j.SubfolderSorting, &j.Category, &j.StorageMode, &j.QueuePosition, &j.OutputFileMode, &j.OutputFolderMode); err != nil {
			return nil, err
		}
		j.Items = queueItemsByJob[j.ID]
		if j.Items == nil {
			j.Items = []queueItem{}
		}
		if j.DownloadLocation == "" {
			j.DownloadLocation = currentSettings.DownloadLocation
		}
		if j.NamingPattern == "" {
			j.NamingPattern = currentSettings.NamingPattern
		}
		if j.SubfolderSorting == "" {
			j.SubfolderSorting = currentSettings.SubfolderSorting
		}
		if j.Category == "" {
			j.Category = currentSettings.DefaultCategory
		}
		if j.StorageMode == "" {
			j.StorageMode = currentSettings.StorageMode
		}
		if j.OutputFileMode == "" {
			j.OutputFileMode = currentSettings.OutputFileMode
		}
		if j.OutputFolderMode == "" {
			j.OutputFolderMode = currentSettings.OutputFolderMode
		}
		if j.MediaType == "audio" {
			j.VideoStrategy = ""
			if j.AudioFormat == "" {
				j.AudioFormat = "mp3"
			}
		} else if j.VideoStrategy == "" {
			j.VideoStrategy = "best"
		}
		if progress.Valid {
			value := progress.Float64
			j.Progress = &value
		}
		if totalCount.Valid {
			value := int(totalCount.Int64)
			j.TotalCount = &value
		}
		savedRows = append(savedRows, jobRow{job: j, dir: dir, cancelRequested: cancelRequested, doneAt: doneAt})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var result []*storedJob
	for _, saved := range savedRows {
		j := saved.job
		dir := saved.dir
		if filepath.Base(j.ID) != j.ID || j.ID == "." || j.ID == ".." {
			return nil, errors.New("state database contains an unsafe job id")
		}
		loaded := &storedJob{job: j, dir: filepath.Join(root, j.ID), items: map[int][]mediaFile{}, cancelled: saved.cancelRequested != 0}
		loaded.job.Files = []mediaFile{}
		loaded.job.Failures = []itemFailure{}
		if saved.doneAt.Valid {
			loaded.done = time.Unix(0, saved.doneAt.Int64)
		}
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != filepath.Join(root, j.ID) {
			return nil, errors.New("state database contains an unsafe job directory")
		}
		if j.Status != "paused" && !terminal(j.Status) {
			loaded.resuming = true
			loaded.cancelled = false
			loaded.job.Status = "queued"
			loaded.job.Error = ""
			loaded.job.Progress = nil
			loaded.done = time.Time{}
			if err := ensureJobDir(loaded.dir); err != nil {
				return nil, err
			}
		}
		files, err := s.db.Query(`SELECT item_index,file_id,name,size,height,mime_type,title,author,duration_seconds,thumbnail_url,thumbnail_mime_type,thumbnail_local_available,publish_date,category,output_name,output_path,output_relative_path,managed_available,published_available,subtitle_json,subtitle_error,chapters_json,additional_files_json FROM job_files WHERE job_id=? ORDER BY item_index`, j.ID)
		if err != nil {
			return nil, err
		}
		for files.Next() {
			var index int
			var file mediaFile
			var managedAvailable, publishedAvailable, thumbnailLocalAvailable int
			var subtitleJSON, chaptersJSON, additionalFilesJSON string
			if err := files.Scan(&index, &file.ID, &file.Name, &file.Size, &file.Height, &file.MimeType, &file.Title, &file.Author,
				&file.DurationSeconds, &file.ThumbnailURL, &file.ThumbnailMimeType, &thumbnailLocalAvailable, &file.PublishDate, &file.Category, &file.OutputName, &file.OutputPath, &file.OutputRelativePath, &managedAvailable, &publishedAvailable, &subtitleJSON, &file.SubtitleError, &chaptersJSON, &additionalFilesJSON); err != nil {
				_ = files.Close()
				return nil, err
			}
			restoreStoredFileMediaType(&file, j.MediaType)
			file.ManagedAvailable = managedAvailable != 0
			file.PublishedAvailable = publishedAvailable != 0
			file.ThumbnailLocalAvailable = thumbnailLocalAvailable != 0
			if subtitleJSON != "" {
				var subtitle subtitleFile
				if err := json.Unmarshal([]byte(subtitleJSON), &subtitle); err != nil {
					_ = files.Close()
					return nil, fmt.Errorf("decode saved caption sidecar: %w", err)
				}
				file.Subtitle = &subtitle
			}
			if chaptersJSON != "" {
				if err := json.Unmarshal([]byte(chaptersJSON), &file.Chapters); err != nil {
					_ = files.Close()
					return nil, fmt.Errorf("decode saved chapters: %w", err)
				}
			}
			loaded.items[index] = []mediaFile{file}
			loaded.job.Files = append(loaded.job.Files, file)
			if additionalFilesJSON != "" {
				var additional []additionalStoredFile
				if err := json.Unmarshal([]byte(additionalFilesJSON), &additional); err != nil {
					_ = files.Close()
					return nil, fmt.Errorf("decode saved chapter files: %w", err)
				}
				for _, stored := range additional {
					restoreStoredFileMediaType(&stored.File, j.MediaType)
					stored.File.OutputPath = stored.OutputPath
					loaded.items[index] = append(loaded.items[index], stored.File)
					loaded.job.Files = append(loaded.job.Files, stored.File)
				}
			}
		}
		if err := files.Err(); err != nil {
			_ = files.Close()
			return nil, err
		}
		_ = files.Close()
		failures, err := s.db.Query(`SELECT item_index,error FROM job_failures WHERE job_id=? ORDER BY item_index`, j.ID)
		if err != nil {
			return nil, err
		}
		for failures.Next() {
			var failure itemFailure
			if err := failures.Scan(&failure.Index, &failure.Error); err != nil {
				_ = failures.Close()
				return nil, err
			}
			loaded.job.Failures = append(loaded.job.Failures, failure)
		}
		if err := failures.Err(); err != nil {
			_ = failures.Close()
			return nil, err
		}
		_ = failures.Close()
		if loaded.resuming {
			if err := s.normalizeResumingJob(loaded); err != nil {
				return nil, err
			}
		}
		result = append(result, loaded)
	}
	return result, nil
}

func restoreStoredFileMediaType(file *mediaFile, jobMediaType string) {
	if file != nil && file.MediaType == "" && jobMediaType == "audio" {
		file.MediaType = "audio"
	}
}

// loadLibraryJobs builds the Library read model from durable finalized-file
// rows. It deliberately does not read job_files or require an in-memory job.
func (s *jobStore) loadLibraryJobs() ([]Job, error) {
	return s.loadLibraryJobsForIDs(nil)
}

func (s *jobStore) loadLibraryJobsForIDs(jobIDs []string) ([]Job, error) {
	settings, err := s.loadAppSettings()
	if err != nil {
		return nil, err
	}
	query := `SELECT j.source_job_id,j.url,j.kind,j.quality,j.video_strategy,j.allow_360p_fallback,j.media_type,j.audio_format,j.audio_bitrate,j.subtitle_language,j.subtitle_format,j.split_by_chapter,j.status,j.title,j.progress,j.current_item,j.completed_count,j.total_count,j.error,j.created_at,j.note,j.category,j.storage_mode,j.queue_position,j.download_location,li.file_json,li.output_path
		FROM library_items li JOIN library_sources j ON j.source_job_id=li.source_job_id
		WHERE j.status IN ('completed','partial','failed','cancelled')
		`
	args := []any{}
	if jobIDs != nil {
		if len(jobIDs) == 0 {
			return []Job{}, nil
		}
		placeholders := make([]string, len(jobIDs))
		for i, id := range jobIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query += " AND j.source_job_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY j.created_at DESC,j.source_job_id DESC,li.source_item_index ASC,li.file_id ASC"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var result []Job
	indexes := make(map[string]int)
	for rows.Next() {
		var j Job
		var progress sql.NullFloat64
		var totalCount sql.NullInt64
		var downloadLocation, fileJSON, outputPath string
		if err := rows.Scan(&j.ID, &j.URL, &j.Kind, &j.Quality, &j.VideoStrategy, &j.Allow360pFallback, &j.MediaType, &j.AudioFormat, &j.AudioBitrate, &j.SubtitleLanguage, &j.SubtitleFormat, &j.SplitByChapter, &j.Status, &j.Title, &progress, &j.CurrentItem,
			&j.CompletedCount, &totalCount, &j.Error, &j.CreatedAt, &j.Note, &j.Category, &j.StorageMode, &j.QueuePosition, &downloadLocation, &fileJSON, &outputPath); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if progress.Valid {
			value := progress.Float64
			j.Progress = &value
		}
		if totalCount.Valid {
			value := int(totalCount.Int64)
			j.TotalCount = &value
		}
		j.Items = []queueItem{}
		j.DownloadLocation = downloadLocation
		if j.MediaType == "" {
			j.MediaType = "video"
		}
		if j.MediaType == "audio" {
			if j.AudioFormat == "" {
				j.AudioFormat = "mp3"
			}
		} else if j.VideoStrategy == "" {
			j.VideoStrategy = "best"
		}
		if j.Category == "" {
			j.Category = settings.DefaultCategory
		}
		if j.StorageMode == "" {
			j.StorageMode = settings.StorageMode
		}
		if index, exists := indexes[j.ID]; exists {
			j = result[index]
		} else {
			j.Files = []mediaFile{}
			indexes[j.ID] = len(result)
			result = append(result, j)
		}
		var file mediaFile
		if err := json.Unmarshal([]byte(fileJSON), &file); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("decode saved Library file: %w", err)
		}
		file.OutputPath = outputPath
		result[indexes[j.ID]].Files = append(result[indexes[j.ID]].Files, file)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []Job{}
	}
	return result, nil
}

func (s *jobStore) loadLibraryJob(root, jobID string) (*storedJob, error) {
	if filepath.Base(jobID) != jobID || jobID == "." || jobID == ".." {
		return nil, errors.New("state database contains an unsafe Library source id")
	}
	jobs, err := s.loadLibraryJobsForIDs([]string{jobID})
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	job := jobs[0]
	if !terminal(job.Status) || !filepath.IsAbs(job.DownloadLocation) {
		return nil, nil
	}
	loaded := &storedJob{
		job:   job,
		dir:   filepath.Join(root, jobID),
		done:  time.Now(),
		items: map[int][]mediaFile{},
	}
	loaded.job.Failures = []itemFailure{}
	loaded.job.Items = []queueItem{}
	for _, file := range loaded.job.Files {
		index := file.SourceItemIndex
		if index < 1 {
			index = 1
		}
		loaded.items[index] = append(loaded.items[index], file)
	}
	return loaded, nil
}

func (s *jobStore) normalizeResumingJob(loaded *storedJob) error {
	validFiles := make([]mediaFile, 0, len(loaded.items))
	indexes := make([]int, 0, len(loaded.items))
	for index := range loaded.items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		group := loaded.items[index]
		validGroup := len(group) > 0
		for _, file := range group {
			info, err := openFinal(loaded.dir, file.Name)
			if err != nil {
				validGroup = false
				break
			}
			stat, statErr := info.Stat()
			_ = info.Close()
			if statErr != nil || stat.Size() != file.Size {
				validGroup = false
				break
			}
		}
		if !validGroup {
			delete(loaded.items, index)
			continue
		}
		validFiles = append(validFiles, group...)
	}
	loaded.job.Files = validFiles
	loaded.job.CompletedCount = len(loaded.items)
	if err := s.saveJob(&jobState{Job: loaded.job, dir: loaded.dir, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}); err != nil {
		return err
	}
	return nil
}

func fileIndexes(items map[int][]mediaFile) map[int]mediaFile {
	result := make(map[int]mediaFile, len(items))
	for index, files := range items {
		if len(files) > 0 {
			result[index] = files[0]
		}
	}
	return result
}

func fileGroupIndexes(items map[int][]mediaFile) map[int][]mediaFile {
	result := make(map[int][]mediaFile, len(items))
	for index, files := range items {
		result[index] = append([]mediaFile(nil), files...)
	}
	return result
}

func ensureJobDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create job directory: %w", err)
	}
	return nil
}
