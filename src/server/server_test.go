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
		Formats:    youtube.FormatList{{ItagNo: 18, MimeType: `video/mp4; codecs="avc1.42001E, mp4a.40.2"`, Height: 360, Width: 640, FPS: 30, AudioChannels: 2, ContentLength: int64(len(fixtureData))}},
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
		addr: "127.0.0.1:8080", root: ".fixture-" + randomID(8), token: strings.Repeat("a", 32),
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
	s.engine = engine
	s.start()
	t.Cleanup(func() {
		s.stop()
		s.wg.Wait()
		if err := os.RemoveAll(s.cfg.root); err != nil {
			t.Error(err)
		}
	})
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
	if json.Unmarshal(w.Body.Bytes(), &health) != nil || !health.Ready || health.Engine != "native-go" || string(health.Missing) != "[]" || len(health.Capabilities) != 5 || health.Capabilities["combinedStreamsOnly"] || !health.Capabilities["adaptiveStreamsSupported"] || health.Capabilities["externalBinariesRequired"] || !health.Capabilities["mp3AudioSupported"] || !health.Capabilities["pureGoAudioConversion"] {
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

func TestInspectionPreferencesRetryAndRemoval(t *testing.T) {
	fake := fixtureClient(3)
	s := testServer(t, fake, nil)

	inspectionResponse := request(s, "POST", "/api/inspect", `{"url":"`+testVideo+`"}`, nil)
	var inspected inspection
	if inspectionResponse.Code != 200 || json.Unmarshal(inspectionResponse.Body.Bytes(), &inspected) != nil {
		t.Fatalf("inspect video: %d %s", inspectionResponse.Code, inspectionResponse.Body.String())
	}
	if inspected.Title != "Fixture video" || inspected.Author != "Fixture channel" || inspected.DurationSeconds != 300 || inspected.ThumbnailURL == "" || len(inspected.AvailableQuality) == 0 {
		t.Fatalf("inspection omitted native metadata or supported quality: %+v", inspected)
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
	outputPath := j.Files[0].OutputPath
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
	publishedPath := publishedJob.Files[0].OutputPath
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
	managedPath := managedJob.Files[0].OutputPath
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
	allPath := allJob.Files[0].OutputPath
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
			if tt.published {
				if file.OutputPath == "" {
					t.Fatalf("published output path missing for %s", tt.mode)
				}
				if _, err := os.Stat(file.OutputPath); err != nil {
					t.Fatalf("published copy missing for %s: %v", tt.mode, err)
				}
				_ = os.Remove(file.OutputPath)
			} else if file.OutputPath != "" {
				t.Fatalf("managed-only job unexpectedly published to %q", file.OutputPath)
			}
		})
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
		publishedPaths = append(publishedPaths, file.OutputPath)
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
