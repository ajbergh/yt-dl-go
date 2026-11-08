package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func seedFilePages(t *testing.T, s *server) {
	t.Helper()
	for _, spec := range []struct {
		id, title, date, kind string
		files []mediaFile
	}{
		{
			id: "global-sort-a", title: "A playlist", date: "2026-10-01T12:00:00Z", kind: "playlist",
			files: []mediaFile{
				{ID: "global-file-z", Name: "z.mp4", Title: "Zulu", Size: 50, DurationSeconds: 1, MediaType: "video", Author: "Channel Z", Category: "Video"},
				{ID: "global-file-a", Name: "a.mp3", Title: "Alpha", Size: 500, DurationSeconds: 8, MediaType: "audio", Author: "Channel A", Category: "Audio"},
				{ID: "global-file-m", Name: "m.mp4", Title: "Mike", Size: 150, DurationSeconds: 3, MediaType: "video", Author: "Channel M", Category: "Video"},
			},
		},
		{
			id: "global-sort-b", title: "B single", date: "2026-11-01T12:00:00Z", kind: "video",
			files: []mediaFile{
				{ID: "global-file-b", Name: "b.mp4", Title: "Bravo", Size: 200, DurationSeconds: 6, MediaType: "video", Author: "Channel B", Category: "Video"},
			},
		},
	} {
		j := &jobState{Job: Job{
			ID: spec.id, URL: testVideo, Kind: spec.kind, Quality: "best",
			MediaType: "video", Status: "completed", Title: spec.title, CreatedAt: spec.date,
			Files: spec.files,
		}, dir: filepath.Join(s.cfg.root, spec.id)}
		if err := s.store.saveJob(j); err != nil {
			t.Fatal(err)
		}
	}
}

func filePageIDs(page libraryFilePage) []string {
	ids := make([]string, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		if len(job.Files) == 1 {
			ids = append(ids, job.Files[0].ID)
		}
	}
	return ids
}

func TestGlobalMediaPaginationReturnsOnlySelectedFilesAndStableSort(t *testing.T) {
	s := newPersistenceTestServer(t)
	seedFilePages(t, s)
	for _, test := range []struct {
		sort string
		want []string
	}{
		{"recent", []string{"global-file-b", "global-file-a", "global-file-m", "global-file-z"}},
		{"oldest", []string{"global-file-a", "global-file-m", "global-file-z", "global-file-b"}},
		{"title", []string{"global-file-a", "global-file-b", "global-file-m", "global-file-z"}},
		{"size", []string{"global-file-a", "global-file-b", "global-file-m", "global-file-z"}},
		{"duration", []string{"global-file-a", "global-file-b", "global-file-m", "global-file-z"}},
	} {
		seen := []string{}
		cursor := ""
		for i := 0; i < 5; i++ {
			url := "/api/library/files?limit=1&sort=" + test.sort
			if cursor != "" {
				url += "&cursor=" + cursor
			}
			page := requestLibraryFilesPage(t, s, url)
			if page.TotalFiles != 4 || page.TotalJobs != 2 || len(page.Jobs) != 1 || len(page.Jobs[0].Files) != 1 {
				t.Fatalf("sort=%s page=%d contains full jobs or wrong count: %+v", test.sort, i, page)
			}
			seen = append(seen, filePageIDs(page)...)
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		if strings.Join(seen, ",") != strings.Join(test.want, ",") {
			t.Fatalf("sort=%s got %v, want %v", test.sort, seen, test.want)
		}
	}
	first := requestLibraryFilesPage(t, s, "/api/library/files?limit=1&sort=title")
	for _, q := range []string{
		"/api/library/files?limit=1&sort=size&cursor=" + first.NextCursor,
		"/api/library/files?limit=1&sort=title&type=audio&cursor=" + first.NextCursor,
		"/api/library/files?limit=1&sort=title&cursor=garbage",
		"/api/library/files?limit=101",
		"/api/library/files?sort=invalid",
		"/api/library/files?favorites=yes",
		"/api/library/files?collection=invalid%20id",
	} {
		r := request(s, http.MethodGet, q, "", nil)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", q, r.Code)
		}
	}
	if r := request(s, http.MethodPost, "/api/library/files", "", nil); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected method = %d", r.Code)
	}
	if r := request(s, http.MethodGet, "/api/library/files", "", map[string]string{"Authorization": "Bearer wrong"}); r.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized media file list = %d", r.Code)
	}
}

func TestGlobalMediaFilesFilterAtFileGrainIncludingFavoritesAndCollections(t *testing.T) {
	s := newPersistenceTestServer(t)
	seedFilePages(t, s)
	_, conflict, err := s.store.saveLibraryOrganization(libraryOrganization{
		Revision: 0,
		Favorites: []string{"global-sort-a:global-file-a", "global-sort-b:global-file-b"},
		Collections: []libraryCollection{{
			ID: "collection-media", Name: "Playlist", ItemKeys: []string{"global-sort-a:global-file-m", "global-sort-b:global-file-b"},
		}},
	})
	if err != nil || conflict {
		t.Fatalf("save library organization: %v, conflict=%v", err, conflict)
	}
	checks := []struct{ params string; want []string }{
		{"type=audio", []string{"global-file-a"}},
		{"type=video&sort=title", []string{"global-file-b", "global-file-m", "global-file-z"}},
		{"channel=Channel+A", []string{"global-file-a"}},
		{"q=Alpha", []string{"global-file-a"}},
		{"favorites=1&sort=title", []string{"global-file-a", "global-file-b"}},
		{"collection=collection-media&sort=title", []string{"global-file-b", "global-file-m"}},
		{"playlists=1&sort=title", []string{"global-file-a", "global-file-m", "global-file-z"}},
		{"source=global-sort-b", []string{"global-file-b"}},
		{"category=Audio", []string{"global-file-a"}},
	}
	for _, test := range checks {
		page := requestLibraryFilesPage(t, s, "/api/library/files?limit=100&"+test.params)
		if got := strings.Join(filePageIDs(page), ","); got != strings.Join(test.want, ",") {
			t.Errorf("%s got [%s], want %v", test.params, got, test.want)
		}
		if page.TotalFiles != len(test.want) || page.NextCursor != "" {
			t.Errorf("%s metadata inconsistent: %+v", test.params, page)
		}
	}
}

func TestGlobalMediaPaginationBoundsTenThousandFilePlaylist(t *testing.T) {
	s := newPersistenceTestServer(t)
	if err := seedLibraryScaleFixture(s.store, 1, 10100); err != nil {
		t.Fatal(err)
	}
	page := requestLibraryFilesPage(t, s, "/api/library/files?limit=50&sort=title")
	if page.TotalFiles != 10100 || len(page.Jobs) != 50 || page.NextCursor == "" {
		t.Fatalf("first media page unexpectedly materialized playlist: total=%d jobs=%d cursor=%t", page.TotalFiles, len(page.Jobs), page.NextCursor != "")
	}
	for _, job := range page.Jobs {
		if len(job.Files) != 1 {
			t.Fatalf("expected exactly one media entry per page job, got %d", len(job.Files))
		}
	}
	next := requestLibraryFilesPage(t, s, "/api/library/files?limit=50&sort=title&cursor="+page.NextCursor)
	if next.TotalFiles != 10100 || len(next.Jobs) != 50 || page.Jobs[49].Files[0].ID == next.Jobs[0].Files[0].ID {
		t.Fatalf("cursor skipped or repeated files: first=%v next=%v", filePageIDs(page), filePageIDs(next))
	}
}

func requestLibraryFilesPage(t *testing.T, s *server, path string) libraryFilePage {
	t.Helper()
	r := request(s, http.MethodGet, path, "", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, r.Code, r.Body.String())
	}
	var page libraryFilePage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}
