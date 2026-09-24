package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryPaginationHandlesMoreThanTenThousandFilesWithoutRetainingFinishedJobs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	config := config{
		addr: "127.0.0.1:8080", root: root, token: "",
		origins: map[string]bool{}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 8, maxBytes: 1 << 20, timeout: time.Minute, retain: time.Minute,
	}
	s, err := newServer(config)
	if err != nil {
		t.Fatal(err)
	}
	firstServerStopped := false
	t.Cleanup(func() {
		if !firstServerStopped {
			s.stop()
		}
	})

	const (
		sourceCount    = 101
		filesPerSource = 100
		fileSize       = 10
	)
	if err := seedLibraryScaleFixture(s.store, sourceCount, filesPerSource); err != nil {
		t.Fatal(err)
	}
	sentinel := &jobState{Job: Job{
		ID: "terminal-sentinel", URL: testVideo, Kind: "video", Quality: "best",
		Status: "completed", CreatedAt: "2026-09-24T12:00:00Z",
	}, dir: filepath.Join(root, "terminal-sentinel")}
	if err := s.store.saveJob(sentinel); err != nil {
		t.Fatal(err)
	}
	if len(s.jobs) != 0 {
		t.Fatalf("finished jobs retained before restart: %d", len(s.jobs))
	}

	s.stop()
	firstServerStopped = true
	s, err = newServer(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.stop)
	if len(s.jobs) != 0 {
		t.Fatalf("finished jobs loaded into memory after restart: %d", len(s.jobs))
	}

	first := requestLibraryPage(t, s, "/api/library?limit=100")
	if first.TotalJobs != sourceCount || len(first.Jobs) != 100 || first.NextCursor == "" {
		t.Fatalf("first page returned %d jobs, total %d, cursor present %t", len(first.Jobs), first.TotalJobs, first.NextCursor != "")
	}
	if first.Jobs[0].ID != "scale-source-100" || first.Jobs[len(first.Jobs)-1].ID != "scale-source-001" {
		t.Fatalf("first page order = %q through %q", first.Jobs[0].ID, first.Jobs[len(first.Jobs)-1].ID)
	}
	seen := make(map[string]bool, sourceCount)
	for _, job := range first.Jobs {
		if len(job.Files) != filesPerSource {
			t.Fatalf("source %s returned %d files, want %d", job.ID, len(job.Files), filesPerSource)
		}
		seen[job.ID] = true
	}
	wantFiles := sourceCount * filesPerSource
	wantBytes := int64(wantFiles * fileSize)
	if first.Stats.Files != wantFiles || first.Stats.LogicalBytes != wantBytes || first.Stats.ManagedBytes != wantBytes || first.Stats.PublishedBytes != wantBytes {
		t.Fatalf("global Library stats = %+v, want %d files and %d bytes in each total", first.Stats, wantFiles, wantBytes)
	}
	if facetCount(first.Categories, "Scale A") != 5100 || facetCount(first.Categories, "Scale B") != 5000 {
		t.Fatalf("global category facets = %+v", first.Categories)
	}
	if facetCount(first.Channels, "Scale Channel") != sourceCount*filesPerSource {
		t.Fatalf("global channel facets = %+v", first.Channels)
	}

	second := requestLibraryPage(t, s, "/api/library?limit=100&cursor="+first.NextCursor)
	if second.TotalJobs != sourceCount || len(second.Jobs) != 1 || second.Jobs[0].ID != "scale-source-000" || len(second.Jobs[0].Files) != filesPerSource || second.NextCursor != "" {
		t.Fatalf("second page = %d jobs, first %q, cursor present %t", len(second.Jobs), firstLibraryJobID(second.Jobs), second.NextCursor != "")
	}
	if seen[second.Jobs[0].ID] {
		t.Fatalf("cursor repeated source %s", second.Jobs[0].ID)
	}

	filtered := requestLibraryPage(t, s, "/api/library?limit=100&q=needle")
	if filtered.TotalJobs != 1 || len(filtered.Jobs) != 1 || filtered.Jobs[0].ID != "scale-source-000" || len(filtered.Jobs[0].Files) != filesPerSource {
		t.Fatalf("search result did not preserve its full source group: total=%d jobs=%d", filtered.TotalJobs, len(filtered.Jobs))
	}
	if len(s.jobs) != 0 {
		t.Fatalf("Library reads retained finished jobs in memory: %d", len(s.jobs))
	}
}

func seedLibraryScaleFixture(store *jobStore, sourceCount, filesPerSource int) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	sourceStmt, err := tx.Prepare(`INSERT INTO library_sources (
		source_job_id,url,kind,quality,video_strategy,allow_360p_fallback,media_type,audio_format,audio_bitrate,
		subtitle_language,subtitle_format,split_by_chapter,status,title,current_item,completed_count,total_count,error,
		created_at,note,category,storage_mode,queue_position,search_text,download_location
	) VALUES(?,?,?,?,?,0,?,'',0,'','',0,'completed',?,'',?,?, '',?,'',?,'managed-published',0,?,?)`)
	if err != nil {
		return err
	}
	defer sourceStmt.Close()
	itemStmt, err := tx.Prepare(`INSERT INTO library_items(file_id,source_job_id,source_item_index,file_json,output_path,created_at) VALUES(?,?,?,?,?,1)`)
	if err != nil {
		return err
	}
	defer itemStmt.Close()
	for sourceIndex := 0; sourceIndex < sourceCount; sourceIndex++ {
		sourceID := fmt.Sprintf("scale-source-%03d", sourceIndex)
		category := "Scale A"
		if sourceIndex%2 != 0 {
			category = "Scale B"
		}
		mediaType := "video"
		if sourceIndex == 0 {
			mediaType = "audio"
		}
		title := fmt.Sprintf("scale source %03d", sourceIndex)
		if sourceIndex == 0 {
			title = "needle " + title
		}
		searchText := title + " " + category + " Scale Channel"
		if _, err := sourceStmt.Exec(sourceID, testVideo, "playlist", "best", "best", mediaType, title, filesPerSource, filesPerSource, "2026-09-24T12:00:00Z", category, searchText, filepath.Join("published", sourceID)); err != nil {
			return err
		}
		for fileIndex := 1; fileIndex <= filesPerSource; fileIndex++ {
			fileID := fmt.Sprintf("%s-file-%03d", sourceID, fileIndex)
			fileJSON, err := json.Marshal(mediaFile{
				ID: fileID, Name: fileID + ".mp4", Size: 10, MimeType: "video/mp4",
				Author: "Scale Channel", Category: category, MediaType: mediaType,
				OutputRelativePath: filepath.Join("published", fileID+".mp4"),
				ManagedAvailable:   true, PublishedAvailable: true, SourceItemIndex: fileIndex,
			})
			if err != nil {
				return err
			}
			if _, err := itemStmt.Exec(fileID, sourceID, fileIndex, string(fileJSON), filepath.Join("published", fileID+".mp4")); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func requestLibraryPage(t *testing.T, s *server, path string) libraryPage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
	response := httptest.NewRecorder()
	s.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
	}
	var page libraryPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatalf("decode GET %s response: %v", path, err)
	}
	return page
}

func facetCount(facets []libraryFacet, value string) int {
	for _, facet := range facets {
		if facet.Value == value {
			return facet.Count
		}
	}
	return 0
}

func firstLibraryJobID(jobs []Job) string {
	if len(jobs) == 0 {
		return ""
	}
	return jobs[0].ID
}
