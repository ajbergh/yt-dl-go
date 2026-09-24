package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func sampleLibraryImport() (libraryExportManifest, []libraryExportRecord) {
	manifest := libraryExportManifest{
		Format: "yt-dl-go-library-export", FormatVersion: libraryExportFormatVersion,
		ExportedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC), DatabaseVersion: 29,
		MediaIncluded: false, ContainsPrivateDB: true,
	}
	return manifest, []libraryExportRecord{
		{
			JobID: "original-job", URL: testVideo, Kind: "video", MediaType: "video", Status: "completed",
			Title: "Imported video", CreatedAt: "2026-08-01T10:00:00Z", Category: "Archive",
			File: libraryExportFile{
				ID: "original-file-1", Name: "video.mp4", Size: 1024, MimeType: "video/mp4", Title: "Portable title",
				Author: "Creator", OutputRelativePath: "Creator/video.mp4", ManagedAvailable: true, PublishedAvailable: true,
				SourceItemIndex: 1, ThumbnailURL: "https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg",
				Subtitle: &libraryExportSubtitle{
					LanguageCode: "en", Format: "vtt", Name: "video.en.vtt", Size: 128,
					OutputRelativePath: "Creator/video.en.vtt", ManagedAvailable: true, PublishedAvailable: true,
				},
			},
		},
		{
			JobID: "original-job", URL: testVideo, Kind: "video", MediaType: "video", Status: "completed",
			Title: "Imported video", CreatedAt: "2026-08-01T10:00:00Z", Category: "Archive",
			File: libraryExportFile{
				ID: "original-file-2", Name: "chapter.mp4", Size: 512, MimeType: "video/mp4", Title: "Chapter",
				SourceItemIndex: 1, ChapterIndex: 1, ChapterTitle: "Chapter one",
			},
		},
	}
}

func makeLibraryImportArchive(t *testing.T, manifest libraryExportManifest, records []libraryExportRecord) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	if err := addExportFile(archive, "state.db", writeImportTestFile(t)); err != nil {
		t.Fatal(err)
	}
	if err := addExportJSON(archive, "manifest.json", manifest); err != nil {
		t.Fatal(err)
	}
	if err := addExportJSON(archive, "library.json", libraryExportDocument{Manifest: manifest, Records: records}); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeImportTestFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private-state.db")
	if err := os.WriteFile(path, []byte("this private snapshot must be ignored by metadata import"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLibraryImportMergesPortableMetadataOnlyAndIsIdempotent(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.cfg.token = "library-import-token"
	settingsBefore, err := s.store.loadAppSettings()
	if err != nil {
		t.Fatal(err)
	}
	manifest, records := sampleLibraryImport()
	archive := makeLibraryImportArchive(t, manifest, records)
	request := func(data []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/library/import", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer library-import-token")
		req.Header.Set("Content-Type", "application/zip")
		response := httptest.NewRecorder()
		s.ServeHTTP(response, req)
		return response
	}

	unauthorized := httptest.NewRecorder()
	s.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/library/import", bytes.NewReader(archive)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized import status = %d, want 401", unauthorized.Code)
	}
	wrongMethod := httptest.NewRecorder()
	wrongMethodRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/library/import", nil)
	wrongMethodRequest.Header.Set("Authorization", "Bearer library-import-token")
	s.ServeHTTP(wrongMethod, wrongMethodRequest)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET import status = %d, want 405", wrongMethod.Code)
	}

	first := request(archive)
	if first.Code != http.StatusOK {
		t.Fatalf("import status = %d, body=%s", first.Code, first.Body.String())
	}
	var result struct {
		Imported   int  `json:"imported"`
		Idempotent bool `json:"idempotent"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil || result.Imported != 2 || result.Idempotent {
		t.Fatalf("import response = %+v, err=%v", result, err)
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 2 {
		t.Fatalf("imported Library = %+v, err=%v", jobs, err)
	}
	job := jobs[0]
	if job.ID == "original-job" || job.Status != "completed" || job.DownloadLocation != "" {
		t.Fatalf("imported source was not safely namespaced and finalized: %+v", job)
	}
	for _, file := range job.Files {
		if file.ID == "original-file-1" || file.OutputPath != "" || file.ManagedAvailable || file.PublishedAvailable || file.ThumbnailLocalAvailable {
			t.Fatalf("imported media is not safely marked unavailable: %+v", file)
		}
		if file.ID == importedFileID("original-job", "original-file-1") {
			if file.OutputRelativePath != "Creator/video.mp4" || file.Subtitle == nil || file.Subtitle.OutputPath != "" || file.Subtitle.ManagedAvailable || file.Subtitle.PublishedAvailable {
				t.Fatalf("portable display metadata or subtitle availability = %+v", file)
			}
		}
	}
	loaded, err := s.store.loadLibraryJob(s.cfg.root, job.ID)
	if err != nil || loaded == nil {
		t.Fatalf("imported source could not hydrate without a local output root: loaded=%+v err=%v", loaded, err)
	}
	if len(loaded.job.Files) != 2 || loaded.job.Files[0].ManagedAvailable || loaded.job.Files[0].OutputPath != "" {
		t.Fatalf("hydrated imported item has unsafe local availability: %+v", loaded.job.Files)
	}
	var savedHistory int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&savedHistory); err != nil || savedHistory != 0 {
		t.Fatalf("import modified job history: rows=%d err=%v", savedHistory, err)
	}
	settingsAfter, err := s.store.loadAppSettings()
	if err != nil || !reflect.DeepEqual(settingsAfter, settingsBefore) {
		t.Fatalf("import modified local settings: before=%+v after=%+v err=%v", settingsBefore, settingsAfter, err)
	}

	second := request(archive)
	if second.Code != http.StatusOK {
		t.Fatalf("repeat import status = %d, body=%s", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &result); err != nil || result.Imported != 0 || !result.Idempotent {
		t.Fatalf("repeat import response = %+v, err=%v", result, err)
	}
	jobs, err = s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 2 {
		t.Fatalf("repeat import duplicated Library metadata: jobs=%+v err=%v", jobs, err)
	}

	changed := append([]libraryExportRecord(nil), records...)
	changed[0].File.Title = "Conflicting title"
	conflict := request(makeLibraryImportArchive(t, manifest, changed))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed metadata for an existing imported identity status = %d, want 409; body=%s", conflict.Code, conflict.Body.String())
	}
	jobs, err = s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || jobs[0].Files[0].Title == "Conflicting title" {
		t.Fatalf("conflicting import partially changed Library: jobs=%+v err=%v", jobs, err)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:8080/api/jobs/"+job.ID+"/library-items/"+importedFileID("original-job", "original-file-1"), nil)
	deleteRequest.Header.Set("Authorization", "Bearer library-import-token")
	deleted := httptest.NewRecorder()
	s.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("remove imported Library item status = %d, body=%s", deleted.Code, deleted.Body.String())
	}
	jobs, err = s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 {
		t.Fatalf("remove imported Library item left incorrect metadata: jobs=%+v err=%v", jobs, err)
	}
}

func TestLibraryImportRejectsUnsafeOrIncompatibleArchiveWithoutMutation(t *testing.T) {
	s := newPersistenceTestServer(t)
	manifest, records := sampleLibraryImport()
	unsafe := append([]libraryExportRecord(nil), records...)
	unsafe[0].File.OutputRelativePath = "../../outside.mp4"
	if _, _, err := parseLibraryImportArchive(makeLibraryImportArchive(t, manifest, unsafe)); err == nil {
		t.Fatal("unsafe relative output path was accepted")
	}
	wrongVersion := manifest
	wrongVersion.FormatVersion++
	if _, _, err := parseLibraryImportArchive(makeLibraryImportArchive(t, wrongVersion, records)); err == nil {
		t.Fatal("unsupported archive version was accepted")
	}
	if _, _, err := parseLibraryImportArchive([]byte("not a ZIP archive")); err == nil {
		t.Fatal("invalid ZIP bytes were accepted")
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 0 {
		t.Fatalf("invalid archive changed Library state: jobs=%+v err=%v", jobs, err)
	}
}

func TestLibraryImportEntryReadIsBoundedAndChecksTrailingJSON(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	manifest, records := sampleLibraryImport()
	if err := addExportJSON(archive, "manifest.json", manifest); err != nil {
		t.Fatal(err)
	}
	entry, err := archive.Create("library.json")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(libraryExportDocument{Manifest: manifest, Records: records})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, string(encoded)+` {"trailing":true}`); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseLibraryImportArchive(buffer.Bytes()); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}

func TestLibraryExportArchiveImportsIntoAnotherLibrary(t *testing.T) {
	source := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "exported-source-job", URL: testVideo, Kind: "video", Quality: "1080", MediaType: "video",
		Status: "completed", Title: "Exported title", CreatedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Files: []mediaFile{{
			ID: "exported-source-file", Name: "saved.mp4", Size: 64, MimeType: "video/mp4", Title: "Saved file",
			OutputPath:         filepath.Join(source.cfg.root, "exported-source-job", "saved.mp4"),
			OutputRelativePath: filepath.Join("Creator", "saved.mp4"), ManagedAvailable: true, PublishedAvailable: true, SourceItemIndex: 1,
		}},
	}, dir: filepath.Join(source.cfg.root, "exported-source-job"), done: time.Now()}
	if err := source.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	exportPath, cleanup, err := source.createLibraryExport()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	archive, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	target := newPersistenceTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/library/import", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/zip")
	response := httptest.NewRecorder()
	target.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("importing real export archive status = %d, body=%s", response.Code, response.Body.String())
	}
	jobs, err := target.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 {
		t.Fatalf("imported real export Library = %+v, err=%v", jobs, err)
	}
	file := jobs[0].Files[0]
	if file.OutputPath != "" || file.ManagedAvailable || file.PublishedAvailable || file.OutputRelativePath != "Creator/saved.mp4" {
		t.Fatalf("real export was not imported as unavailable portable metadata: %+v", file)
	}
}
