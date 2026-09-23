package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func syncCloseRename(file *os.File, temporary, destination string) error {
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	return durableRename(temporary, destination)
}

func writeTemporaryCopy(source io.Reader, directory, pattern string, expected int64) (string, error) {
	temporary, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	copied, copyErr := io.Copy(temporary, source)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if copyErr != nil || copied != expected || syncErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copied != expected {
			copyErr = errors.Join(copyErr, fmt.Errorf("copied %d bytes, expected %d", copied, expected))
		}
		return "", errors.Join(copyErr, syncErr, closeErr)
	}
	return path, nil
}

func publishTemporary(temporary, destination string) error {
	return durableLink(temporary, destination)
}

func temporarySibling(destination string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(destination), ".yt-dl-go-*.part")
}
