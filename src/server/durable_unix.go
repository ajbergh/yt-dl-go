//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
)

// durableRename commits a completed file and then persists the directory entry.
func durableRename(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	if err := syncDirectory(destination); err != nil {
		return err
	}
	if filepath.Dir(source) != filepath.Dir(destination) {
		return syncDirectory(source)
	}
	return nil
}

// durableLink publishes a fully synced temporary file without replacing an
// existing user file. Both paths are in the same destination directory.
func durableLink(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	if err := syncDirectory(destination); err != nil {
		_ = os.Remove(destination)
		return err
	}
	if err := os.Remove(source); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = syncDirectory(destination)
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}
