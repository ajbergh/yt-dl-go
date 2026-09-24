package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
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
	Jobs       []Job          `json:"jobs"`
	NextCursor string         `json:"nextCursor,omitempty"`
	TotalJobs  int            `json:"totalJobs"`
	Categories []libraryFacet `json:"categories"`
	Channels   []libraryFacet `json:"channels"`
	Stats      libraryStats   `json:"stats"`
}

type libraryFacet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type libraryStats struct {
	Files          int   `json:"files"`
	LogicalBytes   int64 `json:"logicalBytes"`
	ManagedBytes   int64 `json:"managedBytes"`
	PublishedBytes int64 `json:"publishedBytes"`
}

type libraryFilter struct {
	Query     string
	MediaType string
	Category  string
	Channel   string
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

func (s *jobStore) loadLibraryPage(limit int, cursor libraryCursor, filters ...libraryFilter) (libraryPage, error) {
	filter := libraryFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	filterClause, filterArgs := libraryFilterClause(filter, "li")
	var total int
	countQuery := `SELECT COUNT(DISTINCT j.source_job_id) FROM library_sources j WHERE j.status IN ('completed','partial','failed','cancelled') AND EXISTS (
		SELECT 1 FROM library_items li WHERE li.source_job_id=j.source_job_id` + filterClause + `)`
	if err := s.db.QueryRow(countQuery, filterArgs...).Scan(&total); err != nil {
		return libraryPage{}, err
	}
	query := `SELECT j.source_job_id,j.created_at FROM library_sources j
		WHERE j.status IN ('completed','partial','failed','cancelled')
		AND EXISTS (SELECT 1 FROM library_items li WHERE li.source_job_id=j.source_job_id` + filterClause + `)`
	args := append([]any(nil), filterArgs...)
	if cursor.JobID != "" {
		query += ` AND (j.created_at < ? OR (j.created_at = ? AND j.source_job_id < ?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.JobID)
	}
	query += ` ORDER BY j.created_at DESC,j.source_job_id DESC LIMIT ?`
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
	page := libraryPage{Jobs: jobs, TotalJobs: total, Categories: []libraryFacet{}, Channels: []libraryFacet{}}
	if err := s.loadLibraryMetadata(&page); err != nil {
		return libraryPage{}, err
	}
	if hasMore && len(selected) > 0 {
		last := selected[len(selected)-1]
		page.NextCursor = encodeLibraryCursor(libraryCursor{CreatedAt: last.createdAt, JobID: last.id})
	}
	return page, nil
}

func libraryFilterClause(filter libraryFilter, itemAlias string) (string, []any) {
	var clauses []string
	var args []any
	if filter.MediaType == "video" {
		clauses = append(clauses, `(j.media_type='video' OR j.media_type='')`)
	} else if filter.MediaType != "" {
		clauses = append(clauses, `j.media_type=?`)
		args = append(args, filter.MediaType)
	}
	if filter.Category != "" {
		clauses = append(clauses, `((j.category<>'' AND j.category=?) OR (j.category='' AND COALESCE(NULLIF(json_extract(`+itemAlias+`.file_json,'$.category'),''),'Uncategorized')=?))`)
		args = append(args, filter.Category, filter.Category)
	}
	if filter.Channel != "" {
		clauses = append(clauses, `COALESCE(NULLIF(TRIM(json_extract(`+itemAlias+`.file_json,'$.author')),''),'Unknown channel')=?`)
		args = append(args, filter.Channel)
	}
	if filter.Query != "" {
		if utf8.RuneCountInString(filter.Query) >= 3 && !strings.ContainsRune(filter.Query, '\x00') {
			phrase := "\"" + strings.ReplaceAll(filter.Query, "\"", "\"\"") + "\""
			clauses = append(clauses, `(j.rowid IN (SELECT rowid FROM library_search_jobs WHERE library_search_jobs MATCH ?) OR `+itemAlias+`.rowid IN (SELECT rowid FROM library_search_files WHERE library_search_files MATCH ?))`)
			args = append(args, phrase, phrase)
		}
		clauses = append(clauses, `instr(lower(COALESCE(j.title,'')||' '||COALESCE(j.url,'')||' '||COALESCE(j.category,'')||' '||COALESCE(j.audio_format,'')||' '||COALESCE(j.subtitle_language,'')||' '||`+itemAlias+`.file_json),?)>0`)
		args = append(args, filter.Query)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

func (s *jobStore) loadLibraryMetadata(page *libraryPage) error {
	rows, err := s.db.Query(`SELECT COALESCE(NULLIF(j.category,''),NULLIF(json_extract(li.file_json,'$.category'),''),'Uncategorized'),COUNT(*)
		FROM library_items li JOIN library_sources j ON j.source_job_id=li.source_job_id
		WHERE j.status IN ('completed','partial','failed','cancelled') GROUP BY 1 ORDER BY 1 COLLATE NOCASE`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var facet libraryFacet
		if err := rows.Scan(&facet.Value, &facet.Count); err != nil {
			_ = rows.Close()
			return err
		}
		page.Categories = append(page.Categories, facet)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = s.db.Query(`SELECT COALESCE(NULLIF(TRIM(json_extract(li.file_json,'$.author')),''),'Unknown channel'),COUNT(*)
		FROM library_items li JOIN library_sources j ON j.source_job_id=li.source_job_id
		WHERE j.status IN ('completed','partial','failed','cancelled') GROUP BY 1 ORDER BY 1 COLLATE NOCASE`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var facet libraryFacet
		if err := rows.Scan(&facet.Value, &facet.Count); err != nil {
			_ = rows.Close()
			return err
		}
		page.Channels = append(page.Channels, facet)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.db.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CAST(json_extract(file_json,'$.size') AS INTEGER)+COALESCE(CAST(json_extract(file_json,'$.subtitle.size') AS INTEGER),0)),0),
		COALESCE(SUM(CASE WHEN json_extract(file_json,'$.managedAvailable')=1 THEN CAST(json_extract(file_json,'$.size') AS INTEGER) ELSE 0 END + CASE WHEN json_extract(file_json,'$.subtitle.managedAvailable')=1 THEN CAST(json_extract(file_json,'$.subtitle.size') AS INTEGER) ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN json_extract(file_json,'$.publishedAvailable')=1 AND COALESCE(json_extract(file_json,'$.outputRelativePath'),'')<>'' THEN CAST(json_extract(file_json,'$.size') AS INTEGER) ELSE 0 END + CASE WHEN json_extract(file_json,'$.subtitle.publishedAvailable')=1 AND COALESCE(json_extract(file_json,'$.subtitle.outputRelativePath'),'')<>'' THEN CAST(json_extract(file_json,'$.subtitle.size') AS INTEGER) ELSE 0 END),0)
		FROM library_items li JOIN library_sources j ON j.source_job_id=li.source_job_id
		WHERE j.status IN ('completed','partial','failed','cancelled')`).Scan(&page.Stats.Files, &page.Stats.LogicalBytes, &page.Stats.ManagedBytes, &page.Stats.PublishedBytes)
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
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(query) > 200 {
		fail(w, http.StatusBadRequest, "q must be 200 characters or fewer")
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if mediaType == "all" {
		mediaType = ""
	}
	if mediaType != "" && mediaType != "video" && mediaType != "audio" {
		fail(w, http.StatusBadRequest, "type must be video or audio")
		return
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	if len(category) > 200 || len(channel) > 200 {
		fail(w, http.StatusBadRequest, "category and channel must be 200 characters or fewer")
		return
	}
	filter := libraryFilter{Query: query, MediaType: mediaType, Category: category, Channel: channel}
	page, err := s.store.loadLibraryPage(limit, cursor, filter)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not load the Library")
		return
	}
	reply(w, http.StatusOK, page)
}
