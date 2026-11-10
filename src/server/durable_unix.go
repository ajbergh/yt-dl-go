//go:build !windows

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
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

// durableReplace atomically replaces a private managed file and persists the
// new directory entry. Callers write and sync source before invoking it.
func durableReplace(source, destination string) error {
	return durableRename(source, destination)
}

// durableLink publishes a fully synced temporary file without replacing an
// existing user file. Both paths are in the same destination directory.
func durableLink(source, destination string) error {
	return durableLinkUsing(source, destination, os.Link)
}

func durableLinkUsing(source, destination string, link func(string, string) error) error {
	if err := link(source, destination); err != nil {
		if !errors.Is(err, syscall.EOPNOTSUPP) && !errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EXDEV) {
			return err
		}
		return durableCopyExclusive(source, destination)
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

// Some removable and network filesystems cannot create hard links. Reserve the
// destination exclusively so this fallback can never replace an existing file.
// Keep the source until both the complete copy and its directory entry are synced.
func durableCopyExclusive(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(destination)
		}
	}()
	n, copyErr := io.Copy(out, in)
	if copyErr == nil && n != info.Size() {
		copyErr = io.ErrUnexpectedEOF
	}
	if err := errors.Join(copyErr, out.Sync(), out.Close()); err != nil {
		return err
	}
	if err := syncDirectory(destination); err != nil {
		return err
	}
	committed = true
	if err := os.Remove(source); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(destination)
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
