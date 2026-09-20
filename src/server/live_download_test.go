package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Run with YTDL_LIVE_DOWNLOAD_URL set to an accessible, authorized video URL:
// go test -run TestLiveDownload -count=1
// Optionally set YTDL_LIVE_MIN_HEIGHT to require a minimum output resolution.
// The test is opt-in because it depends on YouTube availability and downloads real media.
func TestLiveDownload(t *testing.T) {
	raw := os.Getenv("YTDL_LIVE_DOWNLOAD_URL")
	if raw == "" {
		t.Skip("set YTDL_LIVE_DOWNLOAD_URL to run the live download validation")
	}
	minimumHeight := 1
	if strings.Contains(raw, "y0KRrtfy2pY") {
		minimumHeight = 360
	}
	if value := os.Getenv("YTDL_LIVE_MIN_HEIGHT"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 {
			t.Fatalf("invalid YTDL_LIVE_MIN_HEIGHT %q", value)
		}
		minimumHeight = parsed
	}

	c := config{
		addr: "127.0.0.1:8080", root: t.TempDir(), token: "",
		browserPath: os.Getenv("CHROME_PATH"),
		origins:     map[string]bool{"http://127.0.0.1:8080": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 1, maxBytes: 10 * 1024 * 1024 * 1024, timeout: 15 * time.Minute, retain: 10 * time.Minute,
	}
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	s.start()
	t.Cleanup(func() {
		s.stop()
		s.wg.Wait()
	})
	if liveVideo, err := s.engine.GetVideoContext(s.ctx, raw); err == nil {
		if selection, selectErr := selectFormat(liveVideo, "best"); selectErr == nil {
			t.Logf("selected adaptive=%t video itag=%d height=%d length=%d audio itag=%d length=%d", selection.audio != nil, selection.video.ItagNo, selection.video.Height, selection.video.ContentLength, selection.audio.ItagNo, selection.audio.ContentLength)
		}
	}

	job := createJob(t, s, raw)
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		w := request(s, "GET", "/api/jobs/"+job.ID, "", nil)
		var current Job
		if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if terminal(current.Status) {
			if current.Status != "completed" {
				t.Fatalf("live download ended as %s: error=%q failures=%v", current.Status, current.Error, current.Failures)
			}
			if len(current.Files) == 0 {
				t.Fatal("live download completed without a finalized file")
			}
			t.Logf("completed height=%d size=%d note=%q", current.Files[0].Height, current.Files[0].Size, current.Note)
			if current.Files[0].Height < minimumHeight {
				t.Fatalf("live download selected %dp, want at least %dp", current.Files[0].Height, minimumHeight)
			}
			if strings.Contains(raw, "y0KRrtfy2pY") && current.Files[0].Height < 1080 && !strings.Contains(current.Note, adaptiveFallbackNote) {
				t.Fatalf("sample completed below 1080p without an adaptive fallback note: height=%d note=%q", current.Files[0].Height, current.Note)
			}
			assertFinalFiles(t, s, current)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("live download did not reach a terminal state before the test deadline")
}
