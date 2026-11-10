//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDurableLinkUnsupportedFilesystem(t *testing.T) {
	for _, linkErr := range []error{syscall.EOPNOTSUPP, syscall.ENOTSUP, syscall.ENOSYS, syscall.EPERM, syscall.EXDEV} {
		t.Run(linkErr.Error(), func(t *testing.T) {
			dir := t.TempDir()
			source, destination := filepath.Join(dir, "source"), filepath.Join(dir, "output")
			if err := os.WriteFile(source, []byte("complete media"), 0600); err != nil {
				t.Fatal(err)
			}
			link := func(string, string) error { return &os.LinkError{Op: "link", Err: linkErr} }
			if err := durableLinkUsing(source, destination, link); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(destination)
			if err != nil || string(data) != "complete media" {
				t.Fatalf("published %q: %v", data, err)
			}
			if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("source remains: %v", err)
			}
			if err := os.WriteFile(source, []byte("another download"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := durableLinkUsing(source, destination, link); !errors.Is(err, os.ErrExist) {
				t.Fatalf("collision: %v", err)
			}
			data, err = os.ReadFile(destination)
			if err != nil || string(data) != "complete media" {
				t.Fatalf("overwrote destination: %q %v", data, err)
			}
			if _, err := os.Stat(source); err != nil {
				t.Fatalf("collision removed source: %v", err)
			}
		})
	}
}
