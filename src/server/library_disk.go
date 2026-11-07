package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Work files and artwork live inside the selected library, alongside (but
// separate from) named media. No second finalized media copy is retained.
func libraryWorkDir(j Job) string {
	return filepath.Join(j.DownloadLocation, ".yt-dl-go", j.ID)
}

func libraryFilename(file mediaFile) string { return file.OutputName }

func restoreSubtitlePath(location string, file *mediaFile) {
	if file.Subtitle == nil || !filepath.IsAbs(location) || file.Subtitle.OutputRelativePath == "" {
		return
	}
	file.Subtitle.OutputPath = filepath.Join(location, filepath.FromSlash(file.Subtitle.OutputRelativePath))
}

var errLibraryRootUnavailable = errors.New("library folder is unavailable")

func openLibraryPath(root, relative string, size int64) (*os.File, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errLibraryRootUnavailable, err)
	}
	defer r.Close()
	before, err := r.Lstat(filepath.FromSlash(relative))
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() != size {
		return nil, errors.New("library file has changed")
	}
	f, err := r.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, errors.New("library file has changed")
	}
	return f, nil
}

func openLibraryMedia(j *jobState, file mediaFile) (*os.File, error) {
	if err := validateOutputCopy(j, file); err != nil {
		return nil, err
	}
	return openLibraryPath(j.DownloadLocation, file.OutputRelativePath, file.Size)
}

func openLibrarySubtitle(j *jobState, file subtitleFile) (*os.File, error) {
	if err := validateSubtitleOutputCopy(j, file); err != nil {
		return nil, err
	}
	return openLibraryPath(j.DownloadLocation, file.OutputRelativePath, file.Size)
}

func openCompletedMedia(j *jobState, file mediaFile) (*os.File, error) {
	if file.OutputPath != "" {
		return openLibraryMedia(j, file)
	}
	return openFinal(j.dir, file.Name) // Legacy unfinished jobs only.
}

// reconcileLibrary runs before accepting requests. Exclusions ensure a missing
// file cannot be reintroduced by a later history save. Metadata-only imports
// intentionally have no local path and are left alone.
func (s *server) reconcileLibrary() error {
	jobs, err := s.store.loadLibraryDiskJobs(nil, true)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := s.reconcileLibraryJob(job); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) reconcileLibraryJob(job Job) (resultErr error) {
	var created []string
	defer func() {
		if resultErr != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	if filepath.Base(job.ID) != job.ID || job.ID == "." || job.ID == ".." {
		return errors.New("unsafe library source id")
	}
	loaded, err := s.store.loadJob(s.cfg.root, job.ID)
	if err != nil {
		return err
	}
	j := &jobState{Job: job, dir: filepath.Join(s.cfg.root, job.ID)}
	if loaded != nil {
		j = &jobState{Job: loaded.job, dir: loaded.dir, done: loaded.done, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items)}
	} else {
		j.NamingPattern, j.SubfolderSorting = s.settings.NamingPattern, s.settings.SubfolderSorting
		j.OutputFileMode, j.OutputFolderMode = s.settings.OutputFileMode, s.settings.OutputFolderMode
	}
	var obsolete []string
	var updates []mediaFile
	for _, tracked := range job.Files {
		file := tracked
		migrated := false
		if file.OutputPath == "" && !file.ManagedAvailable && !file.PublishedAvailable {
			continue
		}
		// Only legacy managed-only files get moved. A missing canonical file
		// must never be restored from an obsolete private duplicate.
		if file.OutputPath == "" && file.ManagedAvailable {
			if _, statErr := os.Lstat(filepath.Join(j.dir, file.Name)); errors.Is(statErr, os.ErrNotExist) {
				file.ManagedAvailable = false
				syncStoredFile(j, file)
				if err := s.store.deleteLibraryItem(j, file.ID); err != nil {
					return err
				}
				continue
			}
			if err := s.finalizeLibraryOutput(j, &file, false); err != nil {
				return fmt.Errorf("migrate library file: %w", err)
			}
			created = append(created, file.OutputPath)
			migrated = true
		}
		f, openErr := openLibraryMedia(j, file)
		if migrated && openErr != nil {
			return fmt.Errorf("validate migrated library file: %w", openErr)
		}
		if errors.Is(openErr, errLibraryRootUnavailable) || (openErr != nil && !errors.Is(openErr, os.ErrNotExist)) {
			// An unavailable volume, permission failure or changed file is not
			// evidence that the user deleted it. Preserve its metadata.
			continue
		}
		if f != nil {
			_ = f.Close()
		}
		if openErr != nil {
			file.PublishedAvailable, file.ManagedAvailable = false, false
			syncStoredFile(j, file)
			for i := range j.Files {
				if j.Files[i].ID == file.ID {
					j.Files[i] = file
				}
			}
			if err := s.store.deleteLibraryItem(j, file.ID); err != nil {
				return err
			}
		} else {
			// Discard an old app copy only after validating the folder file.
			if file.ManagedAvailable {
				if outputName.MatchString(file.Name) {
					obsolete = append(obsolete, filepath.Join(j.dir, file.Name))
				}
			}
			file.ManagedAvailable, file.PublishedAvailable = false, true
			if file.Subtitle != nil {
				if file.Subtitle.OutputPath == "" && file.Subtitle.ManagedAvailable {
					// Captions are optional. Missing or invalid legacy sidecars must
					// not prevent the media migration or the application startup.
					if err := publishSubtitleOutput(j, &file); err != nil {
						file.SubtitleError = "The legacy caption sidecar could not be migrated."
						file.Subtitle = nil
					} else {
						created = append(created, file.Subtitle.OutputPath)
					}
				}
				if file.Subtitle != nil {
					caption, err := openLibrarySubtitle(j, *file.Subtitle)
					if err == nil {
						_ = caption.Close()
						if file.Subtitle.ManagedAvailable && validateSubtitleManagedName(file.Subtitle.Name, file.Subtitle.Format) == nil {
							obsolete = append(obsolete, filepath.Join(j.dir, file.Subtitle.Name))
						}
						file.Subtitle.ManagedAvailable = false
					} else if errors.Is(err, os.ErrNotExist) && !errors.Is(err, errLibraryRootUnavailable) {
						file.Subtitle = nil
					}
				}
			}
			syncStoredFile(j, file)
			for i := range j.Files {
				if j.Files[i].ID == file.ID {
					j.Files[i] = file
				}
			}
			updates = append(updates, file)
		}
	}
	if loaded != nil {
		if err := s.store.saveJob(j); err != nil {
			return err
		}
	} else {
		tx, err := s.store.db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		for _, file := range updates {
			data, err := json.Marshal(file)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE library_items SET file_json=?,output_path=? WHERE source_job_id=? AND file_id=?`, string(data), file.OutputPath, j.ID, file.ID); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// The new paths are durable. Cleanup failures must never roll them back.
	created = nil
	for _, path := range obsolete {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
