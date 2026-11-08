package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func portableOrganizationTestArchive(t *testing.T, manifest libraryExportManifest, records []libraryExportRecord, organization *libraryExportOrganization) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	if err := addExportJSON(writer, "manifest.json", manifest); err != nil {
		t.Fatal(err)
	}
	document := libraryExportDocument{Manifest: manifest, Records: records, Organization: organization}
	if err := addExportJSON(writer, "library.json", document); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func importPortableOrganizationArchive(t *testing.T, s *server, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/library/import", bytes.NewReader(archive))
	request.Header.Set("Content-Type", "application/zip")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	return response
}

func TestPortableOrganizationExportsOnlyIndexedReferences(t *testing.T) {
	manifest, records := sampleLibraryImport()
	_ = manifest
	origin := libraryOrganization{
		Revision: 8,
		Favorites: []string{"original-job:original-file-1", "removed-job:removed-file"},
		Collections: []libraryCollection{
			{ID: "c_saved", Name: "Saved", ItemKeys: []string{"original-job:original-file-2", "removed-job:removed-file"}},
			{ID: "c_empty", Name: "Empty", ItemKeys: []string{}},
		},
	}
	out := portableLibraryOrganization(origin, records)
	if len(out.Favorites) != 1 || out.Favorites[0] != "original-job:original-file-1" {
		t.Fatalf("exported favorites contain stale entries: %+v", out.Favorites)
	}
	if len(out.Collections) != 2 || len(out.Collections[0].ItemKeys) != 1 || out.Collections[0].ItemKeys[0] != "original-job:original-file-2" || len(out.Collections[1].ItemKeys) != 0 {
		t.Fatalf("exported collections lost valid memberships or kept stale ones: %+v", out.Collections)
	}
}

func TestLibraryOrganizationPortableImportMergesAndIsIdempotent(t *testing.T) {
	s := newPersistenceTestServer(t)
	existing := libraryOrganization{
		Revision: 0,
		Favorites: []string{"local:favorite"},
		Collections: []libraryCollection{
			{ID: "c_local", Name: "Watch Later", ItemKeys: []string{"local:favorite"}},
			{ID: "c_collision", Name: "Personal", ItemKeys: []string{}},
		},
	}
	if _, _, err := s.store.saveLibraryOrganization(existing); err != nil {
		t.Fatal(err)
	}
	manifest, records := sampleLibraryImport()
	incoming := &libraryExportOrganization{
		Favorites: []string{"original-job:original-file-1", "unavailable:missing"},
		Collections: []libraryCollection{
			{ID: "c_other", Name: "watch later", ItemKeys: []string{"original-job:original-file-1", "original-job:original-file-2"}},
			{ID: "c_collision", Name: "Imported", ItemKeys: []string{"original-job:original-file-2", "unavailable:missing"}},
			{ID: "c_empty", Name: "Empty", ItemKeys: []string{}},
		},
	}
	archive := portableOrganizationTestArchive(t, manifest, records, incoming)
	first := importPortableOrganizationArchive(t, s, archive)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"organizationUpdated":true`) {
		t.Fatalf("first portable import: %d %s", first.Code, first.Body.String())
	}
	got, err := s.store.loadLibraryOrganization()
	if err != nil {
		t.Fatal(err)
	}
	mappedFirst := importedSourceID("original-job") + ":" + importedFileID("original-job", "original-file-1")
	mappedSecond := importedSourceID("original-job") + ":" + importedFileID("original-job", "original-file-2")
	if got.Revision != 2 || len(got.Favorites) != 2 || got.Favorites[0] != "local:favorite" || got.Favorites[1] != mappedFirst {
		t.Fatalf("import replaced local favorites or did not map identities: %+v", got)
	}
	if len(got.Collections) != 4 || got.Collections[0].ID != "c_local" || got.Collections[0].Name != "Watch Later" ||
		len(got.Collections[0].ItemKeys) != 3 || got.Collections[0].ItemKeys[1] != mappedFirst || got.Collections[0].ItemKeys[2] != mappedSecond {
		t.Fatalf("case-insensitive collection merge was not additive: %+v", got.Collections)
	}
	if got.Collections[1].Name != "Personal" || got.Collections[2].Name != "Imported" || got.Collections[2].ID == "c_collision" ||
		len(got.Collections[2].ItemKeys) != 1 || got.Collections[2].ItemKeys[0] != mappedSecond || got.Collections[3].Name != "Empty" {
		t.Fatalf("imported ID collision replaced a collection or retained orphan media: %+v", got.Collections)
	}
	second := importPortableOrganizationArchive(t, s, archive)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"idempotent":true`) ||
		!strings.Contains(second.Body.String(), `"organizationUpdated":false`) {
		t.Fatalf("repeated portable import was not idempotent: %d %s", second.Code, second.Body.String())
	}
	stable, err := s.store.loadLibraryOrganization()
	if err != nil || stable.Revision != got.Revision || len(stable.Collections) != len(got.Collections) {
		t.Fatalf("repeat import modified organization: %+v %v", stable, err)
	}
}

func TestPortableLibraryZIPRoundTripPreservesFavoritesAndCollections(t *testing.T) {
	source := newPersistenceTestServer(t)
	job := &jobState{Job: Job{
		ID: "source-portable", URL: testVideo, Kind: "video", Quality: "best", MediaType: "video",
		Status: "completed", Title: "Portable video", CreatedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Files: []mediaFile{{
			ID: "portable-file", Name: "video.mp4", Size: 100, MimeType: "video/mp4", Title: "Video",
			OutputPath: filepath.Join(source.cfg.root, "source-portable", "video.mp4"),
			OutputRelativePath: "Videos/video.mp4", ManagedAvailable: true, PublishedAvailable: true, SourceItemIndex: 1,
		}},
	}, dir: filepath.Join(source.cfg.root, "source-portable"), done: time.Now()}
	if err := source.store.saveJob(job); err != nil {
		t.Fatal(err)
	}
	saved, conflict, err := source.store.saveLibraryOrganization(libraryOrganization{
		Revision: 0,
		Favorites: []string{"source-portable:portable-file", "deleted-job:deleted-file"},
		Collections: []libraryCollection{
			{ID: "c_videos", Name: "My Videos", ItemKeys: []string{"source-portable:portable-file"}},
			{ID: "c_empty", Name: "Unsorted", ItemKeys: []string{}},
		},
	})
	if err != nil || conflict || saved.Revision != 1 {
		t.Fatalf("seed source organization: %+v %v %v", saved, conflict, err)
	}
	filePath, cleanup, err := source.createLibraryExport()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	archiveBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, doc, err := parseLibraryImportArchive(archiveBytes)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != libraryExportFormatVersion || doc.Organization == nil ||
		len(doc.Organization.Favorites) != 1 || len(doc.Organization.Collections) != 2 {
		t.Fatalf("export did not include filtered portable organization: %+v", doc.Organization)
	}
	dest := newPersistenceTestServer(t)
	first := importPortableOrganizationArchive(t, dest, archiveBytes)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"organizationUpdated":true`) {
		t.Fatalf("export/import round trip: %d %s", first.Code, first.Body.String())
	}
	result, err := dest.store.loadLibraryOrganization()
	if err != nil {
		t.Fatal(err)
	}
	mapped := importedSourceID("source-portable") + ":" + importedFileID("source-portable", "portable-file")
	if len(result.Favorites) != 1 || result.Favorites[0] != mapped ||
		len(result.Collections) != 2 || len(result.Collections[0].ItemKeys) != 1 || result.Collections[0].ItemKeys[0] != mapped {
		t.Fatalf("imported organization did not point to imported files: %+v", result)
	}
	jobs, err := dest.store.loadLibraryJobs()
	if err != nil || len(jobs) != 1 || len(jobs[0].Files) != 1 || jobs[0].Files[0].PublishedAvailable {
		t.Fatalf("portable import created live media or lost metadata: %+v %v", jobs, err)
	}
	repeat := importPortableOrganizationArchive(t, dest, archiveBytes)
	if repeat.Code != http.StatusOK || !strings.Contains(repeat.Body.String(), `"idempotent":true`) {
		t.Fatalf("repeated real export import: %d %s", repeat.Code, repeat.Body.String())
	}
}

func TestLegacyV1ArchiveImportsWithoutChangingOrganization(t *testing.T) {
	s := newPersistenceTestServer(t)
	seed := libraryOrganization{Revision: 0, Favorites: []string{"local:favorite"}, Collections: []libraryCollection{}}
	if _, _, err := s.store.saveLibraryOrganization(seed); err != nil {
		t.Fatal(err)
	}
	manifest, records := sampleLibraryImport()
	manifest.FormatVersion = libraryLegacyExportFormatVersion
	first := importPortableOrganizationArchive(t, s, portableOrganizationTestArchive(t, manifest, records, nil))
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"imported":2`) ||
		!strings.Contains(first.Body.String(), `"organizationUpdated":false`) {
		t.Fatalf("legacy archive import: %d %s", first.Code, first.Body.String())
	}
	org, err := s.store.loadLibraryOrganization()
	if err != nil || org.Revision != 1 || len(org.Favorites) != 1 || org.Favorites[0] != "local:favorite" {
		t.Fatalf("legacy archive changed organization: %+v %v", org, err)
	}
}

func TestPortableLibraryImportRejectsInvalidOrganizationBeforeMutatingRecords(t *testing.T) {
	s := newPersistenceTestServer(t)
	manifest, records := sampleLibraryImport()
	incoming := &libraryExportOrganization{
		Favorites: []string{"original-job:original-file-1"},
		Collections: []libraryCollection{
			{ID: "not a valid id!", Name: "Invalid", ItemKeys: []string{}},
		},
	}
	response := importPortableOrganizationArchive(t, s, portableOrganizationTestArchive(t, manifest, records, incoming))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid collection should reject entire import: %d %s", response.Code, response.Body.String())
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 0 {
		t.Fatalf("invalid metadata partially imported media: %+v %v", jobs, err)
	}
}

func TestPortableLibraryImportRollsBackRecordsWhenCollectionLimitExceeded(t *testing.T) {
	s := newPersistenceTestServer(t)
	collections := make([]libraryCollection, 200)
	for i := range collections {
		collections[i] = libraryCollection{ID: fmt.Sprintf("c_%03d", i), Name: fmt.Sprintf("Existing %03d", i), ItemKeys: []string{}}
	}
	if _, _, err := s.store.saveLibraryOrganization(libraryOrganization{Revision: 0, Favorites: []string{}, Collections: collections}); err != nil {
		t.Fatal(err)
	}
	manifest, records := sampleLibraryImport()
	incoming := &libraryExportOrganization{
		Favorites: []string{"original-job:original-file-1"},
		Collections: []libraryCollection{{ID: "c_new", Name: "Over Limit", ItemKeys: []string{"original-job:original-file-1"}}},
	}
	response := importPortableOrganizationArchive(t, s, portableOrganizationTestArchive(t, manifest, records, incoming))
	if response.Code != http.StatusConflict {
		t.Fatalf("over-limit organization should rollback import, got %d %s", response.Code, response.Body.String())
	}
	jobs, err := s.store.loadLibraryJobs()
	if err != nil || len(jobs) != 0 {
		t.Fatalf("failed transaction inserted media rows: %+v %v", jobs, err)
	}
	org, err := s.store.loadLibraryOrganization()
	if err != nil || org.Revision != 1 || len(org.Favorites) != 0 || len(org.Collections) != 200 {
		t.Fatalf("failed transaction modified existing organization: %+v %v", org, err)
	}
}
