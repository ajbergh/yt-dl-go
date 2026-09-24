package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func legacyDataAvailable(legacyRoot, dataRoot string) bool {
	if samePath(legacyRoot, dataRoot) || pathContains(legacyRoot, dataRoot) || pathContains(dataRoot, legacyRoot) {
		return false
	}
	rootInfo, err := os.Lstat(legacyRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	info, err := os.Lstat(filepath.Join(legacyRoot, "state.db"))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return migrationTargetIsEmpty(dataRoot)
}

// migrateLegacyData copies the old working-directory data tree into the new
// per-user location. The source remains intact as a rollback copy.
func migrateLegacyData(legacyRoot, dataRoot string) (err error) {
	legacyRoot, err = filepath.Abs(legacyRoot)
	if err != nil {
		return errors.New("cannot resolve legacy data directory")
	}
	dataRoot, err = filepath.Abs(dataRoot)
	if err != nil {
		return errors.New("cannot resolve destination data directory")
	}
	canonicalLegacyRoot, err := canonicalizeParentPath(legacyRoot)
	if err != nil {
		return fmt.Errorf("resolve legacy data directory: %w", err)
	}
	canonicalDataRoot, err := canonicalizeParentPath(dataRoot)
	if err != nil {
		return fmt.Errorf("resolve destination data directory: %w", err)
	}
	if samePath(canonicalLegacyRoot, canonicalDataRoot) || pathContains(canonicalLegacyRoot, canonicalDataRoot) || pathContains(canonicalDataRoot, canonicalLegacyRoot) {
		return errors.New("legacy and destination data directories must be different")
	}
	legacyInfo, err := os.Lstat(legacyRoot)
	if err != nil || !legacyInfo.IsDir() || legacyInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy data directory must be a real directory")
	}
	legacyDB := filepath.Join(legacyRoot, "state.db")
	dbInfo, err := os.Lstat(legacyDB)
	if err != nil || !dbInfo.Mode().IsRegular() || dbInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy state.db was not found as a regular file")
	}
	if !migrationTargetIsEmpty(dataRoot) {
		return errors.New("destination data directory already contains user data; migration will not overwrite it")
	}
	parent := filepath.Dir(dataRoot)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create per-user data parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".yt-dl-go-migration-")
	if err != nil {
		return fmt.Errorf("create migration staging directory: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := copyLegacyFiles(legacyRoot, stage); err != nil {
		return err
	}
	if err := snapshotLegacyDatabase(legacyDB, filepath.Join(stage, "state.db")); err != nil {
		return err
	}
	if err := remapMigratedPaths(filepath.Join(stage, "state.db"), legacyRoot, dataRoot); err != nil {
		return err
	}

	backup := ""
	if _, err := os.Lstat(dataRoot); err == nil {
		backup = dataRoot + ".before-legacy-migration-" + randomID(4)
		if err := os.Rename(dataRoot, backup); err != nil {
			return fmt.Errorf("preserve the empty destination directory: %w", err)
		}
	}
	if err := os.Rename(stage, dataRoot); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, dataRoot); restoreErr != nil {
				return fmt.Errorf("install migrated data: %v; restore destination backup %s: %w", err, backup, restoreErr)
			}
		}
		return fmt.Errorf("install migrated data: %w", err)
	}
	keepStage = true
	return nil
}

func migrationTargetIsEmpty(path string) bool {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != "state.db" && entry.Name() != "state.db-wal" && entry.Name() != "state.db-shm" {
			return false
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	if len(entries) == 0 {
		return true
	}
	dbPath := filepath.Join(path, "state.db")
	dbInfo, err := os.Lstat(dbPath)
	if err != nil || !dbInfo.Mode().IsRegular() || dbInfo.Mode()&os.ModeSymlink != 0 {
		return false
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, table := range []string{"jobs", "queue_items", "job_files", "job_failures", "library_items", "library_sources", "download_parts"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			return false
		}
	}
	return true
}

func copyLegacyFiles(sourceRoot, destinationRoot string) error {
	return filepath.WalkDir(sourceRoot, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, source)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if relative == "state.db" || relative == "state.db-wal" || relative == "state.db-shm" || relative == "state.db-journal" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("legacy data contains a symbolic link at %s", relative)
		}
		destination := filepath.Join(destinationRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("legacy data contains a non-regular file at %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
}

func snapshotLegacyDatabase(source, destination string) error {
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return fmt.Errorf("open legacy state database: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return fmt.Errorf("open legacy state database: %w", err)
	}
	quotedDestination := strings.ReplaceAll(destination, "'", "''")
	if _, err := db.Exec(`VACUUM INTO '` + quotedDestination + `'`); err != nil {
		return fmt.Errorf("snapshot legacy state database: %w", err)
	}
	if err := os.Chmod(destination, 0600); err != nil {
		return fmt.Errorf("restrict migrated database permissions: %w", err)
	}
	return nil
}

func remapMigratedPaths(databasePath, legacyRoot, dataRoot string) error {
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	jobRows, err := tx.Query(`SELECT id,dir FROM jobs`)
	if err != nil {
		return err
	}
	type jobDir struct{ id, path string }
	var dirs []jobDir
	for jobRows.Next() {
		var value jobDir
		if err := jobRows.Scan(&value.id, &value.path); err != nil {
			_ = jobRows.Close()
			return err
		}
		dirs = append(dirs, value)
	}
	if err := jobRows.Err(); err != nil {
		_ = jobRows.Close()
		return err
	}
	if err := jobRows.Close(); err != nil {
		return err
	}
	for _, value := range dirs {
		if mapped, ok := remapDataPath(legacyRoot, dataRoot, value.path); ok {
			if _, err := tx.Exec(`UPDATE jobs SET dir=? WHERE id=?`, mapped, value.id); err != nil {
				return err
			}
		}
	}
	// Use rowid so path rebasing works with both the original download_parts
	// schema (before v16 added part_key) and current multi-part schemas. This
	// command runs before normal server startup, where schema migrations happen.
	partRows, err := tx.Query(`SELECT rowid,path FROM download_parts`)
	if err != nil {
		return err
	}
	type partPath struct {
		rowID int64
		path  string
	}
	var parts []partPath
	for partRows.Next() {
		var value partPath
		if err := partRows.Scan(&value.rowID, &value.path); err != nil {
			_ = partRows.Close()
			return err
		}
		parts = append(parts, value)
	}
	if err := partRows.Err(); err != nil {
		_ = partRows.Close()
		return err
	}
	if err := partRows.Close(); err != nil {
		return err
	}
	for _, value := range parts {
		if mapped, ok := remapDataPath(legacyRoot, dataRoot, value.path); ok {
			if _, err := tx.Exec(`UPDATE download_parts SET path=? WHERE rowid=?`, mapped, value.rowID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func remapDataPath(oldRoot, newRoot, value string) (string, bool) {
	if value == "" {
		return "", false
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(oldRoot, path)
	}
	relative, err := filepath.Rel(oldRoot, filepath.Clean(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.Join(newRoot, relative), true
}

func samePath(left, right string) bool {
	left, _ = filepath.Abs(left)
	right, _ = filepath.Abs(right)
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathContains(root, path string) bool {
	root, _ = filepath.Abs(root)
	path, _ = filepath.Abs(path)
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative))
}

// canonicalizeParentPath resolves existing ancestors while leaving the final
// path component untouched so callers can still reject it if it is a symlink.
// For a destination that does not exist yet, this also resolves the nearest
// existing ancestor before appending the missing components.
func canonicalizeParentPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, name := filepath.Dir(abs), filepath.Base(abs)
	resolvedParent, err := canonicalizeExistingOrFuture(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, name), nil
}

func canonicalizeExistingOrFuture(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if _, err := os.Lstat(abs); err == nil {
		return filepath.EvalSymlinks(abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs, nil
	}
	resolvedParent, err := canonicalizeExistingOrFuture(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(abs)), nil
}
