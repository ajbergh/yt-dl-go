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

type mediaFile struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Height             int    `json:"height"`
	MimeType           string `json:"mimeType"`
	Title              string `json:"title,omitempty"`
	Author             string `json:"author,omitempty"`
	DurationSeconds    int64  `json:"durationSeconds,omitempty"`
	ThumbnailURL            string `json:"thumbnailUrl,omitempty"`
	ThumbnailLocalAvailable bool   `json:"thumbnailLocalAvailable,omitempty"`
	ThumbnailMimeType       string `json:"thumbnailMimeType,omitempty"`
	PublishDate             string `json:"publishDate,omitempty"`
	Category           string `json:"category,omitempty"`
	MediaType          string `json:"mediaType,omitempty"`
	OutputName         string `json:"outputName,omitempty"`
	OutputPath         string `json:"-"`
	OutputRelativePath string `json:"outputRelativePath,omitempty"`
	ManagedAvailable   bool   `json:"managedAvailable"`
	PublishedAvailable bool   `json:"publishedAvailable"`
	Subtitle           *subtitleFile `json:"subtitle,omitempty"`
	SubtitleError      string        `json:"subtitleError,omitempty"`
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
	RetryRequested   bool     `json:"retryRequested,omitempty"`
}

type Job struct {
	ID               string        `json:"id"`
	URL              string        `json:"url"`
	Kind             string        `json:"kind"`
	Quality          string        `json:"quality"`
	MediaType        string        `json:"mediaType"`
	AudioBitrate     string        `json:"audioBitrate,omitempty"`
	AudioFormat      string        `json:"audioFormat,omitempty"`
	SubtitleLanguage string        `json:"subtitleLanguage,omitempty"`
	SubtitleFormat   string        `json:"subtitleFormat,omitempty"`
	Status           string        `json:"status"`
	Title            string        `json:"title"`
	Progress         *float64      `json:"progress"`
	CurrentItem      string        `json:"currentItem"`
	CompletedCount   int           `json:"completedCount"`
	TotalCount       *int          `json:"totalCount"`
	Files            []mediaFile   `json:"files"`
	Items            []queueItem   `json:"items"`
	Error            string        `json:"error"`
	CreatedAt        string        `json:"createdAt"`
	Note             string        `json:"note"`
	Failures         []itemFailure `json:"failures"`
	DownloadedBytes  int64         `json:"downloadedBytes"`
	TotalBytes       int64         `json:"totalBytes"`
	SpeedBytesPerSec int64         `json:"speedBytesPerSec"`
	ETASeconds       int64         `json:"etaSeconds"`
	ActiveItemCount  int           `json:"activeItemCount"`
	DownloadLocation string        `json:"-"`
	NamingPattern    string        `json:"-"`
	SubfolderSorting string        `json:"-"`
	Category         string        `json:"category,omitempty"`
	StorageMode      string        `json:"storageMode"`
	QueuePosition    int64         `json:"queuePosition,omitempty"`
}

type jobState struct {
	Job
	dir             string
	fileItems       map[int]mediaFile
	cancel          context.CancelFunc
	cancelRequested bool
	pauseRequested  bool
	done            time.Time
	readers         int
	itemProgress    map[int]*itemProgress
	processingItems int
	playlistItemCount int
}

type ticket struct {
	jobID, fileID string
	expires       time.Time
	inline        bool
}

type server struct {
	cfg             config
	settings        AppSettings
	mu              sync.Mutex
	jobs            map[string]*jobState
	order           []string
	tickets         map[string]ticket
	queue           chan string
	slots           chan struct{}
	scheduleChanged chan struct{}
	activeDownloads int
	activeItems     int
	engineMu        sync.Mutex
	ctx             context.Context
	stop            context.CancelFunc
	wg              sync.WaitGroup
	engine          nativeClient
	browserFactory  browserProviderFactory
	filesystemOpener filesystemOpener
	folderSelector   folderSelector
	thumbnailFetcher func(context.Context, string, string) (string, error)
	captionFetcher   captionFetcher
	store            *jobStore
	bandwidth        *bandwidthLimiter
	events           *eventBroker
}

func terminal(status string) bool {
	return status == "completed" || status == "partial" || status == "failed" || status == "cancelled"
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
	s.publishDeletedEventLocked(j.ID)
}

func (s *server) persistJobLocked(j *jobState) {
	if s.store != nil {
		_ = s.store.saveJob(j)
	}
	s.publishJobEventLocked("job-status", j)
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
	bad := errors.New("Use an HTTPS YouTube video, shorts, live, show, or playlist URL with valid IDs")
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
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
		reply(w, 200, map[string]any{
			"ready": s.engine != nil, "missing": []string{}, "engine": "native-go",
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
	if r.URL.Path == "/api/settings" {
		s.handleSettings(w, r)
		return
	}
	if r.URL.Path == "/api/events" {
		s.serveEvents(w, r)
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
	if !strings.HasPrefix(r.URL.Path, "/api/jobs/") || len(parts) > 2 || parts[0] == "" {
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
		FileID json.RawMessage `json:"fileId"`
		Inline bool            `json:"inline"`
	}
	if len(parts) == 2 && parts[1] == "ticket" && r.Method == http.MethodPost && !decode(w, r, &requested) {
		return
	}
	fileID := ""
	if len(requested.FileID) > 0 && (json.Unmarshal(requested.FileID, &fileID) != nil || fileID == "") {
		fail(w, 400, "fileId must be a nonempty string when provided")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[parts[0]]
	if j == nil {
		fail(w, 404, "Job not found")
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
		if err := s.store.deleteJob(j.ID); err != nil {
			fail(w, 500, "Could not remove the job from history")
			return
		}
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
		s.persistJobLocked(j)
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
			s.persistJobLocked(j)
			fail(w, 500, "Could not remove the published media copies")
			return
		}
		s.persistJobLocked(j)
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
			s.persistJobLocked(j)
			fail(w, 500, "Could not remove the published media copies")
			return
		}
		if err := removeManagedCopies(j); err != nil {
			s.persistJobLocked(j)
			fail(w, 500, "Published copies were removed, but private managed files could not be removed")
			return
		}
		if err := s.store.deleteJob(j.ID); err != nil {
			fail(w, 500, "Media copies were removed, but job history could not be deleted")
			return
		}
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
		s.persistJobLocked(j)
		reply(w, 200, snapshot(j))
	case len(parts) == 2 && parts[1] == "resume" && r.Method == http.MethodPost:
		if j.Status != "paused" {
			fail(w, 409, "Only paused jobs can be resumed")
			return
		}
		select {
		case s.queue <- j.ID:
			j.pauseRequested = false
			j.cancelRequested = false
			j.Status, j.Error, j.done = "queued", "", time.Time{}
			j.QueuePosition = s.nextQueuePositionLocked()
			s.refreshAllQueueItemsLocked(j)
			s.persistJobLocked(j)
			s.notifySchedulerLocked()
			reply(w, 200, snapshot(j))
		default:
			fail(w, 429, "Download queue is full; try resuming again shortly")
		}
	case len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost:
		if !terminal(j.Status) {
			if j.Status == "paused" {
				if err := discardPausedParts(j); err != nil {
					fail(w, 500, "Could not remove paused temporary output")
					return
				}
				if err := s.store.deletePartsForJob(j.ID); err != nil {
					fail(w, 500, "Could not remove resumable download state")
					return
				}
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
			s.persistJobLocked(j)
		}
		reply(w, 200, snapshot(j))
	case len(parts) == 2 && parts[1] == "ticket" && r.Method == http.MethodPost:
		s.issueTicket(w, j, fileID, requested.Inline)
	default:
		fail(w, 405, "Method not allowed")
	}
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
			DefaultQuality         *string   `json:"defaultQuality"`
			MaxConcurrentDownloads   *int      `json:"maxConcurrentDownloads"`
			BandwidthLimitBytesPerSec *int64    `json:"bandwidthLimitBytesPerSec"`
			NotificationsEnabled     *bool     `json:"notificationsEnabled"`
			DownloadLocation         *string   `json:"downloadLocation"`
			NamingPattern          *string   `json:"namingPattern"`
			SubfolderSorting       *string   `json:"subfolderSorting"`
			DefaultCategory        *string   `json:"defaultCategory"`
			UserCategories         *[]string `json:"userCategories"`
			StorageMode            *string   `json:"storageMode"`
		}
		if !decode(w, r, &patch) {
			return
		}
		s.mu.Lock()
		settings := mergeAppSettings(defaultAppSettings(), s.settings)
		if patch.DefaultQuality != nil {
			settings.DefaultQuality = *patch.DefaultQuality
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
		if err := s.store.saveAppSettings(settings); err != nil {
			s.mu.Unlock()
			fail(w, 500, "Could not save preferences")
			return
		}
		s.settings = settings
		if s.bandwidth != nil {
			s.bandwidth.SetLimit(settings.BandwidthLimitBytesPerSec)
		}
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
		URL             string          `json:"url"`
		Quality         string          `json:"quality"`
		MediaType       string          `json:"mediaType"`
		AudioBitrate    string          `json:"audioBitrate"`
		AudioFormat     string          `json:"audioFormat"`
		SubtitleLanguage string         `json:"subtitleLanguage"`
		SubtitleFormat   string         `json:"subtitleFormat"`
		Category        string          `json:"category"`
		RightsConfirmed bool            `json:"rightsConfirmed"`
		Items           []inspectedItem `json:"items"`
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
	s.enqueueJob(w, u, kind, request.Quality, request.MediaType, request.AudioFormat, request.AudioBitrate, request.SubtitleLanguage, request.SubtitleFormat, request.Category, "", request.Items)
}

// enqueueJob allocates private per-job storage, persists a queued job, and
// returns 202 only after the job has entered the bounded worker queue.
func (s *server) enqueueJob(w http.ResponseWriter, u, kind, quality, mediaType, audioFormat, audioBitrate, subtitleLanguage, subtitleFormat, requestedCategory, requestedStorageMode string, inspectedItems ...[]inspectedItem) {
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
	if len(s.jobs) >= s.cfg.maxJobs {
		fail(w, 429, "Job capacity reached; wait for retained jobs to expire")
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
				ThumbnailURL: safeInspectedThumbnailURL(inspected.ThumbnailURL), Status: "queued",
			})
		}
	}
	outputSettings := mergeAppSettings(defaultAppSettings(), s.settings)
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
	j := &jobState{Job: Job{
		ID: randomID(16), URL: u, Kind: kind, Quality: quality, MediaType: mediaType, AudioFormat: audioFormat, AudioBitrate: audioBitrate,
		SubtitleLanguage: subtitleLanguage, SubtitleFormat: subtitleFormat,
		Status: "queued", Title: "YouTube " + kind, Files: []mediaFile{}, Items: items, Failures: []itemFailure{},
		Note: formatNote, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), DownloadLocation: outputSettings.DownloadLocation,
		NamingPattern: outputSettings.NamingPattern, SubfolderSorting: outputSettings.SubfolderSorting, Category: category, StorageMode: storageMode,
		QueuePosition: s.nextQueuePositionLocked(),
	}, fileItems: map[int]mediaFile{}}
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
	// Cancelled queue entries can still occupy slots until the worker reaches them.
	select {
	case s.queue <- j.ID:
		s.jobs[j.ID], s.order = j, append(s.order, j.ID)
		s.notifySchedulerLocked()
		if err := s.store.saveJob(j); err != nil {
			delete(s.jobs, j.ID)
			s.order = s.order[:len(s.order)-1]
			_ = os.RemoveAll(j.dir)
			fail(w, 500, "Cannot persist download job")
			return
		}
		s.publishJobEventLocked("job-created", j)
		reply(w, 202, snapshot(j))
	default:
		_ = os.RemoveAll(j.dir)
		fail(w, 429, "Download queue is full")
	}
}

// issueTicket creates a short-lived random capability for a stopped job's ZIP
// or one finalized file. The returned ticket URL, not a bearer token, grants
// access to the corresponding download route.
func (s *server) issueTicket(w http.ResponseWriter, j *jobState, fileID string, inline bool) {
	if !terminal(j.Status) || len(j.Files) == 0 {
		fail(w, 409, "Files are available only after the job has stopped and finalized output exists")
		return
	}
	if inline && fileID == "" {
		fail(w, 400, "inline preview tickets require one fileId")
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
	s.tickets[id] = ticket{jobID: j.ID, fileID: fileID, expires: time.Now().Add(expiresIn), inline: inline}
	reply(w, 200, map[string]string{"path": "/api/downloads/" + id})
}
