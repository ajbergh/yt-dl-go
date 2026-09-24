package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

const (
	defaultLibraryPageSize = 50
	maxLibraryPageSize     = 100
)

type libraryCursor struct {
	CreatedAt string `json:"createdAt"`
	JobID     string `json:"jobId"`
}

type libraryPage struct {
	Jobs       []Job  `json:"jobs"`
	NextCursor string `json:"nextCursor,omitempty"`
	TotalJobs  int    `json:"totalJobs"`
}

func decodeLibraryCursor(value string) (libraryCursor, error) {
	var cursor libraryCursor
	if value == "" {
		return cursor, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, fmt.Errorf("invalid cursor")
	}
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.CreatedAt == "" || cursor.JobID == "" {
		return libraryCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

func encodeLibraryCursor(cursor libraryCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func (s *jobStore) loadLibraryPage(limit int, cursor libraryCursor) (libraryPage, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT j.id)
		FROM jobs j JOIN library_items li ON li.source_job_id=j.id
		WHERE j.status IN ('completed','partial','failed','cancelled')`).Scan(&total); err != nil {
		return libraryPage{}, err
	}
	query := `SELECT j.id,j.created_at FROM jobs j
		WHERE j.status IN ('completed','partial','failed','cancelled')
		AND EXISTS (SELECT 1 FROM library_items li WHERE li.source_job_id=j.id)`
	args := []any{}
	if cursor.JobID != "" {
		query += ` AND (j.created_at < ? OR (j.created_at = ? AND j.id < ?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.JobID)
	}
	query += ` ORDER BY j.created_at DESC,j.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return libraryPage{}, err
	}
	type pageJob struct{ id, createdAt string }
	selected := make([]pageJob, 0, limit+1)
	for rows.Next() {
		var job pageJob
		if err := rows.Scan(&job.id, &job.createdAt); err != nil {
			_ = rows.Close()
			return libraryPage{}, err
		}
		selected = append(selected, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return libraryPage{}, err
	}
	if err := rows.Close(); err != nil {
		return libraryPage{}, err
	}
	hasMore := len(selected) > limit
	if hasMore {
		selected = selected[:limit]
	}
	ids := make([]string, len(selected))
	for i, job := range selected {
		ids[i] = job.id
	}
	jobs, err := s.loadLibraryJobsForIDs(ids)
	if err != nil {
		return libraryPage{}, err
	}
	page := libraryPage{Jobs: jobs, TotalJobs: total}
	if hasMore && len(selected) > 0 {
		last := selected[len(selected)-1]
		page.NextCursor = encodeLibraryCursor(libraryCursor{CreatedAt: last.createdAt, JobID: last.id})
	}
	return page, nil
}

func (s *server) handleLibraryPage(w http.ResponseWriter, r *http.Request) {
	limit := defaultLibraryPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLibraryPageSize {
			fail(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	cursor, err := decodeLibraryCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.store.loadLibraryPage(limit, cursor)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not load the Library")
		return
	}
	reply(w, http.StatusOK, page)
}
