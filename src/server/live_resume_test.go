package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise an actual browser transfer, pause, database close/reopen and resume.
// This is opt-in because it downloads a complete video from YouTube.
func TestLiveSABRRestartResume(t *testing.T) {
	raw := os.Getenv("YTDL_LIVE_RESUME_URL")
	if raw == "" {
		t.Skip("set YTDL_LIVE_RESUME_URL to exercise real SABR restart recovery")
	}
	c := config{addr: "127.0.0.1:8080", root: t.TempDir(), browserPath: os.Getenv("CHROME_PATH"),
		origins: map[string]bool{"http://127.0.0.1:8080": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
		maxJobs: 1, maxBytes: 10 << 30, timeout: 20 * time.Minute, retain: 10 * time.Minute}
	prepareTestDataDir(t, c.root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.stop() })
	if response := request(s, "PUT", "/api/settings", `{"storageMode":"managed-only"}`, nil); response.Code != 200 {
		t.Fatalf("settings: %s", response.Body.String())
	}
	video, err := s.engine.GetVideoContext(s.ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	s.start()
	body, _ := json.Marshal(map[string]any{"url": raw, "quality": "1080", "videoStrategy": "compatibility", "rightsConfirmed": true})
	created := request(s, "POST", "/api/jobs", string(body), nil)
	var job Job
	if created.Code != 202 || json.Unmarshal(created.Body.Bytes(), &job) != nil {
		t.Fatalf("create job: %s", created.Body.String())
	}
	deadline := time.Now().Add(3 * time.Minute)
	ready := false
	for time.Now().Before(deadline) {
		// Require durable media on BOTH tracks, rather than only initialization.
		v, ve := s.store.loadSABRCheckpoint(job.ID, 1, "video")
		a, ae := s.store.loadSABRCheckpoint(job.ID, 1, "audio")
		if ve == nil && ae == nil && v != nil && a != nil && v.Cumulative >= 30000 && a.Cumulative >= 30000 && !v.Complete && !a.Complete {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("both tracks did not produce resumable checkpoints")
	}
	if response := request(s, "POST", "/api/jobs/"+job.ID+"/pause", "{}", nil); response.Code != 200 {
		t.Fatalf("pause: %s", response.Body.String())
	}
	waitJobFor(t, s, job.ID, time.Minute, func(j Job) bool { return j.Status == "paused" })
	before := map[string]int64{}
	for _, key := range []string{"video", "audio"} {
		state, err := s.store.loadSABRCheckpoint(job.ID, 1, key)
		if err != nil || state == nil || state.Bytes <= 0 {
			t.Fatalf("paused %s checkpoint: %v", key, err)
		}
		before[key] = state.Bytes
		t.Logf("paused %s bytes=%d verified_duration_ms=%d", key, state.Bytes, state.Cumulative)
	}
	stopWatchdog := livePhaseWatchdog("shutdown before restart")
	s.stop()
	stopWatchdog()
	reopened, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	s = reopened
	for key, want := range before {
		state, err := s.store.loadSABRCheckpoint(job.ID, 1, key)
		if err != nil || state == nil || state.Bytes != want {
			t.Fatalf("reopened %s checkpoint missing: %v", key, err)
		}
	}
	s.start()
	if response := request(s, "POST", "/api/jobs/"+job.ID+"/resume", "{}", nil); response.Code != 200 {
		t.Fatalf("resume: %s", response.Body.String())
	}
	completed := waitJobFor(t, s, job.ID, 15*time.Minute, func(j Job) bool { return terminal(j.Status) })
	if completed.Status != "completed" || len(completed.Files) != 1 {
		t.Fatalf("resume status=%s error=%s failures=%v", completed.Status, completed.Error, completed.Failures)
	}
	file := completed.Files[0]
	assertFinalFiles(t, s, completed)
	assertLiveContainer(t, filepath.Join(c.root, job.ID, file.Name), file.MimeType, file.Height, video.Duration)
	for key := range before {
		if _, err := s.store.loadPart(job.ID, 1, key); err == nil {
			t.Fatalf("completed transfer retained %s checkpoint", key)
		}
	}
	t.Logf("restarted transfer verified height=%d bytes=%d", file.Height, file.Size)
}
