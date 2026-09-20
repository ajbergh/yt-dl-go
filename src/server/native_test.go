package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestNativeClientUsesAndroidProfile(t *testing.T) {
	_ = newNativeClient(time.Second)
	if youtube.DefaultClient.Name != youtube.AndroidClient.Name {
		t.Fatalf("native client profile = %q, want %q", youtube.DefaultClient.Name, youtube.AndroidClient.Name)
	}
}

func TestFormatSelection(t *testing.T) {
	video := fixtureVideo("dQw4w9WgXcQ")
	video.Formats = append(video.Formats,
		youtube.Format{ItagNo: 22, MimeType: "video/mp4", Height: 720, Width: 1280, AudioChannels: 2},
		youtube.Format{ItagNo: 37, MimeType: "video/webm", Height: 1080, Width: 1920, AudioChannels: 2},
		youtube.Format{ItagNo: 137, MimeType: "video/mp4", Height: 2160, Width: 3840, AudioChannels: 0},
		youtube.Format{ItagNo: 140, MimeType: "audio/mp4", Height: 0, AudioChannels: 2})
	for quality, height := range map[string]int{"best": 1080, "1080": 1080, "720": 720, "480": 360} {
		selection, err := selectFormat(video, quality)
		if err != nil || selection.video == nil || selection.video.Height != height || selection.video.AudioChannels <= 0 || !strings.HasPrefix(selection.kind, "video/") {
			t.Errorf("quality %s: itag=%d height=%d channels=%d kind=%s audio=%t progressive=%t err=%v", quality, selection.video.ItagNo, selection.video.Height, selection.video.AudioChannels, selection.kind, selection.audio != nil, selection.progressive != nil, err)
		}
	}
	video = fixtureVideo("dQw4w9WgXcQ")
	var adaptiveVideo, adaptiveAudio youtube.Format
	if err := json.Unmarshal([]byte(`{"itag":299,"mimeType":"video/mp4; codecs=\"avc1.64002a\"","height":1080,"width":1920,"fps":60,"bitrate":5000000,"audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`), &adaptiveVideo); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"itag":140,"mimeType":"audio/mp4; codecs=\"mp4a.40.2\"","audioChannels":2,"bitrate":128000,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`), &adaptiveAudio); err != nil {
		t.Fatal(err)
	}
	video.Formats = append(video.Formats, adaptiveVideo, adaptiveAudio)
	selection, err := selectFormat(video, "best")
	if err != nil || selection.audio == nil || selection.video == nil || selection.video.Height != 1080 {
		t.Fatalf("adaptive 1080p pair was not selected: %+v %v", selection, err)
	}

	video = fixtureVideo("dQw4w9WgXcQ")
	video.Formats = video.Formats[1:]
	if _, err := selectFormat(video, "best"); !errors.Is(err, errCombined) {
		t.Fatal("unsupported separate tracks were accepted")
	}
	video = fixtureVideo("dQw4w9WgXcQ")
	if err := json.Unmarshal([]byte(`{"initRange":{}}`), &video.Formats[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := selectFormat(video, "best"); !errors.Is(err, errCombined) {
		t.Fatal("segmented/adaptive format was accepted as progressive")
	}
	video = fixtureVideo("dQw4w9WgXcQ")
	video.HLSManifestURL = "https://public.invalid/manifest"
	if _, err := selectFormat(video, "best"); err != nil {
		t.Fatal("a direct combined stream was rejected because other manifest formats exist")
	}
	video.Formats = nil
	if _, err := selectFormat(video, "best"); !errors.Is(err, errManifest) {
		t.Fatal("HLS-only source was accepted")
	}
	video.HLSManifestURL, video.DASHManifestURL = "", "https://public.invalid/manifest"
	if _, err := selectFormat(video, "best"); !errors.Is(err, errManifest) {
		t.Fatal("DASH manifest source was accepted")
	}
}

type failingReader struct{}

func (failingReader) Read(p []byte) (int, error) {
	return copy(p, "broken"), errors.New("secret signed URL https://private.invalid/?signature=secret")
}

type blockedStream struct {
	closed  chan struct{}
	entered chan struct{}
	once    sync.Once
	ready   sync.Once
	prefix  []byte
}

func (b *blockedStream) Read(p []byte) (int, error) {
	if len(b.prefix) > 0 {
		n := copy(p, b.prefix)
		b.prefix = b.prefix[n:]
		return n, nil
	}
	b.ready.Do(func() { close(b.entered) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockedStream) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestNativeFailuresAndLengths(t *testing.T) {
	for _, scenario := range []string{"complete", "metadata", "no-combined", "manifest", "read", "short", "long", "disagreement", "unknown", "panic", "enumeration"} {
		t.Run(scenario, func(t *testing.T) {
			fake := fixtureClient(2)
			fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
				v := fixtureVideo(id)
				if scenario == "unknown" {
					v.Formats[0].ContentLength = 0
				}
				if id == "00000000002" {
					switch scenario {
					case "metadata":
						return nil, errors.New("/secret/path signed URL")
					case "no-combined":
						v.Formats[0].AudioChannels = 0
					case "manifest":
						v.DASHManifestURL = "https://public.invalid/manifest"
						v.Formats = nil
					case "panic":
						panic("malformed metadata /secret/path")
					}
				}
				return v, nil
			}
			fake.streamFn = func(_ context.Context, v *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
				data, reported := fixtureData, int64(len(fixtureData))
				if scenario == "unknown" {
					reported = 0
				}
				if v.ID == "00000000002" {
					switch scenario {
					case "read":
						return io.NopCloser(failingReader{}), reported, nil
					case "short":
						data = "short"
					case "long":
						data += "extra"
					case "disagreement":
						reported++
					}
				}
				return io.NopCloser(strings.NewReader(data)), reported, nil
			}
			if scenario == "enumeration" {
				fake.listErr = errors.New("continuation failed /secret/path")
			}
			s := testServer(t, fake, nil)
			j := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
			want := 1
			if scenario == "complete" || scenario == "unknown" || scenario == "enumeration" {
				want = 2
			}
			status := "partial"
			if scenario == "complete" || scenario == "unknown" {
				status = "completed"
			}
			if j.Status != status || j.CompletedCount != want || j.TotalCount == nil || *j.TotalCount != 2 {
				t.Fatalf("unexpected result: %+v", j)
			}
			if want == 1 && (len(j.Failures) != 1 || j.Failures[0].Index != 2 || j.Failures[0].Error == "") {
				t.Fatalf("missing per-item failure: %+v", j.Failures)
			}
			encoded, _ := json.Marshal(j)
			if strings.Contains(string(encoded), "/secret") || strings.Contains(string(encoded), "signature=secret") {
				t.Fatal("raw engine diagnostic escaped")
			}
			if scenario == "unknown" && !strings.Contains(j.Note, unknownLengthNote) {
				t.Fatal("unknown-length verification limitation omitted")
			}
			assertFinalFiles(t, s, j)
			if scenario == "panic" {
				next := waitTerminal(t, s, createJob(t, s, testVideo).ID)
				if next.Status != "completed" {
					t.Fatal("native panic stopped the worker queue")
				}
			}
		})
	}
}

func TestEntireExposedPlaylistAndInvalidEntries(t *testing.T) {
	fake := fixtureClient(151)
	fake.playlist.Videos[1].ID = fake.playlist.Videos[0].ID
	s := testServer(t, fake, nil)
	j := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	if j.Status != "completed" || len(j.Files) != 151 || j.TotalCount == nil || *j.TotalCount != 151 || !strings.Contains(j.Note, playlistNote) {
		t.Fatalf("playlist was truncated or disclosure omitted: %+v", j)
	}
	if j.Files[0].Name == j.Files[1].Name || !strings.HasPrefix(j.Files[1].Name, "000002-") || !strings.HasSuffix(j.Files[1].Name, "00000000001.mp4") {
		t.Fatal("duplicate video positions were deduplicated or misnamed")
	}
	assertFinalFiles(t, s, j)
	bad := fixtureClient(1)
	bad.playlist.Videos = append([]*youtube.PlaylistEntry{nil, {ID: "../invalid"}}, bad.playlist.Videos...)
	s2 := testServer(t, bad, nil)
	partial := waitTerminal(t, s2, createJob(t, s2, testPlaylist).ID)
	if partial.Status != "partial" || partial.TotalCount == nil || *partial.TotalCount != 3 || partial.CompletedCount != 1 || len(partial.Failures) != 2 || !strings.HasPrefix(partial.Files[0].Name, "000003-") {
		t.Fatalf("invalid entries disappeared: %+v", partial)
	}
	empty := testServer(t, fixtureClient(0), nil)
	if j := waitTerminal(t, empty, createJob(t, empty, testPlaylist).ID); j.Status != "failed" || j.CompletedCount != 0 {
		t.Fatal("empty playlist claimed completed media")
	}
}

func TestHardStorageLimitAndEOF(t *testing.T) {
	s := testServer(t, fixtureClient(2), func(c *config) { c.maxBytes = int64(len(fixtureData)) })
	j := waitTerminal(t, s, createJob(t, s, testPlaylist).ID)
	if j.Status != "partial" || j.CompletedCount != 1 || !strings.Contains(j.Error, "storage limit") {
		t.Fatalf("aggregate budget not enforced: %+v", j)
	}
	assertFinalFiles(t, s, j)
	for _, data := range []string{"1234", "12345", ""} {
		var disk bytes.Buffer
		written, err := copyStream(context.Background(), &disk, strings.NewReader(data), 4, 0, func(int64) {})
		if disk.Len() > 4 || written > 4 {
			t.Fatal("copy wrote bytes beyond its hard budget")
		}
		if (len(data) == 4) != (err == nil) {
			t.Fatalf("EOF/limit handling: %q %v", data, err)
		}
	}
	fake := fixtureClient(1)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		v := fixtureVideo(id)
		v.Formats[0].ContentLength = 0
		return v, nil
	}
	fake.streamFn = func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader(fixtureData)), 0, nil
	}
	s2 := testServer(t, fake, func(c *config) { c.maxBytes = 4 })
	failed := waitTerminal(t, s2, createJob(t, s2, testVideo).ID)
	if failed.Status != "failed" || failed.CompletedCount != 0 {
		t.Fatalf("unknown-length overflow finalized: %+v", failed)
	}
	assertFinalFiles(t, s2, failed)
}

func TestCancellationQueueAndTimeout(t *testing.T) {
	stream := &blockedStream{closed: make(chan struct{}), entered: make(chan struct{}), prefix: []byte("partial")}
	fake := fixtureClient(2)
	fake.streamFn = func(_ context.Context, v *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
		if v.ID == "00000000002" {
			return stream, int64(len(fixtureData)), nil
		}
		return io.NopCloser(strings.NewReader(fixtureData)), int64(len(fixtureData)), nil
	}
	s := testServer(t, fake, func(c *config) { c.maxJobs = 2 })
	first := createJob(t, s, testPlaylist)
	select {
	case <-stream.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture stream did not enter blocked read")
	}
	progress := waitJob(t, s, first.ID, func(j Job) bool { return j.CompletedCount == 1 })
	if progress.Progress == nil || *progress.Progress >= 100 || *progress.Progress <= 0 {
		t.Fatal("current-file progress was not reset for the next entry")
	}
	second := createJob(t, s, testPlaylist)
	if second.Status != "queued" || request(s, "POST", "/api/jobs/"+first.ID+"/ticket", `{}`, nil).Code != 409 {
		t.Fatal("queue or terminal-file guard failed")
	}
	body := `{"url":"` + testVideo + `","quality":"best","rightsConfirmed":true}`
	if request(s, "POST", "/api/jobs", body, nil).Code != 429 {
		t.Fatal("job bound not enforced")
	}
	var listed struct{ Jobs []Job }
	_ = json.Unmarshal(request(s, "GET", "/api/jobs", "", nil).Body.Bytes(), &listed)
	if len(listed.Jobs) != 2 || listed.Jobs[0].ID != second.ID {
		t.Fatal("job ordering changed")
	}
	request(s, "POST", "/api/jobs/"+second.ID+"/cancel", "", nil)
	if waitTerminal(t, s, second.ID).Status != "cancelled" {
		t.Fatal("queued cancellation failed")
	}
	request(s, "POST", "/api/jobs/"+first.ID+"/cancel", "", nil)
	j := waitTerminal(t, s, first.ID)
	if j.Status != "cancelled" || j.CompletedCount != 1 {
		t.Fatalf("stream cancellation lost finalized output: %+v", j)
	}
	select {
	case <-stream.closed:
	default:
		t.Fatal("context cancellation did not close the native stream")
	}
	assertFinalFiles(t, s, j)
	ticketPath(t, s, first.ID, `{}`)
	stalled := fixtureClient(1)
	stalled.videoFn = func(ctx context.Context, _ string) (*youtube.Video, error) { <-ctx.Done(); return nil, ctx.Err() }
	short := testServer(t, stalled, func(c *config) { c.timeout = 30 * time.Millisecond })
	timed := waitTerminal(t, short, createJob(t, short, testVideo).ID)
	if timed.Status != "failed" || !strings.Contains(timed.Error, "timeout") {
		t.Fatal("native metadata timeout was not honored")
	}
}

func TestSymlinksAndBindConfiguration(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	target := filepath.Join(s.cfg.root, "target")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	name := "000001-dQw4w9WgXcQ.mp4"
	if err := os.Symlink(target, filepath.Join(s.cfg.root, name)); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("skipping symlink check on Windows: %v", err)
		} else {
			t.Fatal(err)
		}
	} else {
		if f, err := openFinal(s.cfg.root, name); err == nil {
			_ = f.Close()
			t.Fatal("symlink opened as media")
		}
	}
	for _, key := range []string{"ADDR", "API_TOKEN", "ALLOWED_HOSTS", "ALLOWED_ORIGINS", "MAX_JOBS", "MAX_JOB_BYTES", "JOB_TIMEOUT", "RETENTION"} {
		t.Setenv(key, "")
	}
	c, err := loadConfig()
	if err != nil || !c.hosts["127.0.0.1:8080"] {
		t.Fatal("loopback defaults changed")
	}
	t.Setenv("ADDR", "0.0.0.0:8080")
	if _, err := loadConfig(); err == nil {
		t.Fatal("public bind accepted without authentication")
	}
	t.Setenv("API_TOKEN", strings.Repeat("a", 32))
	if _, err := loadConfig(); err == nil {
		t.Fatal("public bind accepted without explicit hosts")
	}
	t.Setenv("ALLOWED_HOSTS", "api.example.invalid")
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALLOWED_ORIGINS", "*")
	if _, err := loadConfig(); err == nil {
		t.Fatal("wildcard browser origin accepted")
	}
}
