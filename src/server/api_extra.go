// api_extra.go implements metadata inspection, retry-as-new-job, and safe
// metadata projection for the downloader's JSON API.
package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kkdai/youtube/v2"
)

type inspectedQuality struct {
	Value  string `json:"value"`
	Label  string `json:"label"`
	Height int    `json:"height"`
}

type inspectedItem struct {
	Index           int    `json:"index,omitempty"`
	ID              string `json:"id"`
	Title           string `json:"title"`
	Author          string `json:"author,omitempty"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
	ThumbnailURL    string `json:"thumbnailUrl,omitempty"`
}

type inspection struct {
	URL                string             `json:"url"`
	Kind               string             `json:"kind"`
	Title              string             `json:"title"`
	Author             string             `json:"author,omitempty"`
	DurationSeconds    int64              `json:"durationSeconds,omitempty"`
	ThumbnailURL       string             `json:"thumbnailUrl,omitempty"`
	PublishDate        string             `json:"publishDate,omitempty"`
	AvailableQuality   []inspectedQuality `json:"availableQualities,omitempty"`
	AudioOnlyAvailable bool               `json:"audioOnlyAvailable"`
	ItemCount          int                `json:"itemCount,omitempty"`
	Items              []inspectedItem    `json:"items,omitempty"`
	Entries            []inspectedItem    `json:"entries,omitempty"`
	Note               string             `json:"note,omitempty"`
}

// inspect validates a link and returns video quality options or playlist
// metadata with at most ten preview entries; it never queues a download.
func (s *server) inspect(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &request) {
		return
	}
	canonical, kind, err := canonicalURL(request.URL)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if s.engine == nil {
		fail(w, 503, "Native download engine is not initialized")
		return
	}
	timeout := min(s.cfg.timeout, 25*time.Second)
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	engine := s.operationEngine()

	if kind == "video" {
		video, err := engine.GetVideoContext(ctx, canonical)
		if err != nil || video == nil {
			fail(w, 422, "Video metadata is unavailable or the video cannot be inspected")
			return
		}
		parsed, _ := url.Parse(canonical)
		if video.ID != parsed.Query().Get("v") {
			fail(w, 422, "The inspected video did not match the requested URL")
			return
		}
		result := inspection{
			URL: canonical, Kind: kind, Title: video.Title, Author: video.Author,
			DurationSeconds: int64(video.Duration.Seconds()), ThumbnailURL: safeThumbnailURL(video.Thumbnails),
		}
		_, _, audioErr := selectAudioFormat(video)
		result.AudioOnlyAvailable = audioErr == nil
		if !video.PublishDate.IsZero() {
			result.PublishDate = video.PublishDate.UTC().Format("2006-01-02")
		}
		for _, option := range []struct {
			value, label string
			height       int
		}{{"best", "Best available", 0}, {"1080", "Up to 1080p", 1080}, {"720", "Up to 720p", 720}, {"480", "Up to 480p", 480}} {
			if _, err := selectFormat(video, option.value); err == nil {
				result.AvailableQuality = append(result.AvailableQuality, inspectedQuality{Value: option.value, Label: option.label, Height: option.height})
			}
		}
		if len(result.AvailableQuality) == 0 && !result.AudioOnlyAvailable {
			fail(w, 422, "No supported MP4 or WebM video stream is available")
			return
		}
		reply(w, 200, result)
		return
	}

	playlist, err := engine.GetPlaylistContext(ctx, canonical)
	if err != nil || playlist == nil {
		fail(w, 422, "Playlist metadata is unavailable or the playlist cannot be inspected")
		return
	}
	result := inspection{
		URL: canonical, Kind: kind, Title: playlist.Title, Author: playlist.Author,
		ItemCount: len(playlist.Videos), Items: []inspectedItem{}, Entries: []inspectedItem{}, AudioOnlyAvailable: len(playlist.Videos) > 0,
		Note: "The downloader processes every playlist entry exposed by YouTube. Hidden or inaccessible entries cannot be counted.",
	}
	for index, entry := range playlist.Videos {
		item := inspectedItem{Index: index + 1, Title: "Unavailable playlist item"}
		if entry != nil {
			item.ID = entry.ID
			if entry.Title != "" {
				item.Title = entry.Title
			} else {
				item.Title = "Item " + strconv.Itoa(index+1)
			}
			item.Author = entry.Author
			item.DurationSeconds = int64(entry.Duration.Seconds())
			item.ThumbnailURL = safeThumbnailURL(entry.Thumbnails)
		}
		result.Entries = append(result.Entries, item)
		if index < 10 && entry != nil && videoID.MatchString(entry.ID) {
			result.Items = append(result.Items, item)
		}
	}
	reply(w, 200, result)
}

// retry requires fresh rights confirmation and creates a new job from a failed,
// partial, or cancelled job; the original history is left unchanged.
func (s *server) retry(w http.ResponseWriter, r *http.Request, id string) {
	var request struct {
		RightsConfirmed bool `json:"rightsConfirmed"`
	}
	if !decode(w, r, &request) {
		return
	}
	if !request.RightsConfirmed {
		fail(w, 400, "Confirm that you own the content or have permission to download it")
		return
	}
	s.mu.Lock()
	original := s.jobs[id]
	if original == nil {
		s.mu.Unlock()
		fail(w, 404, "Job not found")
		return
	}
	if !terminal(original.Status) || (original.Status != "failed" && original.Status != "partial" && original.Status != "cancelled") {
		s.mu.Unlock()
		fail(w, 409, "Only failed, partial, or cancelled jobs can be retried")
		return
	}
	jobURL, quality, mediaType, audioBitrate, category, storageMode := original.URL, original.Quality, original.MediaType, original.AudioBitrate, original.Category, original.StorageMode
	s.mu.Unlock()
	canonical, kind, err := canonicalURL(jobURL)
	if err != nil {
		fail(w, 409, "The saved job URL is no longer valid")
		return
	}
	s.enqueueJob(w, canonical, kind, quality, mediaType, audioBitrate, category, storageMode)
}

// safeThumbnailURL returns the last HTTPS thumbnail hosted on an approved
// YouTube image domain, or an empty string when none qualifies.
func safeThumbnailURL(thumbnails youtube.Thumbnails) string {
	for index := len(thumbnails) - 1; index >= 0; index-- {
		parsed, err := url.Parse(thumbnails[index].URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "ytimg.com" || strings.HasSuffix(host, ".ytimg.com") || host == "ggpht.com" || strings.HasSuffix(host, ".ggpht.com") {
			return parsed.String()
		}
	}
	return ""
}

func safeInspectedThumbnailURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "ytimg.com" || strings.HasSuffix(host, ".ytimg.com") || host == "ggpht.com" || strings.HasSuffix(host, ".ggpht.com") {
		return parsed.String()
	}
	return ""
}

// applyVideoMetadata copies display metadata to a finalized file record; it
// does not retain signed stream URLs or browser-session credentials.
func applyVideoMetadata(file *mediaFile, video *youtube.Video) {
	file.Title = video.Title
	file.Author = video.Author
	file.DurationSeconds = int64(video.Duration.Seconds())
	file.ThumbnailURL = safeThumbnailURL(video.Thumbnails)
	if !video.PublishDate.IsZero() {
		file.PublishDate = video.PublishDate.UTC().Format("2006-01-02")
	}
}
