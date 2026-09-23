package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncCloseRenameCommitsCompleteFile(t *testing.T) {
	directory := t.TempDir()
	part := filepath.Join(directory, "media.mp4.part")
	final := filepath.Join(directory, "media.mp4")
	file, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("complete media"); err != nil {
		t.Fatal(err)
	}
	if err := syncCloseRename(file, part, final); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "complete media" {
		t.Fatalf("final file contains %q", data)
	}
	if _, err := os.Lstat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file remains: %v", err)
	}
}

func TestPublishTemporaryDoesNotReplaceExistingFile(t *testing.T) {
	directory := t.TempDir()
	temporary := filepath.Join(directory, "media.tmp")
	destination := filepath.Join(directory, "media.mp4")
	if err := os.WriteFile(temporary, []byte("new complete media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishTemporary(temporary, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("publishTemporary() error = %v, want file-exists", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user file" {
		t.Fatalf("existing destination was replaced with %q", data)
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("temporary file should remain available for cleanup or retry: %v", err)
	}
}
