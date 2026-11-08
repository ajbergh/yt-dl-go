package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Opt-in real audio job; all output stays in test-owned temporary directories.
// YTDL_LIVE_AUDIO_URL must identify a single video. Audio format defaults to MP3.
func TestLiveAudioDownload(t *testing.T) {
	raw := os.Getenv("YTDL_LIVE_AUDIO_URL")
	if raw == "" {
		t.Skip("set YTDL_LIVE_AUDIO_URL to run live audio validation")
	}
	canonical, kind, err := canonicalURL(raw)
	if err != nil || kind != "video" {
		t.Fatal("YTDL_LIVE_AUDIO_URL must identify a single YouTube video")
	}
	audioFormat := os.Getenv("YTDL_LIVE_AUDIO_FORMAT")
	if audioFormat == "" {
		audioFormat = "mp3"
	}
	if audioFormat != "mp3" && audioFormat != "m4a" {
		t.Fatal("audio format must be mp3 or m4a")
	}
	c := config{
		addr: "127.0.0.1:8080", root: t.TempDir(), browserPath: os.Getenv("CHROME_PATH"),
		origins: map[string]bool{"http://127.0.0.1:8080": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 1, maxBytes: 1024 * 1024 * 1024, timeout: 10 * time.Minute, retain: 10 * time.Minute,
	}
	prepareTestDataDir(t, c.root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	// The worker snapshots these settings into its job before publishing.
	s.settings.DownloadLocation = t.TempDir()
	s.start()
	t.Cleanup(func() { s.stop(); s.wg.Wait() })
	body, _ := json.Marshal(map[string]any{"url": canonical, "quality": "best", "mediaType": "audio", "audioFormat": audioFormat, "rightsConfirmed": true})
	response := request(s, "POST", "/api/jobs", string(body), nil)
	var job Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("create: %s error=%v", response.Body.String(), err)
	}
	started, lastLog := time.Now(), time.Time{}
	deadline := started.Add(11 * time.Minute)
	for time.Now().Before(deadline) {
		response = request(s, "GET", "/api/jobs/"+job.ID, "", nil)
		if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if time.Since(lastLog) >= 15*time.Second {
			t.Logf("status=%s elapsed=%s", job.Status, time.Since(started).Round(time.Second))
			lastLog = time.Now()
		}
		if terminal(job.Status) {
			if job.Status != "completed" || len(job.Files) != 1 {
				t.Fatalf("status=%s error=%q failures=%v", job.Status, job.Error, job.Failures)
			}
			file := job.Files[0]
			wantMime := "audio/mpeg"
			if audioFormat == "m4a" {
				wantMime = "audio/mp4"
			}
			if file.MediaType != "audio" || file.MimeType != wantMime || file.Height != 0 || file.Size <= 0 {
				t.Fatalf("invalid audio result: %+v", file)
			}
			path := trackedOutputPath(t, s, job.ID, file.ID)
			info, err := os.Stat(path)
			if err != nil || info.Size() != file.Size {
				t.Fatalf("published audio error=%v", err)
			}
			t.Logf("completed format=%s bytes=%d elapsed=%s", audioFormat, file.Size, time.Since(started).Round(time.Millisecond))
			if probe, err := exec.LookPath("ffprobe"); err == nil {
				output, err := exec.Command(probe, "-v", "error", "-show_entries", "stream=codec_name,codec_type,sample_rate,channels:format=duration,size", "-of", "json", path).CombinedOutput()
				if err != nil {
					t.Fatalf("probe: %v %s", err, output)
				}
				t.Logf("audio probe: %s", output)
			}
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("live audio job did not finish before deadline")
}
