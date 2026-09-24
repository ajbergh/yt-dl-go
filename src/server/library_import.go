package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxLibraryImportArchiveBytes = 64 << 20
	maxLibraryImportJSONBytes    = 64 << 20
	maxLibraryImportManifest     = 1 << 20
	maxLibraryImportRecords      = 10000
)

var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:`)

type libraryImportError struct {
	status int
	err    error
}

func (e *libraryImportError) Error() string { return e.err.Error() }
func (e *libraryImportError) Unwrap() error { return e.err }

type libraryImportConflictError struct{ detail string }

func (e *libraryImportConflictError) Error() string { return e.detail }

func badLibraryImport(format string, args ...any) error {
	return &libraryImportError{status: http.StatusBadRequest, err: fmt.Errorf(format, args...)}
}

func (s *server) handleLibraryImport(w http.ResponseWriter, r *http.Request) {
	archive, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLibraryImportArchiveBytes))
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			fail(w, http.StatusRequestEntityTooLarge, "Library import archive exceeds 64 MiB")
			return
		}
		fail(w, http.StatusBadRequest, "Could not read Library import archive")
		return
	}
	manifest, document, err := parseLibraryImportArchive(archive)
	if err != nil {
		status := http.StatusBadRequest
		var importErr *libraryImportError
		if errors.As(err, &importErr) {
			status = importErr.status
		}
		fail(w, status, err.Error())
		return
	}
	count, err := s.store.importLibraryRecords(manifest, document.Records)
	if err != nil {
		var conflict *libraryImportConflictError
		if errors.As(err, &conflict) {
			fail(w, http.StatusConflict, "Library archive conflicts with existing imported records")
		} else {
			fail(w, http.StatusInternalServerError, "Could not import Library metadata")
		}
		return
	}
	reply(w, http.StatusOK, map[string]any{"imported": count, "idempotent": count == 0})
}

func parseLibraryImportArchive(data []byte) (libraryExportManifest, libraryExportDocument, error) {
	var manifest libraryExportManifest
	var document libraryExportDocument
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return manifest, document, badLibraryImport("Invalid Library ZIP archive")
	}
	if len(reader.File) > 4 {
		return manifest, document, badLibraryImport("Library archive contains too many files")
	}
	entries := make(map[string]*zip.File, len(reader.File))
	var totalUncompressed uint64
	for _, entry := range reader.File {
		if entry.Name != "manifest.json" && entry.Name != "library.json" && entry.Name != "library.csv" && entry.Name != "state.db" {
			return manifest, document, badLibraryImport("Library archive contains an unsupported file")
		}
		if _, exists := entries[entry.Name]; exists {
			return manifest, document, badLibraryImport("Library archive contains duplicate files")
		}
		entries[entry.Name] = entry
		totalUncompressed += entry.UncompressedSize64
		if entry.UncompressedSize64 > maxLibraryImportJSONBytes || totalUncompressed > 192<<20 {
			return manifest, document, badLibraryImport("Library archive expands beyond the supported size")
		}
	}
	manifestEntry, ok := entries["manifest.json"]
	if !ok || manifestEntry.UncompressedSize64 == 0 || manifestEntry.UncompressedSize64 > maxLibraryImportManifest {
		return manifest, document, badLibraryImport("Library archive has no valid manifest.json")
	}
	documentEntry, ok := entries["library.json"]
	if !ok || documentEntry.UncompressedSize64 == 0 || documentEntry.UncompressedSize64 > maxLibraryImportJSONBytes {
		return manifest, document, badLibraryImport("Library archive has no valid library.json")
	}
	manifestBytes, err := readZipEntryBounded(manifestEntry, maxLibraryImportManifest)
	if err != nil {
		return manifest, document, badLibraryImport("Could not read Library manifest")
	}
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return manifest, document, badLibraryImport("Invalid Library manifest")
	}
	if manifest.Format != "yt-dl-go-library-export" || manifest.FormatVersion != libraryExportFormatVersion || manifest.MediaIncluded {
		return manifest, document, badLibraryImport("Unsupported Library archive format or version")
	}
	documentBytes, err := readZipEntryBounded(documentEntry, maxLibraryImportJSONBytes)
	if err != nil {
		return manifest, document, badLibraryImport("Could not read Library metadata")
	}
	if err := decodeStrictJSON(documentBytes, &document); err != nil {
		return manifest, document, badLibraryImport("Invalid Library metadata")
	}
	if document.Manifest != manifest {
		return manifest, document, badLibraryImport("Library manifest does not match library.json")
	}
	if len(document.Records) > maxLibraryImportRecords {
		return manifest, document, badLibraryImport("Library archive contains too many records")
	}
	if err := validatePortableRecords(document.Records); err != nil {
		return manifest, document, err
	}
	return manifest, document, nil
}

func readZipEntryBounded(entry *zip.File, limit int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("ZIP entry exceeds limit")
	}
	file, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("ZIP entry could not be read within limit")
	}
	return data, nil
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validatePortableRecords(records []libraryExportRecord) error {
	type sourceShape struct {
		url, kind, mediaType, title, category, createdAt string
	}
	sources := make(map[string]sourceShape)
	items := make(map[string]struct{}, len(records))
	for _, record := range records {
		file := record.File
		if record.JobID == "" || len(record.JobID) > 1024 || record.URL == "" || len(record.URL) > 8192 || len(record.Title) > 8192 || len(record.Category) > 512 || file.ID == "" || len(file.ID) > 1024 || file.Name == "" || len(file.Name) > 1024 || len(file.MimeType) > 256 || len(file.Title) > 8192 || len(file.Author) > 8192 || len(file.Category) > 512 || len(file.ThumbnailURL) > 8192 || len(file.OutputRelativePath) > 4096 || len(file.ChapterTitle) > 8192 {
			return badLibraryImport("Library metadata contains an empty or overlong identifier or name")
		}
		if record.Kind != "video" && record.Kind != "playlist" {
			return badLibraryImport("Library metadata contains an unsupported job kind")
		}
		if record.MediaType != "video" && record.MediaType != "audio" {
			return badLibraryImport("Library metadata contains an unsupported media type")
		}
		if file.Size < 0 || file.DurationSeconds < 0 || file.Height < 0 || file.SourceItemIndex < 0 || file.ChapterIndex < 0 {
			return badLibraryImport("Library metadata contains invalid numeric file metadata")
		}
		if _, err := portableRelativePath(file.OutputRelativePath); err != nil {
			return badLibraryImport("Library metadata contains an unsafe relative output path")
		}
		if file.Subtitle != nil {
			if file.Subtitle.Size < 0 || file.Subtitle.Name == "" || len(file.Subtitle.Name) > 1024 || len(file.Subtitle.LanguageCode) > 64 || len(file.Subtitle.Label) > 1024 || len(file.Subtitle.OutputRelativePath) > 4096 || (file.Subtitle.Format != "vtt" && file.Subtitle.Format != "srt") {
				return badLibraryImport("Library metadata contains invalid subtitle metadata")
			}
			if _, err := portableRelativePath(file.Subtitle.OutputRelativePath); err != nil {
				return badLibraryImport("Library metadata contains an unsafe subtitle output path")
			}
		}
		if record.CreatedAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, record.CreatedAt); err != nil {
				return badLibraryImport("Library metadata contains an invalid creation time")
			}
		}
		shape := sourceShape{record.URL, record.Kind, record.MediaType, record.Title, record.Category, record.CreatedAt}
		if previous, ok := sources[record.JobID]; ok && previous != shape {
			return badLibraryImport("Library metadata has inconsistent source details for one job")
		}
		sources[record.JobID] = shape
		key := record.JobID + "\x00" + file.ID
		if _, exists := items[key]; exists {
			return badLibraryImport("Library metadata contains duplicate file records")
		}
		items[key] = struct{}{}
	}
	return nil
}

func portableRelativePath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") || path.IsAbs(value) || filepath.IsAbs(value) || windowsAbsolutePath.MatchString(value) {
		return "", errors.New("path is not portable and relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path traverses outside its relative root")
	}
	return clean, nil
}

func (s *jobStore) importLibraryRecords(manifest libraryExportManifest, records []libraryExportRecord) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("state database is unavailable")
	}
	stableRecords := append([]libraryExportRecord(nil), records...)
	sort.Slice(stableRecords, func(i, j int) bool {
		if stableRecords[i].JobID != stableRecords[j].JobID {
			return stableRecords[i].JobID < stableRecords[j].JobID
		}
		if stableRecords[i].File.ID != stableRecords[j].File.ID {
			return stableRecords[i].File.ID < stableRecords[j].File.ID
		}
		return stableRecords[i].File.Name < stableRecords[j].File.Name
	})
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	createdSources := make(map[string]struct{})
	createdItems := 0
	for _, record := range stableRecords {
		jobID := importedSourceID(record.JobID)
		createdAt := record.CreatedAt
		if createdAt == "" {
			createdAt = manifest.ExportedAt.UTC().Format(time.RFC3339Nano)
		}
		if _, exists := createdSources[jobID]; !exists {
			created, err := insertImportedLibrarySource(tx, jobID, record, createdAt)
			if err != nil {
				return 0, err
			}
			if created {
				createdSources[jobID] = struct{}{}
			}
		}
		fileID := importedFileID(record.JobID, record.File.ID)
		file := importedMediaFile(record.File, record.MediaType)
		index := file.SourceItemIndex
		if index < 1 {
			index = 1
		}
		file.SourceItemIndex = index
		file.ID = fileID
		fileJSON, err := json.Marshal(file)
		if err != nil {
			return 0, err
		}
		var existingSource string
		var existingIndex int
		var existingJSON, existingPath string
		err = tx.QueryRow(`SELECT source_job_id,source_item_index,file_json,output_path FROM library_items WHERE file_id=?`, fileID).Scan(&existingSource, &existingIndex, &existingJSON, &existingPath)
		if err == nil {
			if existingSource != jobID || existingIndex != index || existingJSON != string(fileJSON) || existingPath != "" {
				return 0, &libraryImportConflictError{detail: fmt.Sprintf("deterministic imported item id %q conflicts with existing Library data", fileID)}
			}
			continue
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO library_items(file_id,source_job_id,source_item_index,file_json,output_path,created_at) VALUES(?,?,?,?,?,?)`,
			fileID, jobID, index, string(fileJSON), "", time.Now().UnixNano()); err != nil {
			return 0, err
		}
		createdItems++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return createdItems, nil
}

func importedSourceID(originalID string) string {
	original := sha256.Sum256([]byte(originalID))
	return "import-" + hex.EncodeToString(original[:])
}

func importedFileID(originalJobID, originalFileID string) string {
	original := sha256.Sum256([]byte(originalJobID + "\x00" + originalFileID))
	return "import-" + hex.EncodeToString(original[:])
}

func importedMediaFile(source libraryExportFile, mediaType string) mediaFile {
	relative, _ := portableRelativePath(source.OutputRelativePath)
	file := mediaFile{
		ID: "", Name: source.Name, Size: source.Size, Height: source.Height, MimeType: source.MimeType,
		Title: source.Title, Author: source.Author, DurationSeconds: source.DurationSeconds,
		ThumbnailURL: source.ThumbnailURL, ThumbnailLocalAvailable: false, PublishDate: source.PublishDate,
		Category: source.Category, MediaType: mediaType, OutputPath: "", OutputRelativePath: relative,
		ManagedAvailable: false, PublishedAvailable: false, Chapters: append([]mediaChapter(nil), source.Chapters...),
		SourceItemIndex: source.SourceItemIndex, ChapterIndex: source.ChapterIndex, ChapterTitle: source.ChapterTitle,
	}
	if source.Subtitle != nil {
		subtitleRelative, _ := portableRelativePath(source.Subtitle.OutputRelativePath)
		file.Subtitle = &subtitleFile{
			LanguageCode: source.Subtitle.LanguageCode, Label: source.Subtitle.Label, Format: source.Subtitle.Format,
			AutoGenerated: source.Subtitle.AutoGenerated, Name: source.Subtitle.Name, Size: source.Subtitle.Size,
			OutputPath: "", OutputRelativePath: subtitleRelative, ManagedAvailable: false, PublishedAvailable: false,
		}
	}
	return file
}

func insertImportedLibrarySource(tx *sql.Tx, jobID string, record libraryExportRecord, createdAt string) (bool, error) {
	searchText := strings.Join([]string{record.Title, record.URL, record.Category, "", ""}, " ")
	result, err := tx.Exec(`INSERT OR IGNORE INTO library_sources (
		source_job_id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,subtitle_language,subtitle_format,split_by_chapter,status,title,progress,current_item,completed_count,total_count,error,created_at,note,category,storage_mode,queue_position,search_text,download_location
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		jobID, record.URL, record.Kind, "best", "best", false, record.MediaType, "", 0, "", "", false,
		"completed", record.Title, nil, "", 0, nil, "", createdAt, "Imported Library metadata; media files are not available on this device.", record.Category, "managed-only", 0, searchText, "")
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		return true, nil
	}
	var existingURL, existingKind, existingMediaType, existingTitle, existingCreatedAt string
	var existingCategory, existingStatus, existingLocation string
	if err := tx.QueryRow(`SELECT url,kind,media_type,title,category,status,created_at,download_location FROM library_sources WHERE source_job_id=?`, jobID).
		Scan(&existingURL, &existingKind, &existingMediaType, &existingTitle, &existingCategory, &existingStatus, &existingCreatedAt, &existingLocation); err != nil {
		return false, err
	}
	if existingURL != record.URL || existingKind != record.Kind || existingMediaType != record.MediaType || existingTitle != record.Title || existingCategory != record.Category || existingStatus != "completed" || existingCreatedAt != createdAt || existingLocation != "" {
		return false, &libraryImportConflictError{detail: fmt.Sprintf("deterministic imported source id %q conflicts with existing Library data", jobID)}
	}
	return false, nil
}
