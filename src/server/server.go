// server.go implements request validation, API routing, in-memory job control,
// and coordination with the SQLite store and concurrent download scheduler.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https://i.ytimg.com https://ytimg.com https://*.ytimg.com https://ggpht.com https://*.ggpht.com; " +
	"media-src 'self' blob:; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

type mediaFile struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	Size                    int64          `json:"size"`
	Height                  int            `json:"height"`
	MimeType                string         `json:"mimeType"`
	Title                   string         `json:"title,omitempty"`
	Author                  string         `json:"author,omitempty"`
	DurationSeconds         int64          `json:"durationSeconds,omitempty"`
	ThumbnailURL            string         `json:"thumbnailUrl,omitempty"`
	ThumbnailLocalAvailable bool           `json:"thumbnailLocalAvailable,omitempty"`
	ThumbnailMimeType       string         `json:"thumbnailMimeType,omitempty"`
	PublishDate             string         `json:"publishDate,omitempty"`
	Category                string         `json:"category,omitempty"`
	MediaType               string         `json:"mediaType,omitempty"`
	OutputName              string         `json:"outputName,omitempty"`
	OutputPath              string         `json:"-"`
	OutputRelativePath      string         `json:"outputRelativePath,omitempty"`
	ManagedAvailable        bool           `json:"managedAvailable"`
	PublishedAvailable      bool           `json:"publishedAvailable"`
	Subtitle                *subtitleFile  `json:"subtitle,omitempty"`
	SubtitleError           string         `json:"subtitleError,omitempty"`
	Chapters                []mediaChapter `json:"chapters,omitempty"`
	SourceItemIndex         int            `json:"sourceItemIndex,omitempty"`
	ChapterIndex            int            `json:"chapterIndex,omitempty"`
	ChapterTitle            string         `json:"chapterTitle,omitempty"`
	naming                  namingValues
}

type itemFailure struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

type queueItem struct {
	Index            int      `json:"index"`
	PlaylistIndex    int      `json:"playlistIndex,omitempty"`
	VideoID          string   `json:"videoId,omitempty"`
	Title            string   `json:"title"`
	Author           string   `json:"author,omitempty"`
	DurationSeconds  int64    `json:"durationSeconds,omitempty"`
	ThumbnailURL     string   `json:"thumbnailUrl,omitempty"`
	Status           string   `json:"status"`
	Progress         *float64 `json:"progress"`
	DownloadedBytes  int64    `json:"downloadedBytes"`
	TotalBytes       int64    `json:"totalBytes"`
	SpeedBytesPerSec int64    `json:"speedBytesPerSec"`
	ETASeconds       int64    `json:"etaSeconds"`
	Error            string   `json:"error,omitempty"`
	FileID           string   `json:"fileId,omitempty"`
	FileIDs          []string `json:"fileIds,omitempty"`
	RetryRequested   bool     `json:"retryRequested,omitempty"`
}

type Job struct {
	ID                string        `json:"id"`
	URL               string        `json:"url"`
	Kind              string        `json:"kind"`
	Quality           string        `json:"quality"`
	VideoStrategy     string        `json:"videoStrategy,omitempty"`
	Allow360pFallback bool          `json:"allow360pFallback"`
	MediaType         string        `json:"mediaType"`
	AudioBitrate      string        `json:"audioBitrate,omitempty"`
	AudioFormat       string        `json:"audioFormat,omitempty"`
	SplitByChapter    bool          `json:"splitByChapter"`
	SubtitleLanguage  string        `json:"subtitleLanguage,omitempty"`
	SubtitleFormat    string        `json:"subtitleFormat,omitempty"`
	Status            string        `json:"status"`
	Title             string        `json:"title"`
	Progress          *float64      `json:"progress"`
	CurrentItem       string        `json:"currentItem"`
	CompletedCount    int           `json:"completedCount"`
	TotalCount        *int          `json:"totalCount"`
	Files             []mediaFile   `json:"files"`
	Items             []queueItem   `json:"items"`
	Error             string        `json:"error"`
	CreatedAt         string        `json:"createdAt"`
	Note              string        `json:"note"`
	Failures          []itemFailure `json:"failures"`
	DownloadedBytes   int64         `json:"downloadedBytes"`
	TotalBytes        int64         `json:"totalBytes"`
	SpeedBytesPerSec  int64         `json:"speedBytesPerSec"`
	ETASeconds        int64         `json:"etaSeconds"`
	ActiveItemCount   int           `json:"activeItemCount"`
	DownloadLocation  string        `json:"-"`
	NamingPattern     string        `json:"-"`
	SubfolderSorting  string        `json:"-"`
	OutputFileMode    string        `json:"-"`
	OutputFolderMode  string        `json:"-"`
	Category          string        `json:"category,omitempty"`
	StorageMode       string        `json:"storageMode"`
	QueuePosition     int64         `json:"queuePosition,omitempty"`
}

type jobState struct {
	Job
	dir                 string
	fileItems           map[int]mediaFile
	fileGroups          map[int][]mediaFile
	librarySignatures   map[string][32]byte
	cancel              context.CancelFunc
	cancelRequested     bool
	pauseRequested      bool
	done                time.Time
	readers             int
	itemProgress        map[int]*itemProgress
	processingItems     int
	playlistItemCount   int
	persistenceFailed   bool
	persistenceRevision uint64
	persistedRevision   uint64
	persistencePending  int
	deleting            bool
	libraryOnly         bool
}

type ticket struct {
	jobID, fileID string
	fileIDs       []string
	expires       time.Time
	inline        bool
}

type server struct {
	cfg                 config
	settings            AppSettings
	settingsRevision    uint64
	queueOrderRevision  uint64
	queuePositionSerial int64
	mu                  sync.Mutex
	persistenceMu       sync.Mutex
	persistenceFailures uint64
	jobs                map[string]*jobState
	order               []string
	tickets             map[string]ticket
	slots               chan struct{}
	scheduleChanged     chan struct{}
	activeDownloads     int
	activeItems         int
	engineMu            sync.Mutex
	ctx                 context.Context
	stop                context.CancelFunc
	wg                  sync.WaitGroup
	engine              nativeClient
	browserFactory      browserProviderFactory
	filesystemOpener    filesystemOpener
	folderSelector      folderSelector
	thumbnailFetcher    func(context.Context, string, string) (string, error)
	captionFetcher      captionFetcher
	rangeHTTPClient     *http.Client
	store               *jobStore
	persistenceWriter   *persistenceWriter
	bandwidth           *bandwidthLimiter
	events              *eventBroker
}

func terminal(status string) bool {
	return status == "completed" || status == "partial" || status == "failed" || status == "cancelled"
}

// activeJobCountLocked counts jobs that still occupy the download backlog.
// Finished Library records do not consume MAX_JOBS capacity.
func (s *server) activeJobCountLocked() int {
	count := 0
	for _, job := range s.jobs {
		switch job.Status {
		case "queued", "downloading", "processing", "paused":
			count++
		}
	}
	return count
}

func (s *server) notifySchedulerLocked() {
	if s.scheduleChanged != nil {
		close(s.scheduleChanged)
	}
	s.scheduleChanged = make(chan struct{})
}

func snapshot(j *jobState) Job {
	copy := j.Job
	copy.Files = append([]mediaFile{}, j.Files...)
	copy.Failures = append([]itemFailure{}, j.Failures...)
	copy.Items = append([]queueItem{}, j.Items...)
	for i := range copy.Items {
		if copy.Items[i].Progress != nil {
			progress := *copy.Items[i].Progress
			copy.Items[i].Progress = &progress
		}
	}
	return copy
}

func (s *server) invalidateJobTicketsLocked(jobID string) {
	for id, ticket := range s.tickets {
		if ticket.jobID == jobID {
			delete(s.tickets, id)
		}
	}
}

func (s *server) forgetJobLocked(j *jobState) {
	delete(s.jobs, j.ID)
	for index, id := range s.order {
		if id == j.ID {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	s.invalidateJobTicketsLocked(j.ID)
	if s.store != nil {
		s.store.releaseJobCaches(j.ID)
	}
	s.publishDeletedEventLocked(j.ID)
}

func (s *server) forgetJobHistoryLocked(j *jobState) {
	if j == nil {
		return
	}
	delete(s.jobs, j.ID)
	for index, id := range s.order {
		if id == j.ID {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	if s.store != nil {
		s.store.releaseJobCaches(j.ID)
	}
	s.publishDeletedEventLocked(j.ID)
}

func (s *server) hydrateTerminalJobLocked(jobID string) (*jobState, bool, error) {
	if job := s.jobs[jobID]; job != nil {
		return job, false, nil
	}
	if s.store == nil {
		return nil, false, nil
	}
	loaded, err := s.store.loadJob(s.cfg.root, jobID)
	if err != nil || loaded == nil || !terminal(loaded.job.Status) {
		return nil, false, err
	}
	job := &jobState{Job: loaded.job, dir: loaded.dir, done: loaded.done, fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items), cancelRequested: loaded.cancelled}
	s.jobs[job.ID] = job
	s.order = append(s.order, job.ID)
	return job, true, nil
}

func (s *server) hydrateLibraryJobLocked(jobID string) (*jobState, bool, error) {
	if job := s.jobs[jobID]; job != nil {
		return job, false, nil
	}
	if s.store == nil {
		return nil, false, nil
	}
	loaded, err := s.store.loadLibraryJob(s.cfg.root, jobID)
	if err != nil || loaded == nil {
		return nil, false, err
	}
	job := &jobState{
		Job: loaded.job, dir: loaded.dir, done: loaded.done,
		fileItems: fileIndexes(loaded.items), fileGroups: fileGroupIndexes(loaded.items),
		libraryOnly: true,
	}
	s.jobs[job.ID] = job
	s.order = append(s.order, job.ID)
	return job, true, nil
}

// evictTerminalJobLocked drops a finished job's working state without sending
// job-deleted: the durable Library record and history still exist.
func (s *server) evictTerminalJobLocked(job *jobState) {
	if job == nil || !terminal(job.Status) || job.persistenceFailed || s.jobs[job.ID] != job {
		return
	}
	delete(s.jobs, job.ID)
	for index, id := range s.order {
		if id == job.ID {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	if s.store != nil {
		s.store.releaseJobCaches(job.ID)
	}
}

const persistenceDegradedThreshold uint64 = 3

func (s *server) recordPersistenceFailure(operation, jobID string, err error) {
	if err == nil {
		return
	}
	s.persistenceMu.Lock()
	s.persistenceFailures++
	consecutive := s.persistenceFailures
	s.persistenceMu.Unlock()
	log.Printf("persistence failure operation=%s job=%s consecutive=%d: %v", operation, jobID, consecutive, err)
}

func (s *server) recordPersistenceSuccess() {
	s.persistenceMu.Lock()
	s.persistenceFailures = 0
	s.persistenceMu.Unlock()
}

func (s *server) persistenceDegraded() (bool, uint64) {
	s.persistenceMu.Lock()
	defer s.persistenceMu.Unlock()
	return s.persistenceFailures >= persistenceDegradedThreshold, s.persistenceFailures
}

func (s *server) persistJobLocked(j *jobState) error {
	if j.libraryOnly {
		if s.store == nil {
			return nil
		}
		return s.persistJobSnapshotLocked(j, "save Library file state")
	}
	if s.store != nil {
		revision := j.persistenceRevision + 1
		if err := s.persistJobSnapshotLocked(j, "save job"); err != nil {
			if j.persistenceRevision == revision {
				j.persistenceFailed = true
				j.Status = "failed"
				if len(j.Files) > 0 {
					j.Status = "partial"
				}
				j.Error = "Could not save job state; download stopped"
				j.done = time.Now()
				if j.cancel != nil {
					j.cancel()
				}
				s.refreshAllQueueItemsLocked(j)
				s.publishJobEventLocked("job-status", j)
			}
			return err
		}
	}
	s.publishJobEventLocked("job-status", j)
	return nil
}

func (s *server) persistJobSnapshotLocked(j *jobState, operation string) error {
	j.persistenceRevision++
	j.persistencePending++
	revision := j.persistenceRevision
	snapshot := cloneJobStateForPersistence(j)
	err := s.persistOperationLocked(operation, j.ID, func(store *jobStore) error {
		if snapshot.libraryOnly {
			return store.saveLibraryJob(snapshot)
		}
		return store.saveJob(snapshot)
	})
	j.persistencePending--
	if err != nil {
		s.recordPersistenceFailure(operation, j.ID, err)
		return err
	}
	if revision > j.persistedRevision {
		j.persistedRevision = revision
		j.librarySignatures = snapshot.librarySignatures
	}
	s.recordPersistenceSuccess()
	return nil
}

// persistOperationLocked queues an immutable database operation in mutation
// order, releases the global job mutex while SQLite runs, then reacquires it
// before returning so callers retain their existing lock contract.
func (s *server) persistOperationLocked(operation, jobID string, save func(*jobStore) error) error {
	if s.persistenceWriter == nil {
		s.mu.Unlock()
		err := save(s.store)
		s.mu.Lock()
		return err
	}
	done := s.persistenceWriter.enqueue(save)
	s.mu.Unlock()
	err := <-done
	s.mu.Lock()
	return err
}

func reply(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, code int, message string) {
	reply(w, code, map[string]string{"error": message})
}

// decode accepts exactly one JSON object (maximum 4096 bytes), rejects unknown
// fields, and writes the appropriate JSON error before returning false.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeWithLimit(w, r, dst, 4096, "4096 bytes")
}

func decodeWithLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64, label string) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	body = bytes.TrimSpace(body)
	if err != nil || len(body) == 0 || body[0] != '{' {
		fail(w, 400, "A JSON object of at most "+label+" is required")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		fail(w, 400, "Invalid JSON body, unknown field, or body exceeds "+label)
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Exactly one JSON object is required")
		return false
	}
	return true
}

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
var playlistID = regexp.MustCompile(`^[A-Za-z0-9_-]{2,200}$`)

// canonicalURL accepts supported HTTPS YouTube video/playlist URL forms and
// returns a canonical watch or playlist URL plus its kind; other hosts/paths
// and malformed or invalid IDs are rejected.
func canonicalURL(raw string) (string, string, error) {
	bad := errors.New("use an HTTPS YouTube video, shorts, live, show, or playlist URL with valid IDs")
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.Fragment != "" {
		return "", "", bad
	}
	host := strings.ToLower(u.Host)
	if host != "youtube.com" && host != "www.youtube.com" && host != "m.youtube.com" && host != "music.youtube.com" && host != "youtu.be" {
		return "", "", bad
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["v"]) > 1 || len(q["list"]) > 1 {
		return "", "", bad
	}
	list, id := q.Get("list"), ""
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	show := host != "youtu.be" && len(parts) == 2 && parts[0] == "show"
	switch {
	case host == "youtu.be" && len(parts) == 1:
		id = parts[0]
	case host != "youtu.be" && u.Path == "/watch":
		id = q.Get("v")
	case host != "youtu.be" && len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "live"):
		id = parts[1]
	case show:
		if !strings.HasPrefix(parts[1], "VL") {
			return "", "", bad
		}
		if len(q["list"]) > 0 {
			return "", "", bad
		}
		list = strings.TrimPrefix(parts[1], "VL")
	case host != "youtu.be" && u.Path == "/playlist" && list != "":
	default:
		return "", "", bad
	}
	if (id == "" && u.Path != "/playlist" && !show) || (show && !playlistID.MatchString(list)) || (id != "" && !videoID.MatchString(id)) || (len(q["list"]) > 0 && !playlistID.MatchString(list)) {
		return "", "", bad
	}
	if list != "" {
		return "https://www.youtube.com/playlist?list=" + list, "playlist", nil
	}
	return "https://www.youtube.com/watch?v=" + id, "video", nil
}

// ServeHTTP enforces host/origin policy, serves health and static UI routes,
// applies bearer auth to protected API routes, and dispatches job/settings and
// ticket-download requests. GET health and ticket downloads are token-exempt;
// the random ticket itself authorizes a download.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/events" {
		writeTimeout := 30 * time.Second
		if r.URL.Path == "/api/library/export" {
			writeTimeout = 5 * time.Minute
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("Vary", "Origin")
	if !s.cfg.hosts[strings.ToLower(r.Host)] {
		fail(w, 403, "Host is not approved")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if !s.cfg.origins[origin] {
			fail(w, 403, "Origin is not approved")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.URL.Path == "/api/health" && r.Method == http.MethodGet {
		info := currentBuildInfo()
		degraded, consecutiveFailures := s.persistenceDegraded()
		extractorHealth := youtubeExtractorHealth()
		missing := make([]string, 0, 2)
		if s.engine == nil {
			missing = append(missing, "native-engine")
		}
		if !extractorHealth.Ready {
			missing = append(missing, "youtube-extractor-profiles")
		}
		reply(w, 200, map[string]any{
			"ready": len(missing) == 0, "missing": missing, "engine": "native-go",
			"extractor": extractorHealth,
			"version":   info.Version, "commit": info.Commit, "buildDate": info.Date,
			"degraded": degraded, "persistence": map[string]any{"degraded": degraded, "consecutiveFailures": consecutiveFailures},
			"capabilities": map[string]bool{"combinedStreamsOnly": false, "adaptiveStreamsSupported": true, "externalBinariesRequired": false,
				"mp3AudioSupported": true, "pureGoAudioConversion": true, "captionsSupported": true},
		})
		return
	}
	if r.URL.Path == "/api/inspect" && r.Method == http.MethodPost {
		if s.cfg.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.token)) != 1 {
			fail(w, 401, "Bearer authorization required")
			return
		}
		s.inspect(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/downloads/") {
		s.download(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") && s.serveStatic(w, r) {
		return
	}
	if s.cfg.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.token)) != 1 {
		fail(w, 401, "Bearer authorization required")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/jobs/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			s.mu.Lock()
			job, hydrated, err := s.hydrateTerminalJobLocked(parts[0])
			if err == nil && job == nil && libraryJobRoute(parts, r.Method) {
				job, hydrated, err = s.hydrateLibraryJobLocked(parts[0])
			}
			deleting := job != nil && job.deleting
			s.mu.Unlock()
			if err != nil {
				fail(w, http.StatusInternalServerError, "Could not load the saved job")
				return
			}
			if job == nil {
				fail(w, http.StatusNotFound, "Job not found")
				return
			}
			if deleting {
				fail(w, http.StatusConflict, "This job is being removed or updated")
				return
			}
			if hydrated {
				defer func() {
					s.mu.Lock()
					s.evictTerminalJobLocked(job)
					s.mu.Unlock()
				}()
			}
		}
	}
	if r.URL.Path == "/api/update" {
		if r.Method != http.MethodGet {
			fail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		status, err := checkLatestRelease(r.Context(), releaseHTTPClient(), latestReleaseAPI, currentBuildInfo())
		if err != nil {
			fail(w, http.StatusBadGateway, "Could not check the latest release")
			return
		}
		reply(w, http.StatusOK, status)
		return
	}
	if r.URL.Path == "/api/diagnostics" {
		if r.Method != http.MethodGet {
			fail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		diagnostics, err := s.store.loadCorruptionDiagnostics()
		if err != nil {
			fail(w, http.StatusInternalServerError, "Could not load diagnostics")
			return
		}
		reply(w, http.StatusOK, diagnostics)
		return
	}
	if r.URL.Path == "/api/library/export" {
		if r.Method != http.MethodGet {
			fail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleLibraryExport(w, r)
		return
	}
	if r.URL.Path == "/api/settings" {
		s.handleSettings(w, r)
		return
	}
	if r.URL.Path == "/api/events" {
		s.serveEvents(w, r)
		return
	}
	if r.URL.Path == "/api/library" {
		if r.Method != http.MethodGet {
			fail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		query := r.URL.Query()
		if query.Has("limit") || query.Has("cursor") || query.Has("q") || query.Has("type") || query.Has("category") || query.Has("channel") {
			s.handleLibraryPage(w, r)
			return
		}
		jobs, err := s.store.loadLibraryJobs()
		if err != nil {
			fail(w, http.StatusInternalServerError, "Could not load the Library")
			return
		}
		reply(w, http.StatusOK, map[string]any{"jobs": jobs})
		return
	}
	if r.URL.Path == "/api/folders/select" {
		if r.Method != http.MethodPost {
			fail(w, 405, "Method not allowed")
			return
		}
		s.handleFolderSelection(w, r)
		return
	}
	if r.URL.Path == "/api/queue/order" {
		if r.Method != http.MethodPut {
			fail(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		s.handleQueueOrder(w, r)
		return
	}
	if r.URL.Path == "/api/jobs" {
		switch r.Method {
		case http.MethodPost:
			s.create(w, r)
		case http.MethodGet:
			s.mu.Lock()
			jobs := []Job{}
			for i := len(s.order) - 1; i >= 0; i-- {
				jobs = append(jobs, snapshot(s.jobs[s.order[i]]))
			}
			s.mu.Unlock()
			reply(w, 200, map[string]any{"jobs": jobs})
		default:
			fail(w, 405, "Method not allowed")
		}
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	validLibraryItemRoute := len(parts) == 3 && parts[1] == "library-items" && r.Method == http.MethodDelete
	if !strings.HasPrefix(r.URL.Path, "/api/jobs/") || (len(parts) > 2 && !validLibraryItemRoute) || parts[0] == "" {
		fail(w, 404, "Endpoint not found")
		return
	}
	if len(parts) == 2 && parts[1] == "retry" && r.Method == http.MethodPost {
		s.retry(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "retry-item" && r.Method == http.MethodPost {
		s.handleRetryItem(w, r, parts[0])
		return
	}
	if len(parts) == 3 && parts[1] == "library-items" && r.Method == http.MethodDelete {
		s.handleLibraryItemDelete(w, parts[0], parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "history" && r.Method == http.MethodDelete {
		s.mu.Lock()
		defer s.mu.Unlock()
		j := s.jobs[parts[0]]
		if j == nil {
			fail(w, http.StatusNotFound, "Job not found")
			return
		}
		if !terminal(j.Status) {
			fail(w, http.StatusConflict, "Only stopped job history can be removed")
			return
		}
		if j.readers > 0 {
			fail(w, http.StatusConflict, "This job has an active file transfer")
			return
		}
		if j.deleting {
			fail(w, http.StatusConflict, "This job is being removed or updated")
			return
		}
		j.deleting = true
		if err := s.persistOperationLocked("delete job history", j.ID, func(store *jobStore) error {
			return store.deleteJobHistory(j.ID)
		}); err != nil {
			j.deleting = false
			s.recordPersistenceFailure("delete job history", j.ID, err)
			fail(w, http.StatusInternalServerError, "Could not remove job history")
			return
		}
		s.recordPersistenceSuccess()
		s.forgetJobHistoryLocked(j)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 2 && parts[1] == "next" && r.Method == http.MethodPost {
		s.handleDownloadNext(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "items" && r.Method == http.MethodPut {
		s.handleItemOrder(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "filesystem" && r.Method == http.MethodPost {
		s.handleFilesystem(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "thumbnail" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		s.serveThumbnail(w, r, parts[0])
		return
	}
	var requested struct {
		FileID  json.RawMessage `json:"fileId"`
		FileIDs []string        `json:"fileIds"`
		Inline  bool            `json:"inline"`
	}
	if len(parts) == 2 && parts[1] == "ticket" && r.Method == http.MethodPost && !decode(w, r, &requested) {
		return
	}
	fileID := ""
	if len(requested.FileID) > 0 && (json.Unmarshal(requested.FileID, &fileID) != nil || fileID == "") {
		fail(w, 400, "fileId must be a nonempty string when provided")
		return
	}
	if len(requested.FileIDs) > 10000 {
		fail(w, 400, "fileIds may contain at most 10000 entries")
		return
	}
	seenFileIDs := make(map[string]struct{}, len(requested.FileIDs))
	for _, requestedID := range requested.FileIDs {
		if requestedID == "" {
			fail(w, 400, "fileIds must contain nonempty strings")
			return
		}
		if _, exists := seenFileIDs[requestedID]; exists {
			fail(w, 400, "fileIds must be unique")
			return
		}
		seenFileIDs[requestedID] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[parts[0]]
	if j == nil {
		fail(w, 404, "Job not found")
		return
	}
	if j.deleting && (len(parts) != 1 || r.Method != http.MethodGet) {
		fail(w, http.StatusConflict, "This job is being removed")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		reply(w, 200, snapshot(j))
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if !terminal(j.Status) {
			fail(w, 409, "Only stopped jobs can be removed")
			return
		}
		if j.readers > 0 {
			fail(w, 409, "This job has an active file transfer")
			return
		}
		// Removing a job from the Library deletes only app-managed media and
		// history. Published files in the user's output directory are preserved.
		if err := removeManagedCopies(j); err != nil {
			fail(w, 500, "Could not remove the job's private managed files")
			return
		}
		j.deleting = true
		if err := s.persistOperationLocked("delete job", j.ID, func(store *jobStore) error {
			return store.deleteJob(j.ID)
		}); err != nil {
			j.deleting = false
			s.recordPersistenceFailure("delete job", j.ID, err)
			fail(w, 500, "Could not remove the job from history")
			return
		}
		s.recordPersistenceSuccess()
		s.forgetJobLocked(j)
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "managed" && r.Method == http.MethodDelete:
		if !terminal(j.Status) {
			fail(w, 409, "Only stopped jobs can have managed copies deleted")
			return
		}
		if j.readers > 0 {
			fail(w, 409, "This job has an active file transfer")
			return
		}
		if err := removeManagedCopies(j); err != nil {
			fail(w, 500, "Could not remove the app-managed media copies")
			return
		}
		s.invalidateJobTicketsLocked(j.ID)
		if err := s.persistJobLocked(j); err != nil {
			fail(w, 500, "Media copies were removed, but job state could not be saved")
			return
		}
		reply(w, 200, snapshot(j))
	case len(parts) == 2 && parts[1] == "published" && r.Method == http.MethodDelete:
		if !terminal(j.Status) {
			fail(w, 409, "Only stopped jobs can have published copies deleted")
			return
		}
		if j.readers > 0 {
			fail(w, 409, "This job has an active file or filesystem operation")
			return
		}
		if err := removeOutputCopies(j); err != nil {
			_ = s.persistJobLocked(j)
			fail(w, 500, "Could not remove the published media copies")
			return
		}
		if err := s.persistJobLocked(j); err != nil {
			fail(w, 500, "Published copies were removed, but job state could not be saved")
			return
		}
		reply(w, 200, snapshot(j))
	case len(parts) == 2 && parts[1] == "all" && r.Method == http.MethodDelete:
		if !terminal(j.Status) {
			fail(w, 409, "Only stopped jobs can be deleted")
			return
		}
		if j.readers > 0 {
			fail(w, 409, "This job has an active file transfer")
			return
		}
		if err := removeOutputCopies(j); err != nil {
			_ = s.persistJobLocked(j)
			fail(w, 500, "Could not remove the published media copies")
			return
		}
		if err := removeManagedCopies(j); err != nil {
			_ = s.persistJobLocked(j)
			fail(w, 500, "Published copies were removed, but private managed files could not be removed")
			return
		}
		j.deleting = true
		if err := s.persistOperationLocked("delete job", j.ID, func(store *jobStore) error {
			return store.deleteJob(j.ID)
		}); err != nil {
			j.deleting = false
			s.recordPersistenceFailure("delete job", j.ID, err)
			fail(w, 500, "Media copies were removed, but job history could not be deleted")
			return
		}
		s.recordPersistenceSuccess()
		s.forgetJobLocked(j)
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "pause" && r.Method == http.MethodPost:
		if j.Status == "queued" {
			j.Status, j.Error = "paused", ""
			s.notifySchedulerLocked()
		} else if j.Status == "downloading" || j.Status == "processing" {
			j.pauseRequested = true
			j.Error = "Pausing after the current download or conversion stops"
			if j.cancel != nil {
				j.cancel()
			}
		} else if j.Status != "paused" {
			fail(w, 409, "Only queued or downloading jobs can be paused")
			return
		}
		s.refreshAllQueueItemsLocked(j)
		if err := s.persistJobLocked(j); err != nil {
			fail(w, 500, "Could not save the pause state")
			return
		}
		reply(w, 200, snapshot(j))
	case len(parts) == 2 && parts[1] == "resume" && r.Method == http.MethodPost:
		if j.Status != "paused" {
			fail(w, 409, "Only paused jobs can be resumed")
			return
		}
		j.pauseRequested = false
		j.cancelRequested = false
		j.Status, j.Error, j.done = "queued", "", time.Time{}
		j.QueuePosition = s.nextQueuePositionLocked()
		s.refreshAllQueueItemsLocked(j)
		if err := s.persistJobLocked(j); err != nil {
			fail(w, 500, "Could not save the resume state")
			return
		}
		s.notifySchedulerLocked()
		reply(w, 200, snapshot(j))
	case len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost:
		if !terminal(j.Status) {
			if j.Status == "paused" {
				if err := discardPausedParts(j); err != nil {
					fail(w, 500, "Could not remove paused temporary output")
					return
				}
				if err := s.persistOperationLocked("delete resumable download state", j.ID, func(store *jobStore) error {
					return store.deletePartsForJob(j.ID)
				}); err != nil {
					s.recordPersistenceFailure("delete resumable download state", j.ID, err)
					fail(w, 500, "Could not remove resumable download state")
					return
				}
				s.recordPersistenceSuccess()
			}
			j.pauseRequested = false
			j.cancelRequested = true
			if j.cancel != nil {
				j.Error = "Cancellation requested; waiting for the downloader to stop"
				j.cancel()
			} else {
				j.Status, j.Error, j.done = "cancelled", "Cancelled before download started", time.Now()
			}
			s.refreshAllQueueItemsLocked(j)
			s.notifySchedulerLocked()
			if err := s.persistJobLocked(j); err != nil {
				fail(w, 500, "Could not save the cancellation state")
				return
			}
		}
		reply(w, 200, snapshot(j))
		s.evictTerminalJobLocked(j)
	case len(parts) == 2 && parts[1] == "ticket" && r.Method == http.MethodPost:
		s.issueTicket(w, j, fileID, requested.FileIDs, requested.Inline)
	default:
		fail(w, 405, "Method not allowed")
	}
}

func libraryJobRoute(parts []string, method string) bool {
	if len(parts) == 3 {
		return parts[1] == "library-items" && method == http.MethodDelete
	}
	if len(parts) == 1 {
		return method == http.MethodDelete
	}
	if len(parts) != 2 {
		return false
	}
	switch parts[1] {
	case "managed", "published", "all":
		return method == http.MethodDelete
	case "history":
		return method == http.MethodDelete
	case "filesystem":
		return method == http.MethodPost
	case "thumbnail":
		return method == http.MethodGet || method == http.MethodHead
	case "ticket":
		return method == http.MethodPost
	default:
		return false
	}
}

func (s *server) handleLibraryItemDelete(w http.ResponseWriter, jobID, fileID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[jobID]
	if j == nil {
		fail(w, http.StatusNotFound, "Library item not found")
		return
	}
	if !terminal(j.Status) {
		fail(w, http.StatusConflict, "Only finalized Library items can be removed")
		return
	}
	if j.deleting || j.readers > 0 {
		fail(w, http.StatusConflict, "This file has an active transfer or removal")
		return
	}
	fileIndex := -1
	for index := range j.Files {
		if j.Files[index].ID == fileID {
			fileIndex = index
			break
		}
	}
	if fileIndex < 0 {
		fail(w, http.StatusNotFound, "Library item not found")
		return
	}
	exists, err := s.store.hasLibraryItem(jobID, fileID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "Could not verify the Library item")
		return
	}
	if !exists {
		fail(w, http.StatusNotFound, "Library item not found")
		return
	}
	j.deleting = true
	file := &j.Files[fileIndex]
	if err := removeManagedCopyForFile(j, file); err != nil {
		syncStoredFile(j, *file)
		s.invalidateJobTicketsLocked(j.ID)
		if saveErr := s.persistJobLocked(j); saveErr != nil {
			log.Printf("Library item removal recovery failure job=%s: %v", j.ID, saveErr)
		}
		j.deleting = false
		fail(w, http.StatusConflict, "Could not safely remove the app-managed copy")
		return
	}
	syncStoredFile(j, *file)
	s.invalidateJobTicketsLocked(j.ID)
	snapshot := cloneJobStateForPersistence(j)
	if err := s.persistOperationLocked("remove Library item", j.ID, func(store *jobStore) error {
		return store.deleteLibraryItem(snapshot, fileID)
	}); err != nil {
		s.recordPersistenceFailure("remove Library item", j.ID, err)
		if saveErr := s.persistJobLocked(j); saveErr != nil {
			log.Printf("Library item removal recovery failure job=%s: %v", j.ID, saveErr)
		}
		j.deleting = false
		fail(w, http.StatusInternalServerError, "Could not remove the Library item")
		return
	}
	s.recordPersistenceSuccess()
	if j.libraryOnly {
		j.Files = append(j.Files[:fileIndex], j.Files[fileIndex+1:]...)
		s.evictTerminalJobLocked(j)
	} else {
		j.deleting = false
		s.publishJobEventLocked("job-status", j)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSettings reads preferences or validates and persists output settings.
func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		settings := mergeAppSettings(defaultAppSettings(), s.settings)
		s.mu.Unlock()
		reply(w, 200, map[string]AppSettings{"settings": settings})
	case http.MethodPut:
		var patch struct {
			DefaultQuality            *string   `json:"defaultQuality"`
			DefaultVideoStrategy      *string   `json:"defaultVideoStrategy"`
			Allow360pFallback         *bool     `json:"allow360pFallback"`
			MaxConcurrentDownloads    *int      `json:"maxConcurrentDownloads"`
			BandwidthLimitBytesPerSec *int64    `json:"bandwidthLimitBytesPerSec"`
			NotificationsEnabled      *bool     `json:"notificationsEnabled"`
			DownloadLocation          *string   `json:"downloadLocation"`
			NamingPattern             *string   `json:"namingPattern"`
			SubfolderSorting          *string   `json:"subfolderSorting"`
			OutputFileMode            *string   `json:"outputFileMode"`
			OutputFolderMode          *string   `json:"outputFolderMode"`
			DefaultCategory           *string   `json:"defaultCategory"`
			UserCategories            *[]string `json:"userCategories"`
			StorageMode               *string   `json:"storageMode"`
		}
		if !decode(w, r, &patch) {
			return
		}
		s.mu.Lock()
		settings := mergeAppSettings(defaultAppSettings(), s.settings)
		if patch.DefaultQuality != nil {
			settings.DefaultQuality = *patch.DefaultQuality
		}
		if patch.DefaultVideoStrategy != nil {
			settings.DefaultVideoStrategy = strings.ToLower(strings.TrimSpace(*patch.DefaultVideoStrategy))
		}
		if patch.Allow360pFallback != nil {
			settings.Allow360pFallback = *patch.Allow360pFallback
		}
		if patch.MaxConcurrentDownloads != nil {
			settings.MaxConcurrentDownloads = *patch.MaxConcurrentDownloads
		}
		if patch.BandwidthLimitBytesPerSec != nil {
			settings.BandwidthLimitBytesPerSec = *patch.BandwidthLimitBytesPerSec
		}
		if patch.NotificationsEnabled != nil {
			settings.NotificationsEnabled = *patch.NotificationsEnabled
		}
		if patch.DownloadLocation != nil {
			settings.DownloadLocation = *patch.DownloadLocation
		}
		if patch.NamingPattern != nil {
			settings.NamingPattern = *patch.NamingPattern
		}
		if patch.SubfolderSorting != nil {
			settings.SubfolderSorting = *patch.SubfolderSorting
		}
		if patch.OutputFileMode != nil {
			settings.OutputFileMode = strings.TrimSpace(*patch.OutputFileMode)
		}
		if patch.OutputFolderMode != nil {
			settings.OutputFolderMode = strings.TrimSpace(*patch.OutputFolderMode)
		}
		if patch.DefaultCategory != nil {
			settings.DefaultCategory = *patch.DefaultCategory
		}
		if patch.UserCategories != nil {
			settings.UserCategories = *patch.UserCategories
		}
		if patch.StorageMode != nil {
			settings.StorageMode = *patch.StorageMode
		}
		settings.DownloadLocation = filepath.Clean(strings.TrimSpace(settings.DownloadLocation))
		if settings.DefaultQuality != "best" && settings.DefaultQuality != "2160" && settings.DefaultQuality != "1440" && settings.DefaultQuality != "1080" && settings.DefaultQuality != "720" && settings.DefaultQuality != "480" {
			s.mu.Unlock()
			fail(w, 400, "defaultQuality must be best, 2160, 1440, 1080, 720, or 480")
			return
		}
		if settings.MaxConcurrentDownloads < 1 || settings.MaxConcurrentDownloads > 6 {
			s.mu.Unlock()
			fail(w, 400, "maxConcurrentDownloads must be between 1 and 6")
			return
		}
		if err := validateAppSettings(settings); err != nil {
			s.mu.Unlock()
			fail(w, 400, err.Error())
			return
		}
		previousSettings := s.settings
		s.settings = settings
		s.settingsRevision++
		revision := s.settingsRevision
		if s.bandwidth != nil {
			s.bandwidth.SetLimit(settings.BandwidthLimitBytesPerSec)
		}
		if err := s.persistOperationLocked("save application settings", "", func(store *jobStore) error {
			return store.saveAppSettings(settings)
		}); err != nil {
			s.recordPersistenceFailure("save application settings", "", err)
			if s.settingsRevision == revision {
				s.settings = previousSettings
				if s.bandwidth != nil {
					s.bandwidth.SetLimit(previousSettings.BandwidthLimitBytesPerSec)
				}
			}
			s.mu.Unlock()
			fail(w, 500, "Could not save preferences")
			return
		}
		s.recordPersistenceSuccess()
		s.publishSettingsEventLocked(settings)
		s.notifySchedulerLocked()
		s.mu.Unlock()
		reply(w, 200, map[string]AppSettings{"settings": settings})
	default:
		fail(w, 405, "Method not allowed")
	}
}

func discardPausedParts(j *jobState) error {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			if err := os.Remove(filepath.Join(j.dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL              string          `json:"url"`
		Quality          string          `json:"quality"`
		VideoStrategy    string          `json:"videoStrategy"`
		MediaType        string          `json:"mediaType"`
		AudioBitrate     string          `json:"audioBitrate"`
		AudioFormat      string          `json:"audioFormat"`
		SplitByChapter   bool            `json:"splitByChapter"`
		SubtitleLanguage string          `json:"subtitleLanguage"`
		SubtitleFormat   string          `json:"subtitleFormat"`
		Category         string          `json:"category"`
		RightsConfirmed  bool            `json:"rightsConfirmed"`
		Items            []inspectedItem `json:"items"`
	}
	if !decodeWithLimit(w, r, &request, 8<<20, "8 MiB") {
		return
	}
	u, kind, err := canonicalURL(request.URL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !request.RightsConfirmed {
		fail(w, 400, "Confirm that you own the content or have permission to download it")
		return
	}
	if len(request.Items) > 10000 {
		fail(w, 400, "A playlist may contain at most 10000 selected entries")
		return
	}
	if kind != "playlist" && len(request.Items) > 0 {
		fail(w, 400, "items may be supplied only for playlist jobs")
		return
	}
	if kind == "playlist" && len(request.Items) > 0 {
		seenIndexes := make(map[int]struct{}, len(request.Items))
		for _, item := range request.Items {
			if item.Index < 1 || item.Index > 10000 || !videoID.MatchString(item.ID) {
				fail(w, 400, "playlist items must include a valid original index and video ID")
				return
			}
			if _, exists := seenIndexes[item.Index]; exists {
				fail(w, 400, "playlist item indexes must be unique")
				return
			}
			seenIndexes[item.Index] = struct{}{}
		}
	}
	if request.Quality != "best" && request.Quality != "2160" && request.Quality != "1440" && request.Quality != "1080" && request.Quality != "720" && request.Quality != "480" {
		fail(w, 400, "Quality must be best, 2160, 1440, 1080, 720, or 480")
		return
	}
	if request.MediaType == "" {
		request.MediaType = "video"
	}
	if request.MediaType != "video" && request.MediaType != "audio" {
		fail(w, 400, "mediaType must be video or audio")
		return
	}
	request.VideoStrategy = strings.ToLower(strings.TrimSpace(request.VideoStrategy))
	if request.MediaType == "video" {
		if request.VideoStrategy != "" && !validVideoStrategy(request.VideoStrategy) {
			fail(w, 400, "videoStrategy must be best, compatibility, vp9, or av1")
			return
		}
	} else if request.VideoStrategy != "" {
		fail(w, 400, "videoStrategy is valid only when mediaType is video")
		return
	}
	if request.MediaType == "audio" {
		if request.AudioFormat == "" {
			request.AudioFormat = "mp3"
		}
		if request.AudioFormat != "mp3" && request.AudioFormat != "m4a" {
			fail(w, 400, "audioFormat must be mp3 or m4a")
			return
		}
		if request.AudioFormat == "mp3" {
			if request.AudioBitrate == "" {
				request.AudioBitrate = "192k"
			}
			if request.AudioBitrate != "128k" && request.AudioBitrate != "192k" && request.AudioBitrate != "256k" && request.AudioBitrate != "320k" {
				fail(w, 400, "audioBitrate must be 128k, 192k, 256k, or 320k")
				return
			}
		} else {
			request.AudioBitrate = ""
		}
	} else {
		if request.AudioFormat != "" {
			fail(w, 400, "audioFormat is valid only when mediaType is audio")
			return
		}
		request.AudioBitrate = ""
	}
	request.SubtitleLanguage = strings.TrimSpace(request.SubtitleLanguage)
	request.SubtitleFormat = strings.ToLower(strings.TrimSpace(request.SubtitleFormat))
	if request.SubtitleLanguage != "" && request.SubtitleFormat == "" {
		request.SubtitleFormat = "vtt"
	}
	if err := validateSubtitleRequest(request.SubtitleLanguage, request.SubtitleFormat); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if request.SplitByChapter && (request.MediaType != "audio" || request.AudioFormat != "mp3") {
		fail(w, 400, "splitByChapter currently requires MP3 audio output")
		return
	}
	s.enqueueJob(w, u, kind, request.Quality, request.VideoStrategy, request.MediaType, request.AudioFormat, request.AudioBitrate, request.SubtitleLanguage, request.SubtitleFormat, request.Category, "", request.SplitByChapter, request.Items)
}

// enqueueJob allocates private per-job storage, persists a queued job, and
// returns 202 only after the job has entered the bounded worker backlog.
func (s *server) enqueueJob(w http.ResponseWriter, u, kind, quality, requestedVideoStrategy, mediaType, audioFormat, audioBitrate, subtitleLanguage, subtitleFormat, requestedCategory, requestedStorageMode string, splitByChapter bool, inspectedItems ...[]inspectedItem) {
	s.enqueueJobWithFallback(w, u, kind, quality, requestedVideoStrategy, mediaType, audioFormat, audioBitrate, subtitleLanguage, subtitleFormat, requestedCategory, requestedStorageMode, splitByChapter, nil, inspectedItems...)
}

func (s *server) enqueueJobWithFallback(w http.ResponseWriter, u, kind, quality, requestedVideoStrategy, mediaType, audioFormat, audioBitrate, subtitleLanguage, subtitleFormat, requestedCategory, requestedStorageMode string, splitByChapter bool, requestedAllow360pFallback *bool, inspectedItems ...[]inspectedItem) {
	if s.engine == nil {
		fail(w, 503, "Native download engine is not initialized")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		fail(w, 503, "Downloader service is stopping")
		return
	}
	if s.activeJobCountLocked() >= s.cfg.maxJobs {
		fail(w, 429, "Active job capacity reached; wait for a job to finish or cancel one")
		return
	}
	items := []queueItem{}
	if kind == "video" {
		items = append(items, queueItem{Index: 1, VideoID: strings.TrimPrefix(u, "https://www.youtube.com/watch?v="), Title: "YouTube video", Status: "queued"})
	} else if kind == "playlist" && len(inspectedItems) > 0 {
		for index, inspected := range inspectedItems[0] {
			title := inspected.Title
			if title == "" {
				title = "Playlist item " + strconv.Itoa(inspected.Index)
			}
			items = append(items, queueItem{
				Index: index + 1, PlaylistIndex: inspected.Index, VideoID: inspected.ID,
				Title: title, Author: inspected.Author,
				DurationSeconds: max(int64(0), inspected.DurationSeconds),
				ThumbnailURL:    safeInspectedThumbnailURL(inspected.ThumbnailURL), Status: "queued",
			})
		}
	}
	outputSettings := mergeAppSettings(defaultAppSettings(), s.settings)
	videoStrategy := ""
	if mediaType == "video" {
		videoStrategy = strings.ToLower(strings.TrimSpace(requestedVideoStrategy))
		if videoStrategy == "" {
			videoStrategy = outputSettings.DefaultVideoStrategy
		}
		if !validVideoStrategy(videoStrategy) {
			fail(w, 400, "videoStrategy must be best, compatibility, vp9, or av1")
			return
		}
	}
	category, err := resolveJobCategory(outputSettings, requestedCategory)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	storageMode := requestedStorageMode
	if storageMode == "" {
		storageMode = outputSettings.StorageMode
	}
	if storageMode != "managed-published" && storageMode != "published-only" && storageMode != "managed-only" {
		fail(w, 400, "storageMode must be managed-published, published-only, or managed-only")
		return
	}
	allow360pFallback := outputSettings.Allow360pFallback
	if requestedAllow360pFallback != nil {
		allow360pFallback = *requestedAllow360pFallback
	}
	j := &jobState{Job: Job{
		ID: randomID(16), URL: u, Kind: kind, Quality: quality, VideoStrategy: videoStrategy, Allow360pFallback: mediaType == "video" && allow360pFallback, MediaType: mediaType, AudioFormat: audioFormat, AudioBitrate: audioBitrate,
		SubtitleLanguage: subtitleLanguage, SubtitleFormat: subtitleFormat,
		Status: "queued", Title: "YouTube " + kind, Files: []mediaFile{}, Items: items, Failures: []itemFailure{},
		Note: formatNote, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), DownloadLocation: outputSettings.DownloadLocation,
		NamingPattern: outputSettings.NamingPattern, SubfolderSorting: outputSettings.SubfolderSorting,
		OutputFileMode: outputSettings.OutputFileMode, OutputFolderMode: outputSettings.OutputFolderMode,
		Category: category, StorageMode: storageMode,
		QueuePosition:  s.nextQueuePositionLocked(),
		SplitByChapter: splitByChapter,
	}, fileItems: map[int]mediaFile{}, fileGroups: map[int][]mediaFile{}}
	if kind == "playlist" {
		j.Note += " " + playlistNote
		if len(j.Items) > 0 {
			total := len(j.Items)
			j.TotalCount = &total
		}
	}
	j.dir = filepath.Join(s.cfg.root, j.ID)
	if err := os.Mkdir(j.dir, 0700); err != nil {
		fail(w, 500, "Cannot allocate private job storage")
		return
	}
	if kind == "video" {
		total := 1
		j.TotalCount = &total
	}
	if err := s.persistJobSnapshotLocked(j, "create job"); err != nil {
		_ = os.RemoveAll(j.dir)
		fail(w, 500, "Cannot persist download job")
		return
	}
	s.jobs[j.ID], s.order = j, append(s.order, j.ID)
	s.publishJobEventLocked("job-created", j)
	s.notifySchedulerLocked()
	reply(w, 202, snapshot(j))
}

// issueTicket creates a short-lived random capability for a stopped job's ZIP
// or one finalized file. The returned ticket URL, not a bearer token, grants
// access to the corresponding download route.
func (s *server) issueTicket(w http.ResponseWriter, j *jobState, fileID string, fileIDs []string, inline bool) {
	if !terminal(j.Status) || len(j.Files) == 0 {
		fail(w, 409, "Files are available only after the job has stopped and finalized output exists")
		return
	}
	if inline && fileID == "" {
		fail(w, 400, "inline preview tickets require one fileId")
		return
	}
	if fileID != "" && len(fileIDs) > 0 {
		fail(w, 400, "provide fileId or fileIds, not both")
		return
	}
	if fileID != "" {
		found, available := false, false
		for _, f := range j.Files {
			if f.ID == fileID {
				found = true
				available = f.ManagedAvailable
				break
			}
		}
		if !found {
			fail(w, 404, "File not found")
			return
		}
		if !available {
			fail(w, 409, "The app-managed copy for this file is no longer available")
			return
		}
	} else if len(fileIDs) > 0 {
		for _, requestedID := range fileIDs {
			found := false
			for _, file := range j.Files {
				if file.ID == requestedID {
					if !file.ManagedAvailable {
						fail(w, 409, "The app-managed media copy is no longer available")
						return
					}
					found = true
					break
				}
			}
			if !found {
				fail(w, 404, "Finalized output not found")
				return
			}
		}
	} else {
		for _, f := range j.Files {
			if !f.ManagedAvailable {
				fail(w, 409, "One or more app-managed copies are no longer available for archive download")
				return
			}
		}
	}
	for id, t := range s.tickets {
		if !time.Now().Before(t.expires) {
			delete(s.tickets, id)
		}
	}
	if len(s.tickets) >= 1024 {
		fail(w, 429, "Too many active download tickets")
		return
	}
	id := randomID(32)
	expiresIn := 5 * time.Minute
	if inline {
		expiresIn = 30 * time.Minute
	}
	s.tickets[id] = ticket{jobID: j.ID, fileID: fileID, fileIDs: append([]string(nil), fileIDs...), expires: time.Now().Add(expiresIn), inline: inline}
	reply(w, 200, map[string]string{"path": "/api/downloads/" + id})
}
