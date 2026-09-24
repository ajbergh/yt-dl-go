package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestLibraryPaginationKeepsJobGroupsAndStableCursor(t *testing.T) {
	s := newPersistenceTestServer(t)
	createdAt := "2026-09-24T12:00:00Z"
	for _, id := range []string{"library-page-a", "library-page-b"} {
		job := &jobState{Job: Job{
			ID: id, URL: testVideo, Kind: "playlist", Quality: "best", Status: "completed", CreatedAt: createdAt,
			Files: []mediaFile{
				{ID: id + "-one", Name: "one.mp4", Size: 10, SourceItemIndex: 1},
				{ID: id + "-two", Name: "two.mp4", Size: 20, SourceItemIndex: 2},
			},
		}, dir: filepath.Join(s.cfg.root, id)}
		if err := s.store.saveJob(job); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.store.loadLibraryPage(1, libraryCursor{})
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalJobs != 2 || len(first.Jobs) != 1 || len(first.Jobs[0].Files) != 2 || first.NextCursor == "" {
		t.Fatalf("first library page = %+v", first)
	}
	secondCursor, err := decodeLibraryCursor(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.store.loadLibraryPage(1, secondCursor)
	if err != nil {
		t.Fatal(err)
	}
	if second.TotalJobs != 2 || len(second.Jobs) != 1 || second.Jobs[0].ID == first.Jobs[0].ID || len(second.Jobs[0].Files) != 2 || second.NextCursor != "" {
		t.Fatalf("second library page = %+v", second)
	}
}

func TestLibraryPaginationRejectsInvalidLimitsAndCursors(t *testing.T) {
	s := newPersistenceTestServer(t)
	for _, path := range []string{"/api/library?limit=0", "/api/library?limit=101", "/api/library?cursor=not-a-cursor"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
		response := httptest.NewRecorder()
		s.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s returned %d, want 400", path, response.Code)
		}
	}
}
