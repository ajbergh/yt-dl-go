package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// libraryPathSharedLocked checks both durable owners and active jobs whose
// finalized files may still be waiting for persistence. An empty fileID excludes
// the entire target job; otherwise only the selected item is excluded.
func (s *server) libraryPathSharedLocked(jobID, fileID, path string) (bool, error) {
	key := strings.ReplaceAll(filepath.Clean(path), `\`, "/")
	for _, job := range s.jobs {
		for _, file := range job.Files {
			if job.ID == jobID && (fileID == "" || file.ID == fileID) {
				continue
			}
			paths := []string{file.OutputPath}
			if file.Subtitle != nil {
				captionPath := file.Subtitle.OutputPath
				if captionPath == "" && file.Subtitle.OutputRelativePath != "" {
					captionPath = filepath.Join(job.DownloadLocation, filepath.FromSlash(file.Subtitle.OutputRelativePath))
				}
				paths = append(paths, captionPath)
			}
			for _, candidate := range paths {
				if candidate != "" && strings.EqualFold(strings.ReplaceAll(filepath.Clean(candidate), `\`, "/"), key) {
					return true, nil
				}
			}
		}
	}
	var shared bool
	err := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM library_items li
		LEFT JOIN library_sources j ON j.source_job_id=li.source_job_id
		WHERE (li.source_job_id<>? OR (?<>'' AND li.file_id<>?)) AND (
			replace(li.output_path,char(92),'/')=? COLLATE NOCASE OR
			(rtrim(replace(j.download_location,char(92),'/'),'/') || '/' || json_extract(li.file_json,'$.subtitle.outputRelativePath'))=? COLLATE NOCASE))`,
		jobID, fileID, fileID, key, key).Scan(&shared)
	return shared, err
}

// verifyTrackedFileForRemoval verifies a regular file with the recorded size
// using a rooted filesystem handle. Neither a symbolic link nor an escaped
// output path is accepted for permanent deletion.
func verifyTrackedFileForRemoval(root *os.Root, relative string, size int64) error {
	if relative == "" || size < 0 {
		return errors.New("missing tracked file metadata")
	}
	path := filepath.FromSlash(relative)
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return errors.New("tracked library file was replaced or changed")
	}
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("tracked library file changed while validating")
	}
	return nil
}

// removeSinglePublishedFile is intentionally not removeOutputCopies: it may
// unlink only the selected file and its own tracked caption sidecar.
func removeSinglePublishedFile(j *jobState, file *mediaFile) error {
	if j == nil || file == nil || !file.PublishedAvailable {
		return errors.New("the selected library file is unavailable")
	}
	if err := validateOutputCopy(j, *file); err != nil {
		return err
	}
	if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
		if err := validateSubtitleOutputCopy(j, *file.Subtitle); err != nil {
			return err
		}
	}
	// Disallow deleting a path referenced by another file or its caption.
	// This also protects against a stale job with accidentally shared paths.
	targets := map[string]bool{file.OutputRelativePath: true}
	if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
		targets[file.Subtitle.OutputRelativePath] = true
	}
	for _, sibling := range j.Files {
		if sibling.ID == file.ID {
			continue
		}
		if targets[sibling.OutputRelativePath] {
			return errors.New("a different library item refers to the same file")
		}
		if sibling.Subtitle != nil && targets[sibling.Subtitle.OutputRelativePath] {
			return errors.New("a different library item refers to the same caption")
		}
	}

	root, err := os.OpenRoot(j.DownloadLocation)
	if err != nil {
		return fmt.Errorf("open library root: %w", err)
	}
	defer root.Close()
	if err := verifyTrackedFileForRemoval(root, file.OutputRelativePath, file.Size); err != nil {
		return fmt.Errorf("validate selected media: %w", err)
	}
	if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
		if err := verifyTrackedFileForRemoval(root, file.Subtitle.OutputRelativePath, file.Subtitle.Size); err != nil {
			return fmt.Errorf("validate selected caption: %w", err)
		}
	}
	// Caption and media are prevalidated before either removal, minimizing
	// partial operations. Update flags after each successful removal so failed
	// later steps cannot claim that a deleted file is still on disk.
	if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
		if err := root.Remove(filepath.FromSlash(file.Subtitle.OutputRelativePath)); err != nil {
			return fmt.Errorf("remove selected caption: %w", err)
		}
		file.Subtitle.PublishedAvailable = false
	}
	if err := root.Remove(filepath.FromSlash(file.OutputRelativePath)); err != nil {
		return fmt.Errorf("remove selected media: %w", err)
	}
	file.PublishedAvailable = false
	return nil
}

func (s *server) handleSingleLibraryFileDelete(w http.ResponseWriter, jobID, fileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[jobID]
	if j == nil {
		fail(w, http.StatusNotFound, "Library item not found")
		return
	}
	if !terminal(j.Status) || j.deleting || j.readers > 0 {
		fail(w, http.StatusConflict, "The Library item is in use or not finalized")
		return
	}
	fileIndex := -1
	for i := range j.Files {
		if j.Files[i].ID == fileID {
			fileIndex = i
			break
		}
	}
	if fileIndex < 0 {
		fail(w, http.StatusNotFound, "Library item not found")
		return
	}
	exists, err := s.store.hasLibraryItem(jobID, fileID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not check the Library item")
		return
	}
	if !exists {
		fail(w, http.StatusNotFound, "Library item not found")
		return
	}
	file := &j.Files[fileIndex]
	// A second indexed item must never lose its file through an alias to the
	// same destination, including case-only path differences on Windows.
	paths := []string{file.OutputPath}
	if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
		paths = append(paths, file.Subtitle.OutputPath)
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		shared, err := s.libraryPathSharedLocked(jobID, fileID, path)
		if err != nil {
			fail(w, http.StatusInternalServerError, "Could not verify library file ownership")
			return
		}
		if shared {
			fail(w, http.StatusConflict, "Another Library item refers to the same output path")
			return
		}
	}
	j.deleting = true
	// Partial filesystem failures are persisted in the job record so a restart
	// can reconcile the remaining file safely. Never delete another item.
	recoverItem := func() {
		syncStoredFile(j, *file)
		s.invalidateJobTicketsLocked(j.ID)
		if saveErr := s.persistJobLocked(j); saveErr != nil {
			s.recordPersistenceFailure("recover single file deletion", j.ID, saveErr)
		}
		j.deleting = false
	}
	if err := removeSinglePublishedFile(j, file); err != nil {
		recoverItem()
		fail(w, http.StatusConflict, "Could not safely delete the selected library file")
		return
	}
	if err := removeManagedCopyForFile(j, file); err != nil {
		recoverItem()
		fail(w, http.StatusConflict, "The selected published file was deleted, but its private working copy could not be removed")
		return
	}
	syncStoredFile(j, *file)
	s.invalidateJobTicketsLocked(j.ID)
	snapshot := cloneJobStateForPersistence(j)
	if err := s.persistOperationLocked("delete selected Library file", j.ID, func(store *jobStore) error {
		return store.deleteLibraryItem(snapshot, fileID)
	}); err != nil {
		s.recordPersistenceFailure("delete selected Library file", j.ID, err)
		recoverItem()
		fail(w, http.StatusInternalServerError, "File was deleted, but the Library index could not be updated")
		return
	}
	s.recordPersistenceSuccess()
	if j.libraryOnly {
		j.Files = append(j.Files[:fileIndex], j.Files[fileIndex+1:]...)
		s.evictTerminalJobLocked(j)
	} else {
		j.deleting = false
		s.publishJobEventLocked("job-status", j)
	}
	w.WriteHeader(http.StatusNoContent)
}
