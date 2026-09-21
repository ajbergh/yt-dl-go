package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type filesystemOpener func(context.Context, string, string) error
type folderSelector func(context.Context) (string, error)

var errFolderSelectionCancelled = errors.New("folder selection cancelled")

func nativeFolderCommand(goos, zenityPath string) (string, []string, error) {
	switch goos {
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description = 'Choose download folder'; if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($d.SelectedPath) }`
		return "powershell.exe", []string{"-NoProfile", "-STA", "-Command", script}, nil
	case "darwin":
		return "osascript", []string{"-e", `POSIX path of (choose folder with prompt "Choose download folder")`}, nil
	case "linux":
		if zenityPath == "" {
			return "", nil, errors.New("native folder selection requires zenity on Linux")
		}
		return zenityPath, []string{"--file-selection", "--directory", "--title=Choose download folder"}, nil
	default:
		return "", nil, errors.New("native folder selection is not supported on this platform")
	}
}

func selectNativeFolder(ctx context.Context) (string, error) {
	zenityPath := ""
	if runtime.GOOS == "linux" {
		path, err := exec.LookPath("zenity")
		if err != nil {
			return "", errors.New("native folder selection requires zenity on Linux")
		}
		zenityPath = path
	}
	commandName, args, err := nativeFolderCommand(runtime.GOOS, zenityPath)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, commandName, args...)
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", errFolderSelectionCancelled
		}
		return "", err
	}
	selected := strings.TrimSpace(string(output))
	if selected == "" {
		return "", errFolderSelectionCancelled
	}
	selected = filepath.Clean(selected)
	if !filepath.IsAbs(selected) || filepath.Dir(selected) == selected || len(selected) > 32760 || strings.ContainsRune(selected, '\x00') {
		return "", errors.New("folder picker returned an invalid path")
	}
	return selected, nil
}

func trackedOutputCommand(goos, action, path string) (string, []string, error) {
	if action != "reveal" && action != "open-folder" {
		return "", nil, errors.New("unsupported filesystem open action")
	}
	folder := filepath.Dir(path)
	switch goos {
	case "windows":
		if action == "reveal" {
			return "explorer.exe", []string{"/select," + path}, nil
		}
		return "explorer.exe", []string{folder}, nil
	case "darwin":
		if action == "reveal" {
			return "open", []string{"-R", path}, nil
		}
		return "open", []string{folder}, nil
	case "linux":
		// xdg-open has no portable file-selection operation, so reveal opens
		// the containing folder rather than executing an arbitrary file path.
		return "xdg-open", []string{folder}, nil
	default:
		return "", nil, errors.New("filesystem opening is not supported on this platform")
	}
}

func openTrackedOutput(ctx context.Context, action, path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("published output is unavailable")
	}
	commandName, args, err := trackedOutputCommand(runtime.GOOS, action, path)
	if err != nil {
		return err
	}
	return exec.CommandContext(ctx, commandName, args...).Run()
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


func (s *server) handleFolderSelection(w http.ResponseWriter, r *http.Request) {
	var request struct{}
	if !decode(w, r, &request) {
		return
	}
	selector := s.folderSelector
	if selector == nil {
		selector = selectNativeFolder
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	path, err := selector(ctx)
	if errors.Is(err, errFolderSelectionCancelled) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		fail(w, 500, "Could not open the native folder picker")
		return
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) || filepath.Dir(path) == path || len(path) > 32760 || strings.ContainsRune(path, '\x00') {
		fail(w, 500, "The native folder picker returned an invalid path")
		return
	}
	reply(w, 200, map[string]string{"path": path})
}
