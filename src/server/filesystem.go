package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type filesystemOpener func(context.Context, string, string) error

func openTrackedOutput(ctx context.Context, action, path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("published output is unavailable")
	}
	folder := filepath.Dir(path)
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		if action == "reveal" {
			command = exec.CommandContext(ctx, "explorer.exe", "/select,"+path)
		} else {
			command = exec.CommandContext(ctx, "explorer.exe", folder)
		}
	case "darwin":
		if action == "reveal" {
			command = exec.CommandContext(ctx, "open", "-R", path)
		} else {
			command = exec.CommandContext(ctx, "open", folder)
		}
	case "linux":
		// xdg-open has no portable file-selection operation, so reveal opens
		// the containing folder rather than executing an arbitrary path.
		command = exec.CommandContext(ctx, "xdg-open", folder)
	default:
		return errors.New("filesystem opening is not supported on this platform")
	}
	return command.Run()
}

func (s *server) handleFilesystem(w http.ResponseWriter, r *http.Request, jobID string) {
	var request struct {
		FileID string `json:"fileId"`
		Action string `json:"action"`
	}
	if !decode(w, r, &request) {
		return
	}
	if request.FileID == "" {
		fail(w, 400, "fileId is required")
		return
	}
	if request.Action != "copy-path" && request.Action != "reveal" && request.Action != "open-folder" {
		fail(w, 400, "action must be copy-path, reveal, or open-folder")
		return
	}

	s.mu.Lock()
	j := s.jobs[jobID]
	if j == nil {
		s.mu.Unlock()
		fail(w, 404, "Job not found")
		return
	}
	if !terminal(j.Status) {
		s.mu.Unlock()
		fail(w, 409, "Filesystem actions are available only for stopped jobs")
		return
	}
	var file mediaFile
	found := false
	for _, candidate := range j.Files {
		if candidate.ID == request.FileID {
			file = candidate
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		fail(w, 404, "File not found")
		return
	}
	if !file.PublishedAvailable {
		s.mu.Unlock()
		fail(w, 409, "The published copy is no longer available")
		return
	}
	if err := validateOutputCopy(j, file); err != nil {
		s.mu.Unlock()
		fail(w, 409, "The tracked published path is invalid")
		return
	}
	if info, err := os.Lstat(file.OutputPath); err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
		s.mu.Unlock()
		fail(w, 409, "The published copy is no longer available")
		return
	}
	path := file.OutputPath
	if request.Action == "copy-path" {
		s.mu.Unlock()
		reply(w, 200, map[string]string{"path": path})
		return
	}
	j.readers++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		j.readers--
		s.mu.Unlock()
	}()

	opener := s.filesystemOpener
	if opener == nil {
		opener = openTrackedOutput
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := opener(ctx, request.Action, path); err != nil {
		fail(w, 500, "Could not open the tracked filesystem location")
		return
	}
	reply(w, 200, map[string]string{"path": path})
}
