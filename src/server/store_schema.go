package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// Every supported database version retains these original jobs columns.
// quick_check detects damaged SQLite pages, but not an incomplete app schema.
// Check before initialization or migration can change the existing database.
func checkJobSchema(db *sql.DB) error {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil // Fresh databases are initialized by openJobStore.
	}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('jobs')`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		columns[strings.ToLower(name)] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var missing []string
	for _, name := range []string{
		"id", "url", "kind", "quality", "status", "title", "progress",
		"current_item", "completed_count", "total_count", "error", "created_at",
		"note", "dir", "cancel_requested", "done_at", "updated_at",
	} {
		if !columns[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("incompatible state database: jobs is missing required columns (%s); no schema migrations were applied. Preserve this data directory and restore a known-good backup, or choose a new private DATA_DIR to start fresh", strings.Join(missing, ", "))
	}
	return nil
}
