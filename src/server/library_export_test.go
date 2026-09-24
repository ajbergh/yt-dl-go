package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLibraryExportContainsConsistentSnapshotAndPortableMetadata(t *testing.T) {
	s := newPersistenceTestServer(t)
	s.cfg.token = "library-export-token"
	job := &jobState{Job: Job{
		ID: "job-library-export", URL: testVideo, Kind: "video", Quality: "best", MediaType: "video", Status: "completed",
		Title: "Playlist, \"quoted\"\ntitle", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Files: []mediaFile{{
			ID: "export-file", Name: "primary.mp4", Size: 7, MimeType: "video/mp4", Title: "A title",
			Author: `=HYPERLINK("https://example.invalid","external")`, OutputPath: filepath.Join(s.cfg.root, "job-library-export", "primary.mp4"),
			OutputRelativePath: filepath.Join("Channel", "primary.mp4"), ManagedAvailable: true, PublishedAvailable: true,
			SourceItemIndex: 1,
		}},
	}, dir: filepath.Join(s.cfg.root, "job-library-export"), done: time.Now()}
	if err := s.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	const privateRaw = "private-quarantine-payload"
	if _, err := s.store.db.Exec(`INSERT INTO corrupt_records(detected_at,source_table,source_column,record_key,error,action,raw_value) VALUES(?,?,?,?,?,?,?)`,
		time.Now().UnixNano(), "app_settings", "user_categories", "1", "malformed JSON", "reset settings", []byte(privateRaw)); err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	s.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/library/export", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized export status = %d, want 401", unauthorized.Code)
	}
	wrongMethod := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/library/export", nil)
	wrongMethod.Header.Set("Authorization", "Bearer library-export-token")
	wrongMethodRecorder := httptest.NewRecorder()
	s.ServeHTTP(wrongMethodRecorder, wrongMethod)
	if wrongMethodRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST export status = %d, want 405", wrongMethodRecorder.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/library/export", nil)
	request.Header.Set("Authorization", "Bearer library-export-token")
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export status/content type = %d/%q, body=%s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte, len(archive.File))
	for _, entry := range archive.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archive entry %s: read=%v close=%v", entry.Name, readErr, closeErr)
		}
		entries[entry.Name] = data
	}
	for _, name := range []string{"state.db", "manifest.json", "library.json", "library.csv"} {
		if len(entries[name]) == 0 {
			t.Fatalf("export is missing non-empty %s entry", name)
		}
	}

	var manifest libraryExportManifest
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "yt-dl-go-library-export" || manifest.FormatVersion != libraryExportFormatVersion || manifest.MediaIncluded || !manifest.ContainsPrivateDB || manifest.QuarantinedRecordCount != 1 {
		t.Fatalf("export manifest = %+v", manifest)
	}
	var document libraryExportDocument
	if err := json.Unmarshal(entries["library.json"], &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Records) != 1 || document.Records[0].File.ID != "export-file" || document.Records[0].File.OutputRelativePath != "Channel/primary.mp4" {
		t.Fatalf("export metadata records = %+v", document.Records)
	}
	metadataText := string(entries["library.json"]) + string(entries["library.csv"])
	if strings.Contains(metadataText, privateRaw) || strings.Contains(metadataText, s.cfg.root) || strings.Contains(metadataText, filepath.Join(s.cfg.root, "job-library-export")) {
		t.Fatal("portable Library metadata exposed a private payload or absolute local path")
	}
	csvReader := csv.NewReader(bytes.NewReader(entries["library.csv"]))
	if _, err := csvReader.Read(); err != nil {
		t.Fatal(err)
	}
	row, err := csvReader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if row[5] != "Playlist, \"quoted\"\ntitle" || row[14] != `'=HYPERLINK("https://example.invalid","external")` {
		t.Fatalf("CSV quoting did not preserve metadata: title=%q author=%q", row[5], row[14])
	}

	snapshotPath := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(snapshotPath, entries["state.db"], 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sql.Open("sqlite", snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var storedRaw []byte
	if err := snapshot.QueryRow(`SELECT raw_value FROM corrupt_records WHERE record_key='1'`).Scan(&storedRaw); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedRaw, []byte(privateRaw)) {
		t.Fatalf("database snapshot quarantine bytes = %q, want original payload", storedRaw)
	}
}
