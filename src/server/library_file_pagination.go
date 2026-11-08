package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type libraryFileCursor struct {
	Sort       string `json:"sort"`
	Key        string `json:"key"`
	FileID     string `json:"fileId"`
	FilterHash string `json:"filterHash"`
}

// This is a separate, additive endpoint. /api/library retains its existing
// source-job pagination contract for older clients.
type libraryFilePage struct {
	Jobs       []Job          `json:"jobs"`
	NextCursor string         `json:"nextCursor,omitempty"`
	TotalFiles int            `json:"totalFiles"`
	TotalJobs  int            `json:"totalJobs"`
	Categories []libraryFacet `json:"categories"`
	Channels   []libraryFacet `json:"channels"`
	Stats      libraryStats   `json:"stats"`
}

type libraryFileQuery struct {
	Filter       libraryFilter
	Sort         string
	Favorites    bool
	CollectionID string
	PlaylistOnly bool
	SourceJobID  string
	RecentOnly   bool
	RecentCutoff string
}

type fileSortSpec struct {
	Expr      string
	Direction string
	Numeric   bool
}

func libraryFileSortSpec(sort string) (fileSortSpec, bool) {
	switch sort {
	case "recent":
		return fileSortSpec{Expr: "j.created_at", Direction: "DESC"}, true
	case "oldest":
		return fileSortSpec{Expr: "j.created_at", Direction: "ASC"}, true
	case "title":
		return fileSortSpec{Expr: "lower(COALESCE(NULLIF(json_extract(li.file_json,'$.title'),''),NULLIF(json_extract(li.file_json,'$.outputName'),''),NULLIF(json_extract(li.file_json,'$.name'),''),''))", Direction: "ASC"}, true
	case "size":
		return fileSortSpec{Expr: "CAST(COALESCE(json_extract(li.file_json,'$.size'),0) AS REAL)", Direction: "DESC", Numeric: true}, true
	case "duration":
		return fileSortSpec{Expr: "CAST(COALESCE(json_extract(li.file_json,'$.durationSeconds'),0) AS REAL)", Direction: "DESC", Numeric: true}, true
	}
	return fileSortSpec{}, false
}

func fileQueryHash(query libraryFileQuery) string {
	data, _ := json.Marshal(query)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8])
}

func decodeLibraryFileCursor(value string, query libraryFileQuery) (libraryFileCursor, error) {
	if value == "" {
		return libraryFileCursor{}, nil
	}
	if len(value) > 2048 {
		return libraryFileCursor{}, fmt.Errorf("invalid media cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return libraryFileCursor{}, fmt.Errorf("invalid media cursor")
	}
	var cursor libraryFileCursor
	if err := json.Unmarshal(raw, &cursor); err != nil ||
		cursor.FileID == "" || cursor.Key == "" ||
		cursor.Sort != query.Sort || cursor.FilterHash != fileQueryHash(query) {
		return libraryFileCursor{}, fmt.Errorf("invalid or stale media cursor")
	}
	return cursor, nil
}

func encodeLibraryFileCursor(cursor libraryFileCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func libraryFileFilterClause(query libraryFileQuery) (string, []any) {
	withoutType := query.Filter
	withoutType.MediaType = ""
	clause, args := libraryFilterClause(withoutType, "li")
	if query.Filter.MediaType != "" {
		clause += " AND COALESCE(NULLIF(json_extract(li.file_json,'$.mediaType'),''),NULLIF(j.media_type,''),'video')=?"
		args = append(args, query.Filter.MediaType)
	}
	if query.Favorites {
		clause += ` AND EXISTS (SELECT 1 FROM json_each((SELECT favorites_json FROM library_organization WHERE id=1)) fav WHERE fav.value = j.source_job_id || ':' || li.file_id)`
	}
	if query.CollectionID != "" {
		clause += ` AND EXISTS (SELECT 1 FROM json_each((SELECT collections_json FROM library_organization WHERE id=1)) col, json_each(col.value,'$.itemKeys') member WHERE json_extract(col.value,'$.id')=? AND member.value=j.source_job_id || ':' || li.file_id)`
		args = append(args, query.CollectionID)
	}
	if query.PlaylistOnly {
		clause += " AND j.kind='playlist'"
	}
	if query.SourceJobID != "" {
		clause += " AND j.source_job_id=?"
		args = append(args, query.SourceJobID)
	}
	if query.RecentOnly {
		clause += " AND j.created_at>=?"
		args = append(args, query.RecentCutoff)
	}
	return clause, args
}

func (s *jobStore) loadLibraryFilePage(limit int, cursor libraryFileCursor, query libraryFileQuery) (libraryFilePage, error) {
	spec, valid := libraryFileSortSpec(query.Sort)
	if !valid {
		return libraryFilePage{}, fmt.Errorf("unsupported sort")
	}
	filterClause, filterArgs := libraryFileFilterClause(query)
	base := ` FROM library_items li JOIN library_sources j ON j.source_job_id=li.source_job_id WHERE j.status IN ('completed','partial','failed','cancelled')` + filterClause
	var totalFiles, totalJobs int
	if err := s.db.QueryRow("SELECT COUNT(*),COUNT(DISTINCT j.source_job_id)"+base, filterArgs...).Scan(&totalFiles, &totalJobs); err != nil {
		return libraryFilePage{}, err
	}
	sqlQuery := "SELECT li.file_id," + spec.Expr + base
	args := append([]any(nil), filterArgs...)
	if cursor.FileID != "" {
		cmp := ">"
		if spec.Direction == "DESC" {
			cmp = "<"
		}
		bound := "?"
		if spec.Numeric {
			bound = "CAST(? AS REAL)"
		}
		sqlQuery += " AND (" + spec.Expr + cmp + bound + " OR (" + spec.Expr + "=" + bound + " AND li.file_id" + cmp + "?))"
		args = append(args, cursor.Key, cursor.Key, cursor.FileID)
	}
	sqlQuery += " ORDER BY " + spec.Expr + " " + spec.Direction + ",li.file_id " + spec.Direction + " LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.Query(sqlQuery, args...)
	if err != nil {
		return libraryFilePage{}, err
	}
	type pageFile struct{ id, key string }
	selected := make([]pageFile, 0, limit+1)
	for rows.Next() {
		var record pageFile
		if err := rows.Scan(&record.id, &record.key); err != nil {
			_ = rows.Close()
			return libraryFilePage{}, err
		}
		selected = append(selected, record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return libraryFilePage{}, err
	}
	if err := rows.Close(); err != nil {
		return libraryFilePage{}, err
	}
	hasMore := len(selected) > limit
	if hasMore {
		selected = selected[:limit]
	}
	ids := make([]string, len(selected))
	for i, item := range selected {
		ids[i] = item.id
	}
	grouped, err := s.loadLibraryDiskJobs(nil, false, ids)
	if err != nil {
		return libraryFilePage{}, err
	}
	byID := make(map[string]Job, len(selected))
	for _, job := range grouped {
		for _, file := range job.Files {
			copy := job
			copy.Files = []mediaFile{file}
			byID[file.ID] = copy
		}
	}
	page := libraryFilePage{
		Jobs: make([]Job, 0, len(selected)), TotalFiles: totalFiles, TotalJobs: totalJobs,
		Categories: []libraryFacet{}, Channels: []libraryFacet{},
	}
	for _, item := range selected {
		if job, ok := byID[item.id]; ok {
			page.Jobs = append(page.Jobs, job)
		}
	}
	metadata := libraryPage{Categories: []libraryFacet{}, Channels: []libraryFacet{}}
	if err := s.loadLibraryMetadata(&metadata); err != nil {
		return libraryFilePage{}, err
	}
	page.Categories, page.Channels, page.Stats = metadata.Categories, metadata.Channels, metadata.Stats
	if hasMore && len(selected) > 0 {
		last := selected[len(selected)-1]
		page.NextCursor = encodeLibraryFileCursor(libraryFileCursor{
			Sort: query.Sort, Key: last.key, FileID: last.id, FilterHash: fileQueryHash(query),
		})
	}
	return page, nil
}

func (s *server) handleLibraryFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	params := r.URL.Query()
	limit := defaultLibraryPageSize
	if raw := params.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLibraryPageSize {
			fail(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	query := libraryFileQuery{Sort: params.Get("sort")}
	if query.Sort == "" {
		query.Sort = "recent"
	}
	if _, valid := libraryFileSortSpec(query.Sort); !valid {
		fail(w, http.StatusBadRequest, "sort must be recent, oldest, title, size or duration")
		return
	}
	query.Filter.Query = strings.ToLower(strings.TrimSpace(params.Get("q")))
	if len(query.Filter.Query) > 200 {
		fail(w, http.StatusBadRequest, "q must be 200 characters or fewer")
		return
	}
	query.Filter.MediaType = strings.ToLower(strings.TrimSpace(params.Get("type")))
	if query.Filter.MediaType == "all" {
		query.Filter.MediaType = ""
	}
	if query.Filter.MediaType != "" && query.Filter.MediaType != "video" && query.Filter.MediaType != "audio" {
		fail(w, http.StatusBadRequest, "type must be video or audio")
		return
	}
	query.Filter.Category = strings.TrimSpace(params.Get("category"))
	query.Filter.Channel = strings.TrimSpace(params.Get("channel"))
	query.CollectionID = params.Get("collection")
	query.SourceJobID = params.Get("source")
	if len(query.Filter.Category) > 200 || len(query.Filter.Channel) > 200 || len(query.SourceJobID) > 200 {
		fail(w, http.StatusBadRequest, "category, channel and source must be 200 characters or fewer")
		return
	}
	if query.CollectionID != "" && !organizationIDPattern.MatchString(query.CollectionID) {
		fail(w, http.StatusBadRequest, "invalid collection ID")
		return
	}
	for _, key := range []string{"favorites", "playlists", "recent"} {
		value := params.Get(key)
		if value != "" && value != "1" {
			fail(w, http.StatusBadRequest, key+" must be 1")
			return
		}
	}
	query.Favorites = params.Get("favorites") == "1"
	query.PlaylistOnly = params.Get("playlists") == "1"
	query.RecentOnly = params.Get("recent") == "1"
	if query.RecentOnly {
		query.RecentCutoff = time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	}
	cursor, err := decodeLibraryFileCursor(params.Get("cursor"), query)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.store.loadLibraryFilePage(limit, cursor, query)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not load media page")
		return
	}
	reply(w, http.StatusOK, page)
}
