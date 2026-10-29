package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yapingcat/gomedia/go-mp4"
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
	if strings.Contains(raw, "JapSnYBq3U8") {
		minimumHeight = 2160
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
		maxJobs: 1, maxBytes: 10 * 1024 * 1024 * 1024, timeout: 30 * time.Minute, retain: 10 * time.Minute,
	}
	prepareTestDataDir(t, c.root)
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
			t.Logf("selected adaptive=%t video itag=%d height=%d length=%d", selection.audio != nil, selection.video.ItagNo, selection.video.Height, selection.video.ContentLength)
			if selection.audio != nil {
				t.Logf("audio itag=%d length=%d", selection.audio.ItagNo, selection.audio.ContentLength)
			}
		}
	}

	job := createJob(t, s, raw)
	deadline := time.Now().Add(35 * time.Minute)
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
			if strings.Contains(raw, "JapSnYBq3U8") && strings.Contains(current.Note, adaptiveFallbackNote) {
				t.Fatalf("4K sample used adaptive fallback: height=%d note=%q", current.Files[0].Height, current.Note)
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

// TestLiveResolutionMatrix downloads complete media through the real job API.
// Set YTDL_LIVE_MATRIX_URLS to comma-separated authorized YouTube URLs.
// Every supported quality ceiling is exercised, even when ceilings select the
// same source resolution. A progressive fallback fails the resolution check.
func TestLiveResolutionMatrix(t *testing.T) {
	raw := os.Getenv("YTDL_LIVE_MATRIX_URLS")
	if raw == "" {
		t.Skip("set YTDL_LIVE_MATRIX_URLS to comma-separated video URLs")
	}
	urls := strings.Split(raw, ",")
	for index, rawURL := range urls {
		canonical, kind, err := canonicalURL(strings.TrimSpace(rawURL))
		if err != nil || kind != "video" {
			t.Fatalf("sample %d must be a valid YouTube video URL", index+1)
		}
		urls[index] = canonical
	}
	for index, rawURL := range urls {
		t.Run(fmt.Sprintf("sample_%d", index+1), func(t *testing.T) {
			t.Parallel()
			c := config{
				addr: "127.0.0.1:8080", root: t.TempDir(), browserPath: os.Getenv("CHROME_PATH"),
				origins: map[string]bool{"http://127.0.0.1:8080": true}, hosts: map[string]bool{"127.0.0.1:8080": true},
				maxJobs: 1, maxBytes: 10 * 1024 * 1024 * 1024, timeout: 30 * time.Minute, retain: 10 * time.Minute,
			}
			prepareTestDataDir(t, c.root)
			s, err := newServer(c)
			if err != nil {
				t.Fatal(err)
			}
			s.start()
			t.Cleanup(func() { s.stop(); s.wg.Wait() })
			settings := request(s, "PUT", "/api/settings", `{"storageMode":"managed-only"}`, nil)
			if settings.Code != 200 {
				t.Fatalf("configure isolated output: %d %s", settings.Code, settings.Body.String())
			}
			inspectCtx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
			defer cancel()
			video, err := s.engine.GetVideoContext(inspectCtx, strings.TrimSpace(rawURL))
			if err != nil {
				t.Fatalf("inspect sample: %v", err)
			}
			t.Logf("sample url=%s title=%q duration=%s", strings.TrimSpace(rawURL), video.Title, video.Duration)
			for _, format := range video.Formats {
				if format.Height >= 1440 {
					t.Logf("available itag=%d height=%d fps=%d bytes=%d mime=%q", format.ItagNo, format.Height, format.FPS, format.ContentLength, format.MimeType)
				}
			}
			for _, quality := range []string{"480", "720", "1080", "1440", "2160", "best"} {
				t.Run(quality, func(t *testing.T) {
					selection, err := selectFormat(video, quality)
					if err != nil {
						t.Fatalf("select %s: %v", quality, err)
					}
					wantHeight := selection.video.Height
					t.Logf("selected video itag=%d bytes=%d reservation=%d", selection.video.ItagNo, selection.video.ContentLength, estimatedItemBudget(&jobState{Job: Job{MediaType: "video"}}, video, selection.video, selection))
					if selection.audio != nil {
						t.Logf("selected audio itag=%d bytes=%d", selection.audio.ItagNo, selection.audio.ContentLength)
					}
					body, err := json.Marshal(map[string]any{"url": strings.TrimSpace(rawURL), "quality": quality, "rightsConfirmed": true})
					if err != nil {
						t.Fatal(err)
					}
					response := request(s, "POST", "/api/jobs", string(body), nil)
					if response.Code != 202 {
						t.Fatalf("create: %d %s", response.Code, response.Body.String())
					}
					var job Job
					if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						response := request(s, "GET", "/api/jobs/"+job.ID, "", nil)
						var remaining Job
						if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &remaining) == nil && !terminal(remaining.Status) {
							request(s, "POST", "/api/jobs/"+job.ID+"/cancel", "{}", nil)
							waitJobFor(t, s, job.ID, 30*time.Second, func(j Job) bool { return terminal(j.Status) })
						}
					})
					lastLog := time.Now()
					current := waitJobFor(t, s, job.ID, 35*time.Minute, func(j Job) bool {
						if time.Since(lastLog) >= 30*time.Second {
							progress := 0.0
							if j.Progress != nil {
								progress = *j.Progress
							}
							t.Logf("quality=%s status=%s progress=%.1f note=%q", quality, j.Status, progress, j.Note)
							lastLog = time.Now()
						}
						return terminal(j.Status)
					})
					if current.Status != "completed" || len(current.Files) != 1 {
						t.Fatalf("download status=%s error=%q failures=%v", current.Status, current.Error, current.Failures)
					}
					file := current.Files[0]
					t.Logf("requested=%s expected=%dp actual=%dp bytes=%d mime=%s note=%q", quality, wantHeight, file.Height, file.Size, file.MimeType, current.Note)
					assertFinalFiles(t, s, current)
					assertLiveContainer(t, filepath.Join(s.cfg.root, job.ID, file.Name), file.MimeType, file.Height, video.Duration)
					if file.Height != wantHeight {
						t.Errorf("download height=%d, selected height=%d (note=%q)", file.Height, wantHeight, current.Note)
					}
					// Release this sample's media before downloading the next ceiling.
					deleted := request(s, "DELETE", "/api/jobs/"+job.ID, "", nil)
					if deleted.Code != 200 && deleted.Code != 204 {
						t.Errorf("cleanup: %d %s", deleted.Code, deleted.Body.String())
					}
				})
			}
		})
	}
}

// Read the actual container rather than trusting the persisted file metadata.
func assertLiveContainer(t *testing.T, path, mimeType string, height int, duration time.Duration) {
	t.Helper()
	if mimeType == "video/webm" {
		video, err := readWebMTrackEntry(path, 1)
		if err != nil {
			t.Fatal(err)
		}
		if int(video.Video.PixelHeight) != height {
			t.Fatalf("WebM height=%d, metadata height=%d", video.Video.PixelHeight, height)
		}
		if _, err := readWebMTrackEntry(path, 2); err != nil {
			t.Fatalf("WebM audio: %v", err)
		}
		for _, trackType := range []uint64{1, 2} {
			source, err := openWebMTrackSource(path, trackType)
			if err != nil {
				t.Fatal(err)
			}
			var last int64
			for {
				frame, readErr := nextWebMFrame(context.Background(), source)
				if readErr == io.EOF || frame.eof {
					break
				}
				if readErr != nil {
					source.close()
					t.Fatal(readErr)
				}
				last = frame.timestamp
			}
			source.close()
			if time.Duration(last)*time.Millisecond < duration-3*time.Second {
				t.Errorf("WebM track %d ends at %dms, want %s", trackType, last, duration)
			}
		}
		return
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	demuxer := mp4.CreateMp4Demuxer(file)
	tracks, err := demuxer.ReadHead()
	if err != nil {
		t.Fatal(err)
	}
	videoID, audioID := -1, -1
	for _, track := range tracks {
		if track.Height > 0 {
			videoID = track.TrackId
			if int(track.Height) != height {
				t.Errorf("MP4 height=%d, metadata height=%d", track.Height, height)
			}
		} else if track.ChannelCount > 0 {
			audioID = track.TrackId
		}
	}
	if videoID < 0 || audioID < 0 {
		t.Fatalf("MP4 requires video and audio tracks: %+v", tracks)
	}
	last := map[int]uint64{}
	for {
		packet, readErr := demuxer.ReadPacket()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if packet != nil && len(packet.Data) > 0 && packet.Pts > last[packet.TrackId] {
			last[packet.TrackId] = packet.Pts
		}
	}
	for _, id := range []int{videoID, audioID} {
		if time.Duration(last[id])*time.Millisecond < duration-3*time.Second {
			t.Errorf("MP4 track %d ends at %dms, want %s", id, last[id], duration)
		}
	}
}
