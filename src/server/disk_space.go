package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// Available is space usable by this process, including OS/user quota constraints.
type diskSpace struct{ Available, Total uint64 }
type storageWarning struct {
	Path        string  `json:"path"`
	FreeBytes   uint64  `json:"freeBytes"`
	TotalBytes  uint64  `json:"totalBytes"`
	FreePercent float64 `json:"freePercent"`
}

func lowDiskSpace(space diskSpace) bool {
	// Integer comparison avoids overflow and treats exactly 5% as healthy.
	return space.Total > 0 && (space.Available < space.Total/20 || (space.Available == space.Total/20 && space.Total%20 != 0))
}

// A newly selected destination may not exist yet. Inspect its closest existing
// ancestor, preserving the original target path in the warning.
func diskSpaceForTarget(path string) (diskSpace, error) {
	for {
		_, err := os.Stat(path)
		if err == nil {
			return readDiskSpace(path)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return diskSpace{}, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return diskSpace{}, err
		}
		path = parent
	}
}

func (s *server) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	s.mu.Lock()
	targets := map[string]bool{s.settings.DownloadLocation: true}
	for _, j := range s.jobs {
		switch j.Status {
		case "queued", "downloading", "processing", "paused":
			targets[j.DownloadLocation] = true
		}
	}
	provider := s.diskSpaceReader
	s.mu.Unlock()
	if provider == nil {
		provider = diskSpaceForTarget
	}
	paths := make([]string, 0, len(targets))
	for path := range targets {
		if filepath.IsAbs(path) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	warnings := []storageWarning{}
	for _, path := range paths {
		space, err := provider(path)
		// Inspection failures are advisory too: they must never prevent downloads.
		if err != nil || !lowDiskSpace(space) {
			continue
		}
		warnings = append(warnings, storageWarning{Path: path, FreeBytes: space.Available, TotalBytes: space.Total, FreePercent: float64(space.Available) / float64(space.Total) * 100})
	}
	reply(w, http.StatusOK, map[string]any{"warnings": warnings})
}
