package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

const testVideo = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
const testPlaylist = "https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PL0123456789ABC"
const fixtureData = "fixture-media"

type fakeClient struct {
	playlist *youtube.Playlist
	listErr  error
	listFn   func(context.Context) (*youtube.Playlist, error)
	videoFn  func(context.Context, string) (*youtube.Video, error)
	streamFn func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error)
}

func fixtureClient(count int) *fakeClient {
	entries := []*youtube.PlaylistEntry{}
	for i := 1; i <= count; i++ {
		entries = append(entries, &youtube.PlaylistEntry{ID: fmt.Sprintf("%011d", i), Title: fmt.Sprintf("Item %d", i)})
	}
	return &fakeClient{playlist: &youtube.Playlist{Title: "Fixture playlist", Videos: entries}}
}

func fixtureVideo(id string) *youtube.Video {
	return &youtube.Video{
		ID: id, Title: "Fixture video", Author: "Fixture channel", Duration: 5 * time.Minute,
		Thumbnails: youtube.Thumbnails{{URL: "https://i.ytimg.com/vi/fixture/hqdefault.jpg"}},
		CaptionTracks: []youtube.CaptionTrack{
			captionTrack("en", "English", "", "https://www.youtube.com/api/timedtext?v=fixture&lang=en"),
			captionTrack("es", "Spanish (auto-generated)", "asr", "https://www.youtube.com/api/timedtext?v=fixture&lang=es"),
		},
		Formats: youtube.FormatList{{ItagNo: 18, MimeType: `video/mp4; codecs="avc1.42001E, mp4a.40.2"`, Height: 360, Width: 640, FPS: 30, AudioChannels: 2, ContentLength: int64(len(fixtureData))}},
	}
}

func (f *fakeClient) GetPlaylistContext(ctx context.Context, _ string) (*youtube.Playlist, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return f.playlist, f.listErr
}

func (f *fakeClient) GetVideoContext(ctx context.Context, raw string) (*youtube.Video, error) {
	u, _ := url.Parse(raw)
	return f.VideoFromPlaylistEntryContext(ctx, &youtube.PlaylistEntry{ID: u.Query().Get("v")})
}

func (f *fakeClient) VideoFromPlaylistEntryContext(ctx context.Context, entry *youtube.PlaylistEntry) (*youtube.Video, error) {
	if f.videoFn != nil {
		return f.videoFn(ctx, entry.ID)
	}
	return fixtureVideo(entry.ID), nil
}

func (f *fakeClient) GetStreamContext(ctx context.Context, video *youtube.Video, format *youtube.Format) (io.ReadCloser, int64, error) {
	if f.streamFn != nil {
		return f.streamFn(ctx, video, format)
	}
	return io.NopCloser(strings.NewReader(fixtureData)), int64(len(fixtureData)), nil
}

func testServer(t *testing.T, engine nativeClient, change func(*config)) *server {
	t.Helper()
	c := config{
		addr: "127.0.0.1:8080", root: filepath.Join(t.TempDir(), "private"), token: strings.Repeat("a", 32),
		origins: map[string]bool{"http://localhost:5173": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 8, maxBytes: 1024 * 1024, timeout: 10 * time.Second, retain: 10 * time.Minute,
	}
	if change != nil {
		change(&c)
	}
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.stop()
		s.wg.Wait()
		if err := os.RemoveAll(s.cfg.root); err != nil {
			t.Error(err)
		}
	})
	s.settings.DownloadLocation = filepath.Join(s.cfg.root, "published")
	if err := s.store.saveAppSettings(s.settings); err != nil {
		t.Fatal(err)
	}
	s.engine = engine
	s.thumbnailFetcher = func(context.Context, string, string) (string, error) {
		return "", errors.New("thumbnail capture disabled in generic fixture")
	}
	s.start()
	return s
}

func request(s *server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+s.cfg.token)
	for k, value := range headers {
		if k == "Host" {
			r.Host = value
		} else {
			r.Header.Set(k, value)
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func trackedOutputPath(t *testing.T, s *server, jobID, fileID string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[jobID]
	if j == nil {
		t.Fatalf("tracked job %s not found", jobID)
	}
	for _, file := range j.Files {
		if file.ID == fileID {
			return file.OutputPath
		}
	}
	t.Fatalf("tracked file %s not found for job %s", fileID, jobID)
	return ""
}

func createJob(t *testing.T, s *server, raw string) Job {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"url": raw, "quality": "best", "rightsConfirmed": true})
	w := request(s, "POST", "/api/jobs", string(body), nil)
	if w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var j Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func waitJob(t *testing.T, s *server, id string, accept func(Job) bool) Job {
	t.Helper()
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		w := request(s, "GET", "/api/jobs/"+id, "", nil)
		var j Job
		if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
			t.Fatal(err)
		}
		if accept(j) {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not reach the expected state")
	return Job{}
}

func waitTerminal(t *testing.T, s *server, id string) Job {
	return waitJob(t, s, id, func(j Job) bool { return terminal(j.Status) })
}

func assertFinalFiles(t *testing.T, s *server, j Job) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.cfg.root, j.ID))
	if err != nil || len(entries) != len(j.Files) {
		t.Fatalf("unfinished or missing files: %d entries, %d final files, %v", len(entries), len(j.Files), err)
	}
	var size int64
	for _, file := range j.Files {
		f, err := openFinal(filepath.Join(s.cfg.root, j.ID), file.Name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := f.Stat()
		_ = f.Close()
		if err != nil || info.Size() != file.Size || file.Height <= 0 || (file.MimeType != "video/mp4" && file.MimeType != "video/webm") {
			t.Fatalf("invalid finalized media: %+v", file)
		}
		size += file.Size
	}
	if size > s.cfg.maxBytes {
		t.Fatal("finalized bytes exceed the hard job limit")
	}
}

func TestLoadConfigAllowsCurrentLoopbackOrigin(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:49152")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:5173")
	t.Setenv("ALLOWED_HOSTS", "")
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{
		"http://127.0.0.1:49152",
		"http://localhost:49152",
		"http://[::1]:49152",
	} {
		if !c.origins[origin] {
			t.Fatalf("current loopback origin %q was not automatically allowed", origin)
		}
	}
	if !c.hosts["127.0.0.1:49152"] {
		t.Fatal("current loopback host was not automatically allowed")
	}
}

func TestCanonicalURL(t *testing.T) {
	for _, raw := range []string{testVideo, "https://youtu.be/dQw4w9WgXcQ?si=ignored", "https://m.youtube.com/shorts/dQw4w9WgXcQ", "https://youtube.com/live/dQw4w9WgXcQ"} {
		got, kind, err := canonicalURL(raw)
		if err != nil || got != testVideo || kind != "video" {
			t.Errorf("video canonicalization: %q %q %v", got, kind, err)
		}
	}
	for _, length := range []int{2, 200} {
		list := strings.Repeat("A", length)
		got, kind, err := canonicalURL(testVideo + "&list=" + list + "&index=12")
		if err != nil || got != "https://www.youtube.com/playlist?list="+list || kind != "playlist" {
			t.Errorf("whole playlist canonicalization: %q %q %v", got, kind, err)
		}
	}
	show, kind, err := canonicalURL("https://www.youtube.com/show/VLPLrpqCP1_qdKhShZPqyWk9xJp0HIqRKc-t?sbp=Kgs5NllzeXJJNEt2OEAB")
	if err != nil || show != "https://www.youtube.com/playlist?list=PLrpqCP1_qdKhShZPqyWk9xJp0HIqRKc-t" || kind != "playlist" {
		t.Errorf("show playlist canonicalization: %q %q %v", show, kind, err)
	}
	for _, raw := range []string{
		"http://youtube.com/watch?v=dQw4w9WgXcQ", "https://youtube.com:443/watch?v=dQw4w9WgXcQ",
		"https://user@youtube.com/watch?v=dQw4w9WgXcQ", "https://youtube.com.evil/watch?v=dQw4w9WgXcQ",
		"https://youtube.com/watch?v=--exec", testVideo + "&v=dQw4w9WgXcQ",
		"https://youtube.com/playlist?list=PL%20bad", "https://youtube.com/%77atch?v=dQw4w9WgXcQ",
		"https://youtube.com/channel/dQw4w9WgXcQ?list=PL0123456789ABC",
		"https://youtube.com/shorts/dQw4w9WgXcQ/extra?list=PL0123456789ABC",
		"https://youtu.be/playlist?list=PL0123456789ABC", "https://youtube.com/watch?list=PL0123456789ABC",
		"https://youtube.com/playlist?list=A", "https://youtube.com/playlist?list=" + strings.Repeat("A", 201),
		"https://www.youtube.com/show/PLrpqCP1_qdKhShZPqyWk9xJp0HIqRKc-t",
	} {
		if _, _, err := canonicalURL(raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
}

func TestAPIContractAndSecurity(t *testing.T) {
	fake := fixtureClient(2)
	fake.listFn = func(ctx context.Context) (*youtube.Playlist, error) { <-ctx.Done(); return nil, ctx.Err() }
	s := testServer(t, fake, nil)
	w := request(s, "GET", "/api/health", "", map[string]string{"Authorization": ""})
	var health struct {
		Ready        bool            `json:"ready"`
		Missing      json.RawMessage `json:"missing"`
		Engine       string          `json:"engine"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if json.Unmarshal(w.Body.Bytes(), &health) != nil || !health.Ready || health.Engine != "native-go" || string(health.Missing) != "[]" || len(health.Capabilities) != 6 || health.Capabilities["combinedStreamsOnly"] || !health.Capabilities["adaptiveStreamsSupported"] || health.Capabilities["externalBinariesRequired"] || !health.Capabilities["mp3AudioSupported"] || !health.Capabilities["pureGoAudioConversion"] || !health.Capabilities["captionsSupported"] {
		t.Fatalf("native health contract: %s", w.Body.String())
	}
	if body := request(s, "GET", "/api/jobs", "", nil).Body.String(); strings.TrimSpace(body) != `{"jobs":[]}` {
		t.Fatalf("empty jobs contract: %s", body)
	}
	valid := `{"url":"` + testPlaylist + `","quality":"best","category":"Music","rightsConfirmed":true}`
	for _, tt := range []struct {
		name, body string
		headers    map[string]string
		code       int
	}{
		{name: "origin", body: valid, headers: map[string]string{"Origin": "https://evil.invalid"}, code: 403},
		{name: "host", body: valid, headers: map[string]string{"Host": "evil.invalid:8080"}, code: 403},
		{name: "auth", body: valid, headers: map[string]string{"Authorization": ""}, code: 401},
		{name: "rights", body: strings.Replace(valid, "true", "false", 1), code: 400},
		{name: "category", body: strings.Replace(valid, `"Music"`, `"Not configured"`, 1), code: 400},
		{name: "audio-format", body: `{"url":"` + testVideo + `","quality":"best","mediaType":"audio","audioFormat":"flac","rightsConfirmed":true}`, code: 400},
		{name: "video-items", body: `{"url":"` + testVideo + `","quality":"best","rightsConfirmed":true,"items":[{"index":1,"id":"dQw4w9WgXcQ","title":"Item"}]}`, code: 400},
		{name: "playlist-invalid-item", body: `{"url":"` + testPlaylist + `","quality":"best","rightsConfirmed":true,"items":[{"index":0,"id":"00000000001","title":"Item"}]}`, code: 400},
		{name: "playlist-duplicate-index", body: `{"url":"` + testPlaylist + `","quality":"best","rightsConfirmed":true,"items":[{"index":1,"id":"00000000001","title":"One"},{"index":1,"id":"00000000002","title":"Two"}]}`, code: 400},
		{name: "unknown", body: strings.Replace(valid, `"quality"`, `"unknown":1,"quality"`, 1), code: 400},
		{name: "trailing", body: valid + `{}`, code: 400}, {name: "null", body: "null", code: 400},
		{name: "oversized", body: `{"url":"` + strings.Repeat("x", 5000) + `"}`, code: 400},
		{name: "type", body: valid, headers: map[string]string{"Content-Type": "text/plain"}, code: 415},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := request(s, "POST", "/api/jobs", tt.body, tt.headers)
			var response map[string]string
			if w.Code != tt.code || json.Unmarshal(w.Body.Bytes(), &response) != nil || response["error"] == "" {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
	if w := request(s, "OPTIONS", "/api/jobs", "", map[string]string{"Authorization": "", "Origin": "http://localhost:5173"}); w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" || w.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, DELETE, OPTIONS" {
		t.Fatal("approved preflight failed")
	}
	w = request(s, "POST", "/api/jobs", valid, nil)
	var value map[string]json.RawMessage
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &value) != nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	for _, key := range []string{"id", "url", "kind", "quality", "category", "status", "title", "currentItem", "error", "createdAt", "note"} {
		if raw := value[key]; len(raw) == 0 || raw[0] != '"' {
			t.Errorf("%s must always be a string", key)
		}
	}
	for _, key := range []string{"files", "failures"} {
		if string(value[key]) != "[]" {
			t.Errorf("%s must always be an array", key)
		}
	}
	if string(value["category"]) != `"Music"` {
		t.Fatalf("selected category was not captured: %s", value["category"])
	}
	if string(value["progress"]) != "null" || string(value["totalCount"]) != "null" {
		t.Fatal("unknown metrics must be present as null")
	}
	nilServer := testServer(t, nil, nil)
	if w := request(nilServer, "POST", "/api/jobs", valid, nil); w.Code != 503 {
		t.Fatal("uninitialized engine accepted a job")
	}
}

func TestSubtitleSidecarDownloadAndPublishing(t *testing.T) {
	fake := fixtureClient(1)
	s := testServer(t, fake, nil)
	s.captionFetcher = func(_ context.Context, track youtube.CaptionTrack, format string) ([]byte, error) {
		if track.LanguageCode != "en" || format != "srt" {
			return nil, errors.New("unexpected caption request")
		}
		return []byte("1\n00:00:00,000 --> 00:00:01,000\nFixture caption\n\n"), nil
	}

	body, _ := json.Marshal(map[string]any{
		"url": testVideo, "quality": "best", "rightsConfirmed": true,
		"subtitleLanguage": "en", "subtitleFormat": "srt",
	})
	created := request(s, "POST", "/api/jobs", string(body), nil)
	if created.Code != 202 {
		t.Fatalf("create subtitle job: %d %s", created.Code, created.Body.String())
	}
	var queued Job
	if err := json.Unmarshal(created.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	completed := waitTerminal(t, s, queued.ID)
	if completed.Status != "completed" || len(completed.Files) != 1 {
		t.Fatalf("subtitle job did not complete: %+v", completed)
	}
	file := completed.Files[0]
	if file.Subtitle == nil || file.Subtitle.LanguageCode != "en" || file.Subtitle.Format != "srt" || !file.Subtitle.ManagedAvailable || !file.Subtitle.PublishedAvailable || file.SubtitleError != "" {
		t.Fatalf("caption sidecar metadata missing: %+v", file)
	}
	managed, err := os.ReadFile(filepath.Join(s.cfg.root, completed.ID, file.Subtitle.Name))
	if err != nil || !strings.Contains(string(managed), "Fixture caption") {
		t.Fatalf("managed caption sidecar missing: %v %q", err, string(managed))
	}
	publishedPath := filepath.Join(s.settings.DownloadLocation, filepath.FromSlash(file.Subtitle.OutputRelativePath))
	published, err := os.ReadFile(publishedPath)
	if err != nil || string(published) != string(managed) {
		t.Fatalf("published caption sidecar missing: %v", err)
	}

	archiveTicket := request(s, "POST", "/api/jobs/"+completed.ID+"/ticket", `{}`, nil)
	var ticket map[string]string
	if archiveTicket.Code != 200 || json.Unmarshal(archiveTicket.Body.Bytes(), &ticket) != nil {
		t.Fatalf("archive ticket: %d %s", archiveTicket.Code, archiveTicket.Body.String())
	}
	archiveResponse := request(s, "GET", ticket["path"], "", nil)
	if archiveResponse.Code != 200 {
		t.Fatalf("archive download: %d %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(archiveResponse.Body.Bytes()), int64(archiveResponse.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	foundSidecar := false
	for _, entry := range zr.File {
		if entry.Name == file.Subtitle.Name {
			foundSidecar = true
		}
	}
	if !foundSidecar {
		t.Fatal("archive omitted managed caption sidecar")
	}

	if w := request(s, "POST", "/api/jobs", `{"url":"`+testVideo+`","quality":"best","rightsConfirmed":true,"subtitleLanguage":"../en","subtitleFormat":"vtt"}`, nil); w.Code != 400 {
		t.Fatalf("unsafe subtitle language accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestInspectionPreferencesRetryAndRemoval(t *testing.T) {
	fake := fixtureClient(3)
	s := testServer(t, fake, nil)

	inspectionResponse := request(s, "POST", "/api/inspect", `{"url":"`+testVideo+`"}`, nil)
	var inspected inspection
	if inspectionResponse.Code != 200 || json.Unmarshal(inspectionResponse.Body.Bytes(), &inspected) != nil {
		t.Fatalf("inspect video: %d %s", inspectionResponse.Code, inspectionResponse.Body.String())
	}
	if inspected.Title != "Fixture video" || inspected.Author != "Fixture channel" || inspected.DurationSeconds != 300 || inspected.ThumbnailURL == "" || len(inspected.AvailableQuality) == 0 || len(inspected.CaptionTracks) != 2 {
		t.Fatalf("inspection omitted native metadata, captions, or supported quality: %+v", inspected)
	}
	playlistResponse := request(s, "POST", "/api/inspect", `{"url":"`+testPlaylist+`"}`, nil)
	var playlistInspection inspection
	if playlistResponse.Code != 200 || json.Unmarshal(playlistResponse.Body.Bytes(), &playlistInspection) != nil || playlistInspection.ItemCount != 3 || len(playlistInspection.Items) != 3 {
		t.Fatalf("inspect playlist: %d %s", playlistResponse.Code, playlistResponse.Body.String())
	}

	settings := request(s, "GET", "/api/settings", "", nil)
	if settings.Code != 200 || !strings.Contains(settings.Body.String(), `"defaultQuality":"best"`) {
		t.Fatalf("default settings: %d %s", settings.Code, settings.Body.String())
	}
	settings = request(s, "PUT", "/api/settings", `{"defaultQuality":"720"}`, nil)
	if settings.Code != 200 || !strings.Contains(settings.Body.String(), `"defaultQuality":"720"`) {
		t.Fatalf("save settings: %d %s", settings.Code, settings.Body.String())
	}
	settings = request(s, "PUT", "/api/settings", `{"bandwidthLimitBytesPerSec":5242880}`, nil)
	if settings.Code != 200 || !strings.Contains(settings.Body.String(), `"bandwidthLimitBytesPerSec":5242880`) || s.bandwidth.Limit() != 5242880 {
		t.Fatalf("save bandwidth setting: %d %s limit=%d", settings.Code, settings.Body.String(), s.bandwidth.Limit())
	}
	settings = request(s, "PUT", "/api/settings", `{"notificationsEnabled":true}`, nil)
	if settings.Code != 200 || !strings.Contains(settings.Body.String(), `"notificationsEnabled":true`) || !s.settings.NotificationsEnabled {
		t.Fatalf("save notification setting: %d %s", settings.Code, settings.Body.String())
	}
	if request(s, "PUT", "/api/settings", `{"bandwidthLimitBytesPerSec":-1}`, nil).Code != 400 ||
		request(s, "PUT", "/api/settings", `{"bandwidthLimitBytesPerSec":1073741825}`, nil).Code != 400 {
		t.Fatal("invalid bandwidth limit was accepted")
	}
	if request(s, "PUT", "/api/settings", `{"defaultQuality":"2160"}`, nil).Code != 400 {
		t.Fatal("unsupported quality preference was accepted")
	}
	if request(s, "PUT", "/api/settings", `{"storageMode":"unknown"}`, nil).Code != 400 {
		t.Fatal("unsupported storage mode was accepted")
	}

	fake.videoFn = func(context.Context, string) (*youtube.Video, error) { return nil, errors.New("fixture failure") }
	failed := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	if failed.Status != "failed" {
		t.Fatalf("fixture job status = %s", failed.Status)
	}
	retriedResponse := request(s, "POST", "/api/jobs/"+failed.ID+"/retry", `{"rightsConfirmed":true}`, nil)
	var retried Job
	if retriedResponse.Code != 202 || json.Unmarshal(retriedResponse.Body.Bytes(), &retried) != nil || retried.ID == failed.ID || retried.Quality != failed.Quality {
		t.Fatalf("retry: %d %s", retriedResponse.Code, retriedResponse.Body.String())
	}
	if request(s, "POST", "/api/jobs/"+failed.ID+"/retry", `{"rightsConfirmed":false}`, nil).Code != 400 {
		t.Fatal("retry without authorization confirmation was accepted")
	}
	waitTerminal(t, s, retried.ID)
	if code := request(s, "DELETE", "/api/jobs/"+failed.ID, "", nil).Code; code != 204 {
		t.Fatalf("delete failed job: %d", code)
	}
	if code := request(s, "GET", "/api/jobs/"+failed.ID, "", nil).Code; code != 404 {
		t.Fatalf("deleted job still available: %d", code)
	}
}

func TestLibraryRemovalPreservesPublishedMedia(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	j := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	if len(j.Files) != 1 || !j.Files[0].ManagedAvailable || !j.Files[0].PublishedAvailable {
		t.Fatalf("completed file availability not recorded: %+v", j.Files)
	}
	outputPath := trackedOutputPath(t, s, j.ID, j.Files[0].ID)
	if outputPath == "" {
		t.Fatal("completed file has no published output path")
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("published output missing before Library removal: %v", err)
	}
	if response := request(s, "DELETE", "/api/jobs/"+j.ID, "", nil); response.Code != 204 {
		t.Fatalf("remove from Library: %d %s", response.Code, response.Body.String())
	}
	if request(s, "GET", "/api/jobs/"+j.ID, "", nil).Code != 404 {
		t.Fatal("removed Library job is still available")
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("Library removal deleted published media: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.root, j.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Library removal retained app-managed media: %v", err)
	}
	_ = os.Remove(outputPath)
}

func TestScopedMediaDeletion(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)

	publishedJob := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	publishedPath := trackedOutputPath(t, s, publishedJob.ID, publishedJob.Files[0].ID)
	response := request(s, "DELETE", "/api/jobs/"+publishedJob.ID+"/published", "", nil)
	var afterPublished Job
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &afterPublished) != nil {
		t.Fatalf("delete published copies: %d %s", response.Code, response.Body.String())
	}
	if afterPublished.Files[0].PublishedAvailable || !afterPublished.Files[0].ManagedAvailable {
		t.Fatalf("published deletion changed wrong copy state: %+v", afterPublished.Files[0])
	}
	if _, err := os.Stat(publishedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published file survived scoped deletion: %v", err)
	}
	if request(s, "POST", "/api/jobs/"+publishedJob.ID+"/ticket", `{"fileId":"`+afterPublished.Files[0].ID+`"}`, nil).Code != 200 {
		t.Fatal("managed copy became unavailable after published-only deletion")
	}

	managedJob := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	managedPath := trackedOutputPath(t, s, managedJob.ID, managedJob.Files[0].ID)
	response = request(s, "DELETE", "/api/jobs/"+managedJob.ID+"/managed", "", nil)
	var afterManaged Job
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &afterManaged) != nil {
		t.Fatalf("delete managed copies: %d %s", response.Code, response.Body.String())
	}
	if afterManaged.Files[0].ManagedAvailable || !afterManaged.Files[0].PublishedAvailable {
		t.Fatalf("managed deletion changed wrong copy state: %+v", afterManaged.Files[0])
	}
	if _, err := os.Stat(managedPath); err != nil {
		t.Fatalf("published output was removed with managed copy: %v", err)
	}
	if request(s, "POST", "/api/jobs/"+managedJob.ID+"/ticket", `{"fileId":"`+afterManaged.Files[0].ID+`"}`, nil).Code != 409 {
		t.Fatal("ticket was issued for a removed managed copy")
	}

	allJob := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	allPath := trackedOutputPath(t, s, allJob.ID, allJob.Files[0].ID)
	if response = request(s, "DELETE", "/api/jobs/"+allJob.ID+"/all", "", nil); response.Code != 204 {
		t.Fatalf("delete everywhere: %d %s", response.Code, response.Body.String())
	}
	if request(s, "GET", "/api/jobs/"+allJob.ID, "", nil).Code != 404 {
		t.Fatal("delete everywhere retained Library history")
	}
	if _, err := os.Stat(allPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete everywhere retained published media: %v", err)
	}
	_ = os.Remove(managedPath)
}

func TestStoragePolicies(t *testing.T) {
	for _, tt := range []struct {
		mode                 string
		managed, published   bool
	}{
		{mode: "managed-published", managed: true, published: true},
		{mode: "published-only", managed: false, published: true},
		{mode: "managed-only", managed: true, published: false},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			s := testServer(t, fixtureClient(1), nil)
			settings := request(s, "PUT", "/api/settings", `{"storageMode":"`+tt.mode+`"}`, nil)
			if settings.Code != 200 {
				t.Fatalf("set storage mode: %d %s", settings.Code, settings.Body.String())
			}
			j := waitTerminal(t, s, createJob(t, s, testVideo).ID)
			if j.StorageMode != tt.mode || len(j.Files) != 1 {
				t.Fatalf("storage policy not captured: %+v", j)
			}
			file := j.Files[0]
			if file.ManagedAvailable != tt.managed || file.PublishedAvailable != tt.published {
				t.Fatalf("availability for %s = managed:%t published:%t", tt.mode, file.ManagedAvailable, file.PublishedAvailable)
			}
			managedPath := filepath.Join(s.cfg.root, j.ID, file.Name)
			_, managedErr := os.Stat(managedPath)
			if tt.managed && managedErr != nil {
				t.Fatalf("managed copy missing for %s: %v", tt.mode, managedErr)
			}
			if !tt.managed && !errors.Is(managedErr, os.ErrNotExist) {
				t.Fatalf("managed copy unexpectedly retained for %s: %v", tt.mode, managedErr)
			}
			outputPath := trackedOutputPath(t, s, j.ID, file.ID)
			if tt.published {
				if outputPath == "" {
					t.Fatalf("published output path missing for %s", tt.mode)
				}
				if _, err := os.Stat(outputPath); err != nil {
					t.Fatalf("published copy missing for %s: %v", tt.mode, err)
				}
				_ = os.Remove(outputPath)
			} else if outputPath != "" {
				t.Fatalf("managed-only job unexpectedly published to %q", outputPath)
			}
		})
	}
}

func TestOriginalM4AAudioPreservesSourceBytes(t *testing.T) {
	fake := fixtureClient(1)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		video := fixtureVideo(id)
		video.Formats = youtube.FormatList{{
			ItagNo: 140,
			MimeType: `audio/mp4; codecs="mp4a.40.2"`,
			AudioChannels: 2,
			AudioSampleRate: "44100",
			ContentLength: int64(len(fixtureData)),
		}}
		return video, nil
	}
	s := testServer(t, fake, nil)
	body := `{"url":"` + testVideo + `","quality":"best","mediaType":"audio","audioFormat":"m4a","rightsConfirmed":true}`
	response := request(s, "POST", "/api/jobs", body, nil)
	var created Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create M4A job: %d %s", response.Code, response.Body.String())
	}
	if created.AudioFormat != "m4a" || created.AudioBitrate != "" {
		t.Fatalf("audio request was not normalized: %+v", created)
	}
	j := waitTerminal(t, s, created.ID)
	if j.Status != "completed" || len(j.Files) != 1 {
		t.Fatalf("M4A job failed: %+v", j)
	}
	file := j.Files[0]
	if !strings.HasSuffix(file.Name, ".m4a") || file.MimeType != "audio/mp4" || file.MediaType != "audio" {
		t.Fatalf("unexpected M4A metadata: %+v", file)
	}
	handle, err := openFinal(filepath.Join(s.cfg.root, j.ID), file.Name)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(handle)
	_ = handle.Close()
	if readErr != nil || string(data) != fixtureData {
		t.Fatalf("M4A source bytes changed: %q err=%v", data, readErr)
	}
	outputPath := trackedOutputPath(t, s, j.ID, file.ID)
	if outputPath == "" {
		t.Fatal("M4A output was not published")
	}
	_ = os.Remove(outputPath)
}

func TestRetrySinglePlaylistItemPreservesSuccessfulFiles(t *testing.T) {
	fake := fixtureClient(3)
	var secondAttempts atomic.Int32
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		if id == "00000000002" && secondAttempts.Add(1) == 1 {
			return nil, errors.New("fixture item failure")
		}
		return fixtureVideo(id), nil
	}
	s := testServer(t, fake, nil)
	initial := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	if initial.Status != "partial" || len(initial.Files) != 2 || len(initial.Items) != 3 || initial.Items[1].Status != "failed" {
		t.Fatalf("playlist fixture did not produce one failed item: %+v", initial)
	}
	firstFileID, thirdFileID := initial.Items[0].FileID, initial.Items[2].FileID
	if firstFileID == "" || thirdFileID == "" {
		t.Fatalf("successful sibling files were not finalized: %+v", initial.Items)
	}
	if response := request(s, "POST", "/api/jobs/"+initial.ID+"/retry-item", `{"index":1}`, nil); response.Code != 409 {
		t.Fatalf("completed playlist item was accepted for retry: %d %s", response.Code, response.Body.String())
	}
	response := request(s, "POST", "/api/jobs/"+initial.ID+"/retry-item", `{"index":2}`, nil)
	var queued Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &queued) != nil {
		t.Fatalf("retry failed item: %d %s", response.Code, response.Body.String())
	}
	if queued.Status != "queued" || !queued.Items[1].RetryRequested || queued.Items[0].RetryRequested || queued.Items[2].RetryRequested {
		t.Fatalf("single retry intent was not isolated: %+v", queued.Items)
	}

	completed := waitTerminal(t, s, initial.ID)
	if completed.Status != "completed" || len(completed.Files) != 3 || len(completed.Failures) != 0 || secondAttempts.Load() != 2 {
		t.Fatalf("single item retry did not complete cleanly: status=%s files=%d failures=%v attempts=%d error=%q",
			completed.Status, len(completed.Files), completed.Failures, secondAttempts.Load(), completed.Error)
	}
	if completed.Items[0].FileID != firstFileID || completed.Items[2].FileID != thirdFileID {
		t.Fatalf("successful sibling files changed during one-item retry: before=%q/%q after=%q/%q",
			firstFileID, thirdFileID, completed.Items[0].FileID, completed.Items[2].FileID)
	}
	if completed.Items[1].FileID == "" || completed.Items[1].RetryRequested {
		t.Fatalf("retried item did not finalize or retry intent survived: %+v", completed.Items[1])
	}
	for _, file := range completed.Files {
		if path := trackedOutputPath(t, s, completed.ID, file.ID); path != "" {
			_ = os.Remove(path)
		}
	}
}

func TestPlaylistItemSelectionPreservesOriginalPositions(t *testing.T) {
	fake := fixtureClient(5)
	var processed atomic.Int32
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		processed.Add(1)
		return fixtureVideo(id), nil
	}
	s := testServer(t, fake, nil)
	body := `{"url":"` + testPlaylist + `","quality":"best","rightsConfirmed":true,"items":[{"index":2,"id":"00000000002","title":"Item 2"},{"index":4,"id":"00000000004","title":"Item 4"}]}`
	response := request(s, "POST", "/api/jobs", body, nil)
	var created Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create selected playlist: %d %s", response.Code, response.Body.String())
	}
	if len(created.Items) != 2 || created.Items[0].Index != 1 || created.Items[0].PlaylistIndex != 2 || created.Items[1].Index != 2 || created.Items[1].PlaylistIndex != 4 {
		t.Fatalf("queued selection did not preserve indexes: %+v", created.Items)
	}
	j := waitTerminal(t, s, created.ID)
	if j.Status != "completed" || j.TotalCount == nil || *j.TotalCount != 2 || len(j.Files) != 2 || processed.Load() != 2 {
		t.Fatalf("selected playlist did not complete only two items: status=%s total=%v files=%d processed=%d error=%q", j.Status, j.TotalCount, len(j.Files), processed.Load(), j.Error)
	}
	if !strings.HasPrefix(j.Files[0].Name, "000002-") || !strings.HasPrefix(j.Files[1].Name, "000004-") {
		t.Fatalf("original playlist positions were not retained in filenames: %+v", j.Files)
	}
	if len(j.Items) != 2 || j.Items[0].Index != 1 || j.Items[0].PlaylistIndex != 2 || j.Items[1].Index != 2 || j.Items[1].PlaylistIndex != 4 {
		t.Fatalf("final queue indexes are not contiguous/original-position aware: %+v", j.Items)
	}
	if !strings.Contains(j.Note, playlistSelectionNote) {
		t.Fatalf("selection note missing: %q", j.Note)
	}
	for _, file := range j.Files {
		if path := trackedOutputPath(t, s, j.ID, file.ID); path != "" {
			_ = os.Remove(path)
		}
	}
}

func TestPlaylistItemSelectionRejectsStaleMetadata(t *testing.T) {
	s := testServer(t, fixtureClient(3), nil)
	body := `{"url":"` + testPlaylist + `","quality":"best","rightsConfirmed":true,"items":[{"index":2,"id":"00000000003","title":"Stale item"}]}`
	response := request(s, "POST", "/api/jobs", body, nil)
	var created Job
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &created) != nil {
		t.Fatalf("create stale selection: %d %s", response.Code, response.Body.String())
	}
	j := waitTerminal(t, s, created.ID)
	if j.Status != "failed" || len(j.Files) != 0 || !strings.Contains(j.Error, errPlaylistSelection.Error()) {
		t.Fatalf("stale playlist selection was not rejected: %+v", j)
	}

	retry := request(s, "POST", "/api/jobs/"+j.ID+"/retry", `{"rightsConfirmed":true}`, nil)
	var retried Job
	if retry.Code != 202 || json.Unmarshal(retry.Body.Bytes(), &retried) != nil {
		t.Fatalf("retry stale selection: %d %s", retry.Code, retry.Body.String())
	}
	if len(retried.Items) != 1 || retried.Items[0].PlaylistIndex != 2 || retried.Items[0].VideoID != "00000000003" {
		t.Fatalf("retry did not preserve playlist selection: %+v", retried.Items)
	}
	waitTerminal(t, s, retried.ID)
}

func TestLocalThumbnailCapturePersistenceAndServing(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	const thumbnailData = "fixture-thumbnail"
	s.thumbnailFetcher = func(_ context.Context, rawURL, destination string) (string, error) {
		if safeInspectedThumbnailURL(rawURL) == "" {
			t.Fatalf("thumbnail fetch received unsafe URL %q", rawURL)
		}
		if err := os.WriteFile(destination, []byte(thumbnailData), 0600); err != nil {
			return "", err
		}
		return "image/jpeg", nil
	}

	j := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	if len(j.Files) != 1 {
		t.Fatalf("thumbnail fixture finalized %d files", len(j.Files))
	}
	file := j.Files[0]
	if !file.ThumbnailLocalAvailable || file.ThumbnailMimeType != "image/jpeg" || file.ThumbnailURL == "" {
		t.Fatalf("local thumbnail metadata missing: %+v", file)
	}

	response := request(s, "GET", "/api/jobs/"+j.ID+"/thumbnail?fileId="+file.ID, "", nil)
	if response.Code != 200 || response.Body.String() != thumbnailData || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("serve local thumbnail: %d %q %q", response.Code, response.Body.String(), response.Header().Get("Content-Type"))
	}
	if request(s, "GET", "/api/jobs/"+j.ID+"/thumbnail?fileId=../other", "", nil).Code != 400 {
		t.Fatal("thumbnail endpoint accepted unsafe file id")
	}
	if request(s, "GET", "/api/jobs/"+j.ID+"/thumbnail?fileId=missing", "", nil).Code != 404 {
		t.Fatal("thumbnail endpoint accepted unknown file id")
	}

	loaded, err := s.store.loadJobs(s.cfg.root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, stored := range loaded {
		if stored.job.ID != j.ID || len(stored.job.Files) != 1 {
			continue
		}
		persisted := stored.job.Files[0]
		found = persisted.ThumbnailLocalAvailable && persisted.ThumbnailMimeType == "image/jpeg"
	}
	if !found {
		t.Fatal("local thumbnail state did not persist through SQLite")
	}
}

func TestNativeFolderSelectionEndpoint(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	selected := filepath.Join(s.cfg.root, "chosen-output")
	s.folderSelector = func(context.Context) (string, error) { return selected, nil }
	response := request(s, "POST", "/api/folders/select", `{}`, nil)
	var result struct{ Path string `json:"path"` }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Path != selected {
		t.Fatalf("folder selection: %d %s", response.Code, response.Body.String())
	}
	if request(s, "GET", "/api/folders/select", "", nil).Code != 405 {
		t.Fatal("folder selector accepted unsupported method")
	}
	s.folderSelector = func(context.Context) (string, error) { return "", errFolderSelectionCancelled }
	if response = request(s, "POST", "/api/folders/select", `{}`, nil); response.Code != 204 {
		t.Fatalf("cancelled folder selection: %d %s", response.Code, response.Body.String())
	}
	s.folderSelector = func(context.Context) (string, error) { return "relative/path", nil }
	if response = request(s, "POST", "/api/folders/select", `{}`, nil); response.Code != 500 {
		t.Fatal("folder selector accepted a relative path")
	}
}

func TestTrackedFilesystemActions(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	j := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	if len(j.Files) != 1 || !j.Files[0].PublishedAvailable {
		t.Fatalf("fixture did not publish media: %+v", j.Files)
	}
	file := j.Files[0]
	outputPath := trackedOutputPath(t, s, j.ID, file.ID)
	var openedAction, openedPath string
	s.filesystemOpener = func(_ context.Context, action, path string) error {
		openedAction, openedPath = action, path
		return nil
	}

	response := request(s, "POST", "/api/jobs/"+j.ID+"/filesystem", `{"fileId":"`+file.ID+`","action":"reveal"}`, nil)
	if response.Code != 200 || openedAction != "reveal" || openedPath != outputPath {
		t.Fatalf("reveal tracked output: %d action=%q path=%q body=%s", response.Code, openedAction, openedPath, response.Body.String())
	}

	openedAction, openedPath = "", ""
	response = request(s, "POST", "/api/jobs/"+j.ID+"/filesystem", `{"fileId":"`+file.ID+`","action":"copy-path"}`, nil)
	var copied struct{ Path string `json:"path"` }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &copied) != nil || copied.Path != outputPath || openedAction != "" {
		t.Fatalf("copy tracked path: %d %+v opener=%q", response.Code, copied, openedAction)
	}
	if request(s, "POST", "/api/jobs/"+j.ID+"/filesystem", `{"fileId":"../other","action":"reveal"}`, nil).Code != 404 {
		t.Fatal("filesystem action accepted unknown file")
	}
	if request(s, "POST", "/api/jobs/"+j.ID+"/filesystem", `{"fileId":"`+file.ID+`","action":"execute"}`, nil).Code != 400 {
		t.Fatal("filesystem action accepted unsupported operation")
	}
	if request(s, "DELETE", "/api/jobs/"+j.ID+"/published", "", nil).Code != 200 {
		t.Fatal("could not remove published fixture")
	}
	if request(s, "POST", "/api/jobs/"+j.ID+"/filesystem", `{"fileId":"`+file.ID+`","action":"reveal"}`, nil).Code != 409 {
		t.Fatal("filesystem action accepted removed published media")
	}
}

type activePauseTestStream struct {
	ctx       context.Context
	remaining int
	started   chan<- struct{}
}

func (r *activePauseTestStream) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-time.After(12 * time.Millisecond):
	}
	n := min(len(p), 8192, r.remaining)
	copy(p[:n], bytes.Repeat([]byte{0x2a}, n))
	r.remaining -= n
	return n, nil
}

func (r *activePauseTestStream) Close() error { return nil }

func TestPauseDuringActiveStreamRead(t *testing.T) {
	const size = 2 << 20
	fake := fixtureClient(1)
	started := make(chan struct{}, 1)
	var streamCalls atomic.Int32
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		video := fixtureVideo(id)
		video.Formats[0].ContentLength = size
		return video, nil
	}
	fake.streamFn = func(ctx context.Context, _ *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
		if streamCalls.Add(1) == 1 {
			return &activePauseTestStream{ctx: ctx, remaining: size, started: started}, size, nil
		}
		return io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{0x2a}, size))), size, nil
	}

	s := testServer(t, fake, func(c *config) { c.maxBytes = 4 << 20 })
	job := createJob(t, s, testVideo)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("active stream did not begin reading")
	}
	if response := request(s, "POST", "/api/jobs/"+job.ID+"/pause", "", nil); response.Code != 200 {
		t.Fatalf("pause active stream: %d %s", response.Code, response.Body.String())
	}
	paused := waitJob(t, s, job.ID, func(value Job) bool { return value.Status == "paused" })
	if len(paused.Failures) != 0 {
		t.Fatalf("active-stream pause was recorded as failure: %+v", paused.Failures)
	}
	if response := request(s, "POST", "/api/jobs/"+job.ID+"/resume", "", nil); response.Code != 200 {
		t.Fatalf("resume active stream: %d %s", response.Code, response.Body.String())
	}
	completed := waitTerminal(t, s, job.ID)
	if completed.Status != "completed" || streamCalls.Load() < 2 {
		t.Fatalf("active-stream resume did not complete: status=%s calls=%d", completed.Status, streamCalls.Load())
	}
}

func TestPauseAndResumeJob(t *testing.T) {
	fake := fixtureClient(1)
	var streamCalls atomic.Int32
	fake.streamFn = func(ctx context.Context, _ *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
		if streamCalls.Add(1) == 1 {
			<-ctx.Done()
			return nil, 0, ctx.Err()
		}
		return io.NopCloser(strings.NewReader(fixtureData)), int64(len(fixtureData)), nil
	}
	s := testServer(t, fake, nil)
	job := createJob(t, s, testVideo)
	waitJob(t, s, job.ID, func(value Job) bool { return value.Status == "downloading" })
	paused := request(s, "POST", "/api/jobs/"+job.ID+"/pause", "", nil)
	if paused.Code != 200 {
		t.Fatalf("pause request: %d %s", paused.Code, paused.Body.String())
	}
	pausedJob := waitJob(t, s, job.ID, func(value Job) bool { return value.Status == "paused" })
	if len(pausedJob.Failures) != 0 {
		t.Fatalf("pausing was incorrectly recorded as a failed item: %+v", pausedJob.Failures)
	}
	resumed := request(s, "POST", "/api/jobs/"+job.ID+"/resume", "", nil)
	if resumed.Code != 200 {
		t.Fatalf("resume request: %d %s", resumed.Code, resumed.Body.String())
	}
	completed := waitTerminal(t, s, job.ID)
	if completed.Status != "completed" || streamCalls.Load() < 2 {
		t.Fatalf("resumed job did not finish: status=%s stream calls=%d", completed.Status, streamCalls.Load())
	}
}

func TestCancelPausedJobRemovesTemporaryFiles(t *testing.T) {
	fake := fixtureClient(1)
	fake.streamFn = func(ctx context.Context, _ *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
		<-ctx.Done()
		return nil, 0, ctx.Err()
	}
	s := testServer(t, fake, nil)
	job := createJob(t, s, testVideo)
	waitJob(t, s, job.ID, func(value Job) bool { return value.Status == "downloading" })
	if response := request(s, "POST", "/api/jobs/"+job.ID+"/pause", "", nil); response.Code != 200 {
		t.Fatalf("pause: %d %s", response.Code, response.Body.String())
	}
	waitJob(t, s, job.ID, func(value Job) bool { return value.Status == "paused" })
	partialPath := filepath.Join(s.jobs[job.ID].dir, "test-output.part")
	if err := os.WriteFile(partialPath, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if response := request(s, "POST", "/api/jobs/"+job.ID+"/cancel", "", nil); response.Code != 200 {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("paused temporary output remains after cancellation: %v", err)
	}
}

func ticketPath(t *testing.T, s *server, id, body string) string {
	t.Helper()
	w := request(s, "POST", "/api/jobs/"+id+"/ticket", body, nil)
	var result struct{ Path string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !strings.HasPrefix(result.Path, "/api/downloads/") {
		t.Fatalf("ticket: %d %s", w.Code, w.Body.String())
	}
	return result.Path
}

func TestInlinePreviewTicketSupportsRanges(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	j := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	if j.Status != "completed" || len(j.Files) != 1 {
		t.Fatalf("preview fixture failed: %+v", j)
	}
	if response := request(s, "POST", "/api/jobs/"+j.ID+"/ticket", `{"inline":true}`, nil); response.Code != 400 {
		t.Fatalf("inline archive ticket accepted: %d %s", response.Code, response.Body.String())
	}
	path := ticketPath(t, s, j.ID, `{"fileId":"`+j.Files[0].ID+`","inline":true}`)
	response := request(s, "GET", path, "", map[string]string{"Authorization": "", "Range": "bytes=0-6"})
	if response.Code != 206 || response.Body.String() != "fixture" {
		t.Fatalf("inline preview range: %d %q", response.Code, response.Body.String())
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, "inline") {
		t.Fatalf("preview disposition = %q", disposition)
	}
	if response.Header().Get("Content-Type") != j.Files[0].MimeType {
		t.Fatalf("preview MIME = %q, want %q", response.Header().Get("Content-Type"), j.Files[0].MimeType)
	}
}

func TestDownloadsAndRetention(t *testing.T) {
	s := testServer(t, fixtureClient(2), nil)
	j := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	if j.Status != "completed" {
		t.Fatalf("fixture failed: %+v", j)
	}
	for _, invalid := range []string{"null", `{"fileId":null}`, `{"fileId":""}`} {
		if request(s, "POST", "/api/jobs/"+j.ID+"/ticket", invalid, nil).Code != 400 {
			t.Fatal("invalid ticket body accepted")
		}
	}
	if request(s, "POST", "/api/jobs/"+j.ID+"/ticket", `{"fileId":"../other"}`, nil).Code != 404 {
		t.Fatal("unknown file accepted")
	}
	path := ticketPath(t, s, j.ID, `{"fileId":"`+j.Files[0].ID+`"}`)
	w := request(s, "GET", path, "", map[string]string{"Authorization": "", "Range": "bytes=0-6"})
	if w.Code != 206 || w.Body.String() != "fixture" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("scoped single-file range failed")
	}
	w = request(s, "GET", path+"?fileId="+j.Files[1].ID, "", map[string]string{"Authorization": ""})
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), j.Files[0].Name) {
		t.Fatal("ticket changed scope through query parameters")
	}
	w = request(s, "GET", path, "", map[string]string{"Range": "bytes=900-"})
	if w.Code != 416 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("invalid range was not a JSON error")
	}
	archivePath := ticketPath(t, s, j.ID, `{}`)
	w = request(s, "GET", archivePath, "", map[string]string{"Authorization": ""})
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil || w.Code != 200 || len(reader.File) != 2 {
		t.Fatalf("incomplete archive: %v", err)
	}
	for _, entry := range reader.File {
		f, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil || string(data) != fixtureData || strings.Contains(entry.Name, "/") {
			t.Fatal("archive content or entry name is invalid")
		}
	}
	publishedPaths := make([]string, 0, len(j.Files))
	for _, file := range j.Files {
		publishedPaths = append(publishedPaths, trackedOutputPath(t, s, j.ID, file.ID))
	}
	s.mu.Lock()
	s.jobs[j.ID].done = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.prune(time.Now())
	if request(s, "GET", "/api/jobs/"+j.ID, "", nil).Code != 200 {
		t.Fatal("retention removed a job with a live ticket")
	}
	s.prune(time.Now().Add(6 * time.Minute))
	if request(s, "GET", path, "", nil).Code != 404 || request(s, "GET", "/api/jobs/"+j.ID, "", nil).Code != 404 {
		t.Fatal("expired job/ticket survived retention")
	}
	if _, err := os.Stat(filepath.Join(s.cfg.root, j.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retained app-managed output was not removed")
	}
	for _, outputPath := range publishedPaths {
		if _, err := os.Stat(outputPath); err != nil {
			t.Fatalf("retention deleted published media: %v", err)
		}
		_ = os.Remove(outputPath)
	}
}
