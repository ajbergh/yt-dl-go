package main

import (
	"fmt"
)

type migration struct {
	version    int
	statements []string
}

var versionOneSchema = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`,
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
}

// legacyConfigSchema is present in historical v1 databases. Runtime bootstrap
// no longer creates this write-only table; migration v29 removes it from old DBs.
const legacyConfigSchema = `CREATE TABLE config (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at INTEGER NOT NULL
)`

// transactionalMigrations contains the simple schema-only upgrades. Data
// transformations and table rebuilds remain in their dedicated migrations.
var transactionalMigrations = []migration{
	{version: 2, statements: []string{
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
	}},
	{version: 3, statements: []string{
		`ALTER TABLE jobs ADD COLUMN media_type TEXT NOT NULL DEFAULT 'video'`,
		`ALTER TABLE jobs ADD COLUMN audio_bitrate TEXT NOT NULL DEFAULT '192k'`,
		`ALTER TABLE app_settings ADD COLUMN max_concurrent_downloads INTEGER NOT NULL DEFAULT 3`,
	}},
	{version: 4, statements: []string{
		`ALTER TABLE jobs ADD COLUMN queue_items TEXT NOT NULL DEFAULT '[]'`,
	}},
	{version: 5, statements: []string{
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
	}},
	{version: 6, statements: []string{
		`ALTER TABLE job_files ADD COLUMN managed_available INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE job_files ADD COLUMN published_available INTEGER NOT NULL DEFAULT 0`,
		`UPDATE job_files SET published_available = CASE WHEN output_path <> '' THEN 1 ELSE 0 END`,
	}},
	{version: 7, statements: []string{
		`ALTER TABLE app_settings ADD COLUMN storage_mode TEXT NOT NULL DEFAULT 'managed-published'`,
		`ALTER TABLE jobs ADD COLUMN storage_mode TEXT NOT NULL DEFAULT 'managed-published'`,
	}},
	{version: 8, statements: []string{
		`ALTER TABLE job_files ADD COLUMN thumbnail_mime_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN thumbnail_local_available INTEGER NOT NULL DEFAULT 0`,
	}},
	{version: 9, statements: []string{
		`ALTER TABLE jobs ADD COLUMN audio_format TEXT NOT NULL DEFAULT ''`,
		`UPDATE jobs SET audio_format='mp3' WHERE media_type='audio' AND audio_format=''`,
	}},
	{version: 10, statements: []string{
		`ALTER TABLE jobs ADD COLUMN queue_position INTEGER NOT NULL DEFAULT 0`,
		`UPDATE jobs SET queue_position=rowid WHERE queue_position=0`,
	}},
	{version: 11, statements: []string{
		`ALTER TABLE app_settings ADD COLUMN bandwidth_limit_bytes_per_sec INTEGER NOT NULL DEFAULT 0`,
	}},
	{version: 12, statements: []string{
		`ALTER TABLE app_settings ADD COLUMN notifications_enabled INTEGER NOT NULL DEFAULT 0`,
	}},
	{version: 13, statements: []string{
		`ALTER TABLE jobs ADD COLUMN subtitle_language TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN subtitle_format TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN subtitle_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE job_files ADD COLUMN subtitle_error TEXT NOT NULL DEFAULT ''`,
	}},
	{version: 14, statements: []string{
		`ALTER TABLE jobs ADD COLUMN video_strategy TEXT NOT NULL DEFAULT 'best'`,
		`ALTER TABLE app_settings ADD COLUMN default_video_strategy TEXT NOT NULL DEFAULT 'best'`,
	}},
	{version: 15, statements: []string{
		`ALTER TABLE jobs ADD COLUMN allow_360p_fallback INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE app_settings ADD COLUMN allow_360p_fallback INTEGER NOT NULL DEFAULT 0`,
	}},
}

func (s *jobStore) applyMigration(migration migration) error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= migration.version {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range migration.statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migration v%d: %w", migration.version, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, migration.version); err != nil {
		return fmt.Errorf("record migration v%d: %w", migration.version, err)
	}
	return tx.Commit()
}
