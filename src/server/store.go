// store.go persists jobs, finalized-file metadata, preferences, and resumable
// transfer state in the pure-Go SQLite database under DATA_DIR.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

// jobStore is the durable source of truth for job history, completed items,
// preferences, and resume state; live queue and cancellation handles stay in RAM.
type jobStore struct {
	db *sql.DB
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
	items     map[int]mediaFile
	resuming  bool
	cancelled bool
}

// openJobStore opens DATA_DIR/state.db, applies SQLite runtime pragmas, creates
// missing tables, and runs schema migrations before any jobs are loaded.
func openJobStore(root string) (*jobStore, error) {
	db, err := sql.Open("sqlite", filepath.Join(root, "state.db"))
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &jobStore{db: db}
	for _, statement := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
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
	if err := store.migrateV2(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV3(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV4(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV5(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV6(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV7(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV8(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV9(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV10(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV11(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV12(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV13(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV14(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV15(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV16(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV17(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	if err := store.migrateV18(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate state database: %w", err)
	}
	return store, nil
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

func (s *jobStore) saveJob(j *jobState) error {
	if s == nil {
		return nil
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
	queueItems, err := json.Marshal(j.Items)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(`INSERT INTO jobs
		(id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,status,title,progress,current_item,completed_count,total_count,error,created_at,note,dir,cancel_requested,done_at,updated_at,queue_items,output_location,naming_pattern,subfolder_sorting,category,storage_mode,queue_position,output_file_mode,output_folder_mode)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET url=excluded.url,kind=excluded.kind,quality=excluded.quality,video_strategy=excluded.video_strategy,
		allow_360p_fallback=excluded.allow_360p_fallback,
		media_type=excluded.media_type,audio_format=excluded.audio_format,audio_bitrate=excluded.audio_bitrate,
		subtitle_language=excluded.subtitle_language,subtitle_format=excluded.subtitle_format,
		status=excluded.status,title=excluded.title,progress=excluded.progress,current_item=excluded.current_item,
		completed_count=excluded.completed_count,total_count=excluded.total_count,error=excluded.error,
		created_at=excluded.created_at,note=excluded.note,dir=excluded.dir,cancel_requested=excluded.cancel_requested,
		done_at=excluded.done_at,updated_at=excluded.updated_at,queue_items=excluded.queue_items,
		output_location=excluded.output_location,naming_pattern=excluded.naming_pattern,subfolder_sorting=excluded.subfolder_sorting,category=excluded.category,storage_mode=excluded.storage_mode,queue_position=excluded.queue_position,output_file_mode=excluded.output_file_mode,output_folder_mode=excluded.output_folder_mode`,
		j.ID, j.URL, j.Kind, j.Quality, j.VideoStrategy, j.Allow360pFallback, j.MediaType, j.AudioFormat, j.AudioBitrate, j.SubtitleLanguage, j.SubtitleFormat, j.Status, j.Title, progress, j.CurrentItem, j.CompletedCount, total,
		j.Error, j.CreatedAt, j.Note, j.dir, cancelRequested, done, time.Now().UnixNano(), string(queueItems),
		j.DownloadLocation, j.NamingPattern, j.SubfolderSorting, j.Category, j.StorageMode, j.QueuePosition, j.OutputFileMode, j.OutputFolderMode)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM job_files WHERE job_id=?`, j.ID); err != nil {
		return err
	}
	for index, file := range j.Files {
		itemIndex := index + 1
		for index, saved := range j.fileItems {
			if saved.ID == file.ID {
				itemIndex = index
				break
			}
		}
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
		if _, err = tx.Exec(`INSERT INTO job_files(job_id,item_index,file_id,name,size,height,mime_type,title,author,duration_seconds,thumbnail_url,thumbnail_mime_type,thumbnail_local_available,publish_date,category,output_name,output_path,output_relative_path,managed_available,published_available,subtitle_json,subtitle_error,chapters_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			j.ID, itemIndex, file.ID, file.Name, file.Size, file.Height, file.MimeType, file.Title, file.Author,
			file.DurationSeconds, file.ThumbnailURL, file.ThumbnailMimeType, thumbnailLocalAvailable, file.PublishDate, file.Category, file.OutputName, file.OutputPath, file.OutputRelativePath, managedAvailable, publishedAvailable, subtitleJSON, file.SubtitleError, chaptersJSON); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM job_failures WHERE job_id=?`, j.ID); err != nil {
		return err
	}
	for _, failure := range j.Failures {
		if _, err = tx.Exec(`INSERT INTO job_failures(job_id,item_index,error) VALUES(?,?,?)`, j.ID, failure.Index, failure.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, jobID)
	return err
}

func (s *jobStore) loadJobs(root string) ([]*storedJob, error) {
	currentSettings, err := s.loadAppSettings()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,status,title,progress,current_item,completed_count,total_count,error,created_at,note,dir,cancel_requested,done_at,queue_items,output_location,naming_pattern,subfolder_sorting,category,storage_mode,queue_position,output_file_mode,output_folder_mode FROM jobs ORDER BY created_at ASC`)
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
		var queueItems string
		if err := rows.Scan(&j.ID, &j.URL, &j.Kind, &j.Quality, &j.VideoStrategy, &j.Allow360pFallback, &j.MediaType, &j.AudioFormat, &j.AudioBitrate, &j.SubtitleLanguage, &j.SubtitleFormat, &j.Status, &j.Title, &progress, &j.CurrentItem,
			&j.CompletedCount, &totalCount, &j.Error, &j.CreatedAt, &j.Note, &dir, &cancelRequested, &doneAt, &queueItems,
			&j.DownloadLocation, &j.NamingPattern, &j.SubfolderSorting, &j.Category, &j.StorageMode, &j.QueuePosition, &j.OutputFileMode, &j.OutputFolderMode); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(queueItems), &j.Items); err != nil {
			return nil, fmt.Errorf("decode saved queue entries: %w", err)
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
		loaded := &storedJob{job: j, dir: filepath.Join(root, j.ID), items: map[int]mediaFile{}, cancelled: saved.cancelRequested != 0}
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
		files, err := s.db.Query(`SELECT item_index,file_id,name,size,height,mime_type,title,author,duration_seconds,thumbnail_url,thumbnail_mime_type,thumbnail_local_available,publish_date,category,output_name,output_path,output_relative_path,managed_available,published_available,subtitle_json,subtitle_error,chapters_json FROM job_files WHERE job_id=? ORDER BY item_index`, j.ID)
		if err != nil {
			return nil, err
		}
		for files.Next() {
			var index int
			var file mediaFile
			var managedAvailable, publishedAvailable, thumbnailLocalAvailable int
			var subtitleJSON, chaptersJSON string
			if err := files.Scan(&index, &file.ID, &file.Name, &file.Size, &file.Height, &file.MimeType, &file.Title, &file.Author,
				&file.DurationSeconds, &file.ThumbnailURL, &file.ThumbnailMimeType, &thumbnailLocalAvailable, &file.PublishDate, &file.Category, &file.OutputName, &file.OutputPath, &file.OutputRelativePath, &managedAvailable, &publishedAvailable, &subtitleJSON, &file.SubtitleError, &chaptersJSON); err != nil {
				_ = files.Close()
				return nil, err
			}
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
			loaded.items[index] = file
			loaded.job.Files = append(loaded.job.Files, file)
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

func (s *jobStore) normalizeResumingJob(loaded *storedJob) error {
	validFiles := make([]mediaFile, 0, len(loaded.items))
	indexes := make([]int, 0, len(loaded.items))
	for index := range loaded.items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		file := loaded.items[index]
		info, err := openFinal(loaded.dir, file.Name)
		if err != nil {
			delete(loaded.items, index)
			continue
		}
		stat, statErr := info.Stat()
		_ = info.Close()
		if statErr != nil || stat.Size() != file.Size {
			delete(loaded.items, index)
			continue
		}
		validFiles = append(validFiles, file)
	}
	loaded.job.Files = validFiles
	loaded.job.CompletedCount = len(validFiles)
	if err := s.saveJob(&jobState{Job: loaded.job, dir: loaded.dir, fileItems: fileIndexes(loaded.items)}); err != nil {
		return err
	}
	return nil
}

func fileIndexes(items map[int]mediaFile) map[int]mediaFile {
	result := make(map[int]mediaFile, len(items))
	for index, file := range items {
		result[index] = file
	}
	return result
}

func ensureJobDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create job directory: %w", err)
	}
	return nil
}
