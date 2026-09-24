// worker.go selects supported streams, schedules concurrent jobs, processes
// each playlist in order, records progress, and prunes expired job output.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kkdai/youtube/v2"
	"github.com/yapingcat/gomedia/go-mp4"
)

const formatNote = "Native Go engine: progressive MP4/WebM, adaptive H.264/AAC MP4, adaptive VP9/AV1 + Opus WebM, and MP3 audio-only downloads. Quality is a maximum, not a guarantee; HLS/DASH manifest and live sources are unsupported."
const playlistNote = "Playlist totals cover all entries exposed by YouTube, not independently verified hidden entries."
const playlistSelectionNote = "Only the selected exposed playlist items are queued; original playlist positions are preserved."
const unknownLengthNote = "An unknown-length stream is complete only at clean EOF; its original size cannot be independently verified."
const adaptiveFallbackNote = "The requested adaptive stream could not be completed safely; the optional verified 360p progressive MP4 fallback was downloaded instead."
const adaptiveMP4AudioFallbackNote = "The adaptive AAC audio track could not be captured safely; the progressive MP4 AAC track was used instead."
const extractionFallbackNote = "The default YouTube extraction profile was unavailable; a fallback profile was used."
const browserAdaptiveNote = "Adaptive media was streamed through a temporary browser session."
const vp9PreferenceFallbackNote = "VP9 was preferred but unavailable under the selected quality ceiling; the best supported video format was used instead."
const av1PreferenceFallbackNote = "AV1 was preferred but unavailable under the selected quality ceiling; the best supported video format was used instead."

var (
	errMetadata          = errors.New("video metadata is unavailable or invalid")
	errPlaylist          = errors.New("playlist enumeration failed; completeness could not be verified")
	errPlaylistSelection = errors.New("selected playlist items no longer match the inspected playlist")
	errCombined          = errors.New("no compatible combined or adaptive stream fits the requested maximum height")
	errManifest          = errors.New("HLS/DASH manifest or live sources are unsupported")
	errRead              = errors.New("media stream could not be read completely")
	errLength            = errors.New("media stream is empty or does not match its declared size")
	errMux               = errors.New("video and audio streams could not be combined into a complete file")
	errStorage           = errors.New("private media storage could not be safely written or cleaned")
	errLimit             = errors.New("Job storage limit reached; remaining entries were not downloaded")
	errNative            = errors.New("native engine failed unexpectedly; completeness could not be verified")
	errNoAudio           = errors.New("no compatible standalone audio stream is available")
	errAudioConvert      = errors.New("AAC audio could not be decoded and encoded to MP3")
	errItemTimeout       = errors.New("item timed out before its media transfer completed; retry the item")
)

const (
	metadataTimeout     = 2 * time.Minute
	transferIdleTimeout = 60 * time.Second
	minimumItemTimeout  = 15 * time.Minute
	maximumItemTimeout  = 7 * 24 * time.Hour
	defaultTransferRate = 512 * 1024 // bytes per second when no bandwidth limit is configured
	itemTimeoutOverhead = 10 * time.Minute
)

type extractor interface {
	GetVideoContext(context.Context, string) (*youtube.Video, error)
	GetPlaylistContext(context.Context, string) (*youtube.Playlist, error)
	VideoFromPlaylistEntryContext(context.Context, *youtube.PlaylistEntry) (*youtube.Video, error)
	GetStreamContext(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error)
}

type nativeClient = extractor

type streamSelection struct {
	video               *youtube.Format
	audio               *youtube.Format
	progressive         *youtube.Format
	progressiveFallback *youtube.Format
	kind                string
	preferenceFallback  string
}

type itemProgress struct {
	title         string
	downloaded    int64
	total         int64
	speed         int64
	processing    bool
	progressAt    time.Time
	progressBytes int64
	eventAt       time.Time
}

type synchronizedNativeClient struct {
	client nativeClient
	mu     *sync.Mutex
}

func (c synchronizedNativeClient) GetVideoContext(ctx context.Context, raw string) (*youtube.Video, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.GetVideoContext(ctx, raw)
}

func (c synchronizedNativeClient) GetPlaylistContext(ctx context.Context, raw string) (*youtube.Playlist, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.GetPlaylistContext(ctx, raw)
}

func (c synchronizedNativeClient) VideoFromPlaylistEntryContext(ctx context.Context, entry *youtube.PlaylistEntry) (*youtube.Video, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.VideoFromPlaylistEntryContext(ctx, entry)
}

func (c synchronizedNativeClient) GetStreamContext(ctx context.Context, video *youtube.Video, format *youtube.Format) (io.ReadCloser, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client.GetStreamContext(ctx, video, format)
}

func (c synchronizedNativeClient) GetStreamURLContext(ctx context.Context, video *youtube.Video, format *youtube.Format) (string, error) {
	resolver, ok := c.client.(interface {
		GetStreamURLContext(context.Context, *youtube.Video, *youtube.Format) (string, error)
	})
	if !ok {
		return "", errMetadata
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return resolver.GetStreamURLContext(ctx, video, format)
}

// Each production operation receives a fresh YouTube client because the
// upstream client keeps mutable visitor and player caches. Test clients remain
// injectable through server.engine.
func (s *server) operationEngine() nativeClient {
	if _, ok := s.engine.(*youtube.Client); ok {
		return newYouTubeExtractor(0)
	}
	return synchronizedNativeClient{client: s.engine, mu: &s.engineMu}
}

func newJobContext(parent context.Context, overallTimeout time.Duration) (context.Context, context.CancelFunc) {
	if overallTimeout > 0 {
		return context.WithTimeout(parent, overallTimeout)
	}
	return context.WithCancel(parent)
}

func itemDeadlineError(parent, item context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(item.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return errItemTimeout
	}
	return err
}

func formatType(f *youtube.Format) (string, string, bool) {
	kind, params, err := mime.ParseMediaType(f.MimeType)
	if err != nil {
		return "", "", false
	}
	return kind, strings.ToLower(params["codecs"]), true
}

func codecFamily(codecs string) string {
	codecs = strings.ToLower(codecs)
	switch {
	case strings.Contains(codecs, "av01") || strings.Contains(codecs, "av1"):
		return "av1"
	case strings.Contains(codecs, "vp09") || strings.Contains(codecs, "vp9"):
		return "vp9"
	case strings.Contains(codecs, "avc1") || strings.Contains(codecs, "h264"):
		return "h264"
	case strings.Contains(codecs, "opus"):
		return "opus"
	case strings.Contains(codecs, "mp4a"):
		return "aac"
	default:
		return ""
	}
}

// browserWebMVideoCandidates returns only adaptive representations that can
// safely substitute for the selected WebM video when Chrome chooses a
// different itag. Resolution and codec family must remain identical; the
// browser capture later pins the first candidate's SABR format digest.
func browserWebMVideoCandidates(video *youtube.Video, selected *youtube.Format) []*youtube.Format {
	if video == nil || selected == nil || selected.ItagNo <= 0 {
		return nil
	}
	selectedKind, selectedCodecs, ok := formatType(selected)
	if !ok || selectedKind != "video/webm" || selected.Height <= 0 || selected.Width <= 0 {
		return nil
	}
	family := codecFamily(selectedCodecs)
	if family != "vp9" && family != "av1" {
		return nil
	}
	var candidates []*youtube.Format
	seen := make(map[int]struct{})
	for i := range video.Formats {
		candidate := &video.Formats[i]
		kind, codecs, valid := formatType(candidate)
		if !valid || kind != "video/webm" || candidate.ItagNo <= 0 || candidate.AudioChannels != 0 ||
			candidate.Height != selected.Height || candidate.Width != selected.Width ||
			candidate.InitRange == nil || candidate.IndexRange == nil || codecFamily(codecs) != family {
			continue
		}
		if _, exists := seen[candidate.ItagNo]; exists {
			continue
		}
		seen[candidate.ItagNo] = struct{}{}
		candidates = append(candidates, candidate)
	}
	if _, exists := seen[selected.ItagNo]; !exists {
		candidates = append(candidates, selected)
	}
	return candidates
}

func betterVideoFormat(candidate, current *youtube.Format) bool {
	return current == nil || candidate.Height > current.Height ||
		(candidate.Height == current.Height && (candidate.FPS > current.FPS ||
			(candidate.FPS == current.FPS && candidate.Bitrate > current.Bitrate)))
}

func betterWebMVideoFormat(candidate, current *youtube.Format) bool {
	if current == nil {
		return true
	}
	if candidate.Height != current.Height {
		return candidate.Height > current.Height
	}
	if candidate.FPS != current.FPS {
		return candidate.FPS > current.FPS
	}
	candidateCodec := ""
	currentCodec := ""
	if _, codecs, ok := formatType(candidate); ok {
		candidateCodec = codecFamily(codecs)
	}
	if _, codecs, ok := formatType(current); ok {
		currentCodec = codecFamily(codecs)
	}
	// At equal resolution/frame rate prefer VP9 over AV1 for broader native
	// decoder compatibility. AV1 remains fully supported when it is the better
	// or only high-resolution WebM representation.
	if candidateCodec != currentCodec {
		if candidateCodec == "vp9" {
			return true
		}
		if currentCodec == "vp9" {
			return false
		}
	}
	return candidate.Bitrate > current.Bitrate
}

func betterAudioFormat(candidate, current *youtube.Format) bool {
	return current == nil || candidate.Bitrate > current.Bitrate ||
		(candidate.Bitrate == current.Bitrate && candidate.ContentLength > current.ContentLength)
}

// selectFormat preserves the automatic P2.4 policy for callers that do not
// need an explicit codec/container strategy.
func selectFormat(video *youtube.Video, quality string) (streamSelection, error) {
	return selectFormatForStrategy(video, quality, "best")
}

func validVideoStrategy(strategy string) bool {
	return strategy == "best" || strategy == "compatibility" || strategy == "vp9" || strategy == "av1"
}

// selectFormatForStrategy chooses a stream under the requested maximum height
// while keeping codec/container policy independent from the quality ceiling.
//
// "compatibility" is strict MP4: progressive MP4 or adaptive H.264/AAC MP4.
// "vp9" and "av1" prefer that WebM video codec with Opus audio (or a matching
// progressive WebM) and transparently fall back to the automatic best-quality
// policy when the preferred codec is unavailable. The returned selection marks
// that fallback so the job can disclose it to the user.
func selectFormatForStrategy(video *youtube.Video, quality, strategy string) (streamSelection, error) {
	if video == nil {
		return streamSelection{}, errMetadata
	}
	if strategy == "" {
		strategy = "best"
	}
	if !validVideoStrategy(strategy) {
		return streamSelection{}, errCombined
	}
	maxHeight := 0
	if quality != "best" {
		maxHeight, _ = strconv.Atoi(quality)
	}

	var progressive streamSelection
	var progressiveFallback *youtube.Format
	var compatibilityProgressive streamSelection
	var progressiveVP9 streamSelection
	var progressiveAV1 streamSelection
	for i := range video.Formats {
		f := &video.Formats[i]
		kind, codecs, ok := formatType(f)
		if !ok || (kind != "video/mp4" && kind != "video/webm") || f.AudioChannels <= 0 || f.Height <= 0 || f.Width <= 0 || f.InitRange != nil || f.IndexRange != nil || (maxHeight > 0 && f.Height > maxHeight) {
			continue
		}
		if betterVideoFormat(f, progressive.video) {
			progressive = streamSelection{video: f, kind: kind}
		}
		if kind == "video/mp4" && f.Height <= 360 && betterVideoFormat(f, progressiveFallback) {
			progressiveFallback = f
		}
		if kind == "video/mp4" {
			if codecFamily(codecs) == "h264" && strings.Contains(codecs, "mp4a") && betterVideoFormat(f, compatibilityProgressive.video) {
				compatibilityProgressive = streamSelection{video: f, kind: kind}
			}
		}
		if kind == "video/webm" {
			switch codecFamily(codecs) {
			case "vp9":
				if betterVideoFormat(f, progressiveVP9.video) {
					progressiveVP9 = streamSelection{video: f, kind: kind}
				}
			case "av1":
				if betterVideoFormat(f, progressiveAV1.video) {
					progressiveAV1 = streamSelection{video: f, kind: kind}
				}
			}
		}
	}

	var aacAudio *youtube.Format
	var opusAudio *youtube.Format
	for i := range video.Formats {
		f := &video.Formats[i]
		kind, codecs, ok := formatType(f)
		if !ok || f.AudioChannels <= 0 || f.Height != 0 || f.InitRange == nil || f.IndexRange == nil {
			continue
		}
		switch {
		case kind == "audio/mp4" && codecFamily(codecs) == "aac":
			if betterAudioFormat(f, aacAudio) {
				aacAudio = f
			}
		case kind == "audio/webm" && codecFamily(codecs) == "opus":
			if betterAudioFormat(f, opusAudio) {
				opusAudio = f
			}
		}
	}

	var adaptiveMP4 streamSelection
	// Adaptive MP4 audio fallback must be the verified H.264/AAC progressive
	// format so the muxer always receives AAC metadata for an AAC track.
	progressiveMP4 := compatibilityProgressive.video
	if aacAudio != nil {
		for i := range video.Formats {
			f := &video.Formats[i]
			kind, codecs, ok := formatType(f)
			if !ok || kind != "video/mp4" || codecFamily(codecs) != "h264" || f.AudioChannels != 0 || f.Height <= 0 || f.Width <= 0 || f.InitRange == nil || f.IndexRange == nil || (maxHeight > 0 && f.Height > maxHeight) {
				continue
			}
			if betterVideoFormat(f, adaptiveMP4.video) {
				adaptiveMP4 = streamSelection{video: f, audio: aacAudio, progressive: progressiveMP4, kind: kind}
			}
		}
	}
	if adaptiveMP4.video != nil {
		adaptiveMP4.progressiveFallback = progressiveFallback
	}

	var adaptiveWebM streamSelection
	var adaptiveVP9 streamSelection
	var adaptiveAV1 streamSelection
	if opusAudio != nil {
		for i := range video.Formats {
			f := &video.Formats[i]
			kind, codecs, ok := formatType(f)
			if !ok || kind != "video/webm" || f.AudioChannels != 0 || f.Height <= 0 || f.Width <= 0 || f.InitRange == nil || f.IndexRange == nil || (maxHeight > 0 && f.Height > maxHeight) {
				continue
			}
			family := codecFamily(codecs)
			if family != "vp9" && family != "av1" {
				continue
			}
			if betterWebMVideoFormat(f, adaptiveWebM.video) {
				adaptiveWebM = streamSelection{video: f, audio: opusAudio, progressive: progressiveMP4, kind: kind}
			}
			switch family {
			case "vp9":
				if betterVideoFormat(f, adaptiveVP9.video) {
					adaptiveVP9 = streamSelection{video: f, audio: opusAudio, progressive: progressiveMP4, kind: kind}
				}
			case "av1":
				if betterVideoFormat(f, adaptiveAV1.video) {
					adaptiveAV1 = streamSelection{video: f, audio: opusAudio, progressive: progressiveMP4, kind: kind}
				}
			}
		}
	}
	for _, selection := range []*streamSelection{&adaptiveWebM, &adaptiveVP9, &adaptiveAV1} {
		if selection.video != nil {
			selection.progressiveFallback = progressiveFallback
		}
	}

	var bestAutomatic streamSelection
	// Preserve the P2.4 automatic MP4 invariant: adaptive H.264/AAC participates
	// in automatic selection only when a progressive MP4 exists as the verified
	// fallback. The explicit compatibility strategy may still attempt adaptive
	// MP4 without that fallback because the user selected a strict MP4 policy.
	if adaptiveMP4.video != nil && progressiveMP4 != nil {
		bestAutomatic = adaptiveMP4
	}
	if adaptiveWebM.video != nil && (bestAutomatic.video == nil || betterVideoFormat(adaptiveWebM.video, bestAutomatic.video)) {
		bestAutomatic = adaptiveWebM
	}
	if progressive.video != nil && (bestAutomatic.video == nil || !betterVideoFormat(bestAutomatic.video, progressive.video)) {
		bestAutomatic = progressive
	}
	if bestAutomatic.video == nil {
		if video.HLSManifestURL != "" || video.DASHManifestURL != "" {
			return streamSelection{}, errManifest
		}
		return streamSelection{}, errCombined
	}

	if strategy == "compatibility" {
		compatibility := adaptiveMP4
		if compatibility.video != nil {
			// Strict compatibility may only fall back to a progressive stream
			// that is itself verified as H.264/AAC MP4.
			compatibility.progressive = compatibilityProgressive.video
		}
		if compatibilityProgressive.video != nil && (compatibility.video == nil || !betterVideoFormat(compatibility.video, compatibilityProgressive.video)) {
			compatibility = compatibilityProgressive
		}
		if compatibility.video != nil {
			return compatibility, nil
		}
		if video.HLSManifestURL != "" || video.DASHManifestURL != "" {
			return streamSelection{}, errManifest
		}
		return streamSelection{}, errCombined
	}

	if strategy == "vp9" || strategy == "av1" {
		preferredProgressive := progressiveVP9
		preferredAdaptive := adaptiveVP9
		if strategy == "av1" {
			preferredProgressive = progressiveAV1
			preferredAdaptive = adaptiveAV1
		}
		preferred := preferredAdaptive
		if preferredProgressive.video != nil && (preferred.video == nil || !betterVideoFormat(preferred.video, preferredProgressive.video)) {
			preferred = preferredProgressive
		}
		if preferred.video != nil {
			return preferred, nil
		}
		bestAutomatic.preferenceFallback = strategy
		return bestAutomatic, nil
	}

	return bestAutomatic, nil
}

func selectAudioFormat(video *youtube.Video) (*youtube.Format, string, error) {
	if video == nil {
		return nil, "", errMetadata
	}
	var selected *youtube.Format
	var extension string
	for i := range video.Formats {
		f := &video.Formats[i]
		kind, codecs, ok := formatType(f)
		if !ok || f.AudioChannels < 1 || f.AudioChannels > 2 || f.Height != 0 || f.Width != 0 || kind != "audio/mp4" ||
			(f.AudioSampleRate != "44100" && f.AudioSampleRate != "48000") {
			continue
		}
		if !strings.Contains(codecs, "mp4a.40.2") {
			continue
		}
		if betterAudioFormat(f, selected) {
			selected = f
			extension = "m4a"
		}
	}
	if selected == nil {
		return nil, "", errNoAudio
	}
	return selected, extension, nil
}

// start launches the bounded download scheduler and periodic retention pruning.
// Persisted QueuePosition selects which queued job starts next. scheduleChanged
// wakes the scheduler when a job is added, resumed, or reordered.
func (s *server) start() {
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		for {
			s.mu.Lock()
			if s.ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			limit := s.settings.MaxConcurrentDownloads
			if limit < 1 {
				limit = 1
			}
			if s.activeDownloads < limit {
				if j := s.nextQueuedJobLocked(); j != nil {
					s.activeDownloads++
					requested := append([]queueItem(nil), j.Items...)
					ctx, cancel := newJobContext(s.ctx, s.cfg.timeout)
					j.Status, j.cancel = "downloading", cancel
					s.persistJobLocked(j)
					s.mu.Unlock()
					s.wg.Add(1)
					go func(job *jobState, jobCtx context.Context, jobCancel context.CancelFunc, requested []queueItem) {
						defer s.wg.Done()
						defer jobCancel()
						s.run(jobCtx, job, requested)
						s.mu.Lock()
						s.activeDownloads--
						s.notifySchedulerLocked()
						s.mu.Unlock()
					}(j, ctx, cancel, requested)
					continue
				}
			}
			changed := s.scheduleChanged
			s.mu.Unlock()
			select {
			case <-s.ctx.Done():
				return
			case <-changed:
			}
		}
	}()
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case now := <-tick.C:
				s.prune(now)
			}
		}
	}()
}

// run processes one video or the selected playlist entries exposed by the
// upstream library, reuses validated finalized items, records per-item failures,
// and finalizes job status even if an unexpected panic occurs.
type playlistWorkItem struct {
	entry         *youtube.PlaylistEntry
	playlistIndex int
}

func playlistWorkItems(playlist *youtube.Playlist, requested []queueItem) ([]playlistWorkItem, error) {
	if playlist == nil {
		return nil, errPlaylist
	}
	hasSelection := false
	for _, item := range requested {
		if item.PlaylistIndex > 0 {
			hasSelection = true
			break
		}
	}
	if !hasSelection {
		work := make([]playlistWorkItem, 0, len(playlist.Videos))
		for index, entry := range playlist.Videos {
			work = append(work, playlistWorkItem{entry: entry, playlistIndex: index + 1})
		}
		return work, nil
	}
	work := make([]playlistWorkItem, 0, len(requested))
	seen := make(map[int]struct{}, len(requested))
	for _, item := range requested {
		if item.PlaylistIndex < 1 || item.PlaylistIndex > len(playlist.Videos) || !videoID.MatchString(item.VideoID) {
			return nil, errPlaylistSelection
		}
		if _, exists := seen[item.PlaylistIndex]; exists {
			return nil, errPlaylistSelection
		}
		seen[item.PlaylistIndex] = struct{}{}
		entry := playlist.Videos[item.PlaylistIndex-1]
		if entry == nil || entry.ID != item.VideoID {
			return nil, errPlaylistSelection
		}
		work = append(work, playlistWorkItem{entry: entry, playlistIndex: item.PlaylistIndex})
	}
	if len(work) == 0 {
		return nil, errPlaylistSelection
	}
	return work, nil
}

func (s *server) run(ctx context.Context, j *jobState, requested []queueItem) {
	var fatal error
	engine := s.operationEngine()
	defer func() {
		if recover() != nil {
			fatal = errNative
		}
		s.finish(ctx, j, fatal)
	}()
	retryTargets := make(map[int]struct{})
	for _, item := range requested {
		if item.RetryRequested {
			retryTargets[item.Index] = struct{}{}
		}
	}
	work := []playlistWorkItem{{entry: &youtube.PlaylistEntry{ID: strings.TrimPrefix(j.URL, "https://www.youtube.com/watch?v=")}}}
	if j.Kind == "playlist" {
		playlistCtx, cancelPlaylist := context.WithTimeout(ctx, metadataTimeout)
		playlist, err := engine.GetPlaylistContext(playlistCtx, j.URL)
		cancelPlaylist()
		if playlist == nil {
			fatal = errPlaylist
			return
		}
		j.playlistItemCount = len(playlist.Videos)
		selected, selectionErr := playlistWorkItems(playlist, requested)
		if selectionErr != nil {
			fatal = selectionErr
			return
		}
		work = selected
		s.mu.Lock()
		total := len(work)
		j.TotalCount = &total
		if playlist.Title != "" {
			j.Title = playlist.Title
		}
		if len(work) < len(playlist.Videos) && !strings.Contains(j.Note, playlistSelectionNote) {
			j.Note += " " + playlistSelectionNote
		}
		s.mu.Unlock()
		if err != nil {
			fatal = errPlaylist
		}
	} else {
		total := len(work)
		s.mu.Lock()
		j.TotalCount = &total
		s.mu.Unlock()
	}
	s.setQueueItems(j, work, retryTargets)
	s.mu.Lock()
	for index, files := range groupedJobFiles(j) {
		valid := len(files) > 0
		for _, file := range files {
			if !s.validCompletedFile(j, file) {
				valid = false
				break
			}
		}
		if !valid {
			delete(j.fileItems, index)
			delete(j.fileGroups, index)
		}
	}
	s.rebuildFilesLocked(j)
	s.refreshAllQueueItemsLocked(j)
	var used int64
	for _, file := range j.Files {
		used += file.Size
		if file.Subtitle != nil {
			used += file.Subtitle.Size
		}
	}
	s.mu.Unlock()
	tracker := newJobBudget(s.cfg.maxBytes, used)
	workCtx, stopWork := context.WithCancel(ctx)
	defer stopWork()
	var nextMu, fatalMu sync.Mutex
	nextIndex := 0
	workerCount := min(6, len(work))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				nextMu.Lock()
				if nextIndex >= len(work) {
					nextMu.Unlock()
					return
				}
				index := nextIndex
				nextIndex++
				nextMu.Unlock()
				if workCtx.Err() != nil {
					return
				}
				current := index + 1
				if len(retryTargets) > 0 {
					if _, retryThisItem := retryTargets[current]; !retryThisItem {
						continue
					}
				}
				item := work[index]
				outputIndex := item.playlistIndex
				if outputIndex <= 0 {
					outputIndex = current
				}
				entry := item.entry
				title := fmt.Sprintf("Item %d", current)
				if entry != nil && entry.Title != "" {
					title = entry.Title
				}
				if !s.acquireItemSlot(workCtx, j, current, title) {
					return
				}
				err := s.safeProcessItem(workCtx, j, entry, current, outputIndex, tracker)
				s.releaseItemSlot(j, current)
				if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				if workCtx.Err() != nil {
					return
				}
				s.recordFailure(j, current, err)
				if errors.Is(err, errLimit) || errors.Is(err, errStorage) {
					fatalMu.Lock()
					if fatal == nil {
						fatal = err
						stopWork()
					}
					fatalMu.Unlock()
					return
				}
			}
		}()
	}
	workers.Wait()
}

func floatPointer(value float64) *float64 { return &value }

type jobBudget struct {
	mu       sync.Mutex
	limit    int64
	used     int64
	reserved int64
	changed  chan struct{}
}

func newJobBudget(limit, used int64) *jobBudget {
	return &jobBudget{limit: limit, used: used, changed: make(chan struct{})}
}

func (b *jobBudget) reserve(ctx context.Context, requested int64) (int64, error) {
	for {
		b.mu.Lock()
		if b.used > b.limit {
			b.mu.Unlock()
			return 0, errLimit
		}
		amount := requested
		if amount <= 0 {
			amount = b.limit - b.used
		}
		if amount <= 0 || amount > b.limit-b.used {
			b.mu.Unlock()
			return 0, errLimit
		}
		if amount <= b.limit-b.used-b.reserved {
			b.reserved += amount
			b.mu.Unlock()
			return amount, nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-changed:
		}
	}
}

func (b *jobBudget) release(amount, completed int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved -= amount
	if b.reserved < 0 {
		b.reserved = 0
	}
	if completed > amount || completed > b.limit-b.used {
		close(b.changed)
		b.changed = make(chan struct{})
		return errLimit
	}
	b.used += completed
	close(b.changed)
	b.changed = make(chan struct{})
	return nil
}

func (b *jobBudget) reserveOptional(maximum int64) int64 {
	if maximum <= 0 {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	available := b.limit - b.used - b.reserved
	if available <= 0 {
		return 0
	}
	amount := min(maximum, available)
	b.reserved += amount
	return amount
}

func estimatedItemBudget(j *jobState, video *youtube.Video, format *youtube.Format, selection streamSelection) int64 {
	var estimated int64
	if j.MediaType == "audio" {
		if format == nil || format.ContentLength <= 0 {
			return 0
		}
		if j.AudioFormat == "m4a" {
			estimated = format.ContentLength
		} else {
			if video == nil || video.Duration <= 0 {
				return 0
			}
			bitrate, _ := strconv.Atoi(strings.TrimSuffix(j.AudioBitrate, "k"))
			if bitrate <= 0 {
				bitrate = 192
			}
			encoded := int64(float64(bitrate*1000) * video.Duration.Seconds() / 8)
			margin := max(int64(64*1024), encoded/20)
			estimated = format.ContentLength + encoded + margin
		}
	} else if selection.audio != nil {
		if selection.video == nil || selection.video.ContentLength <= 0 {
			return 0
		}
		kind, _, _ := formatType(selection.video)
		if kind == "video/webm" {
			if selection.audio.ContentLength <= 0 {
				return 0
			}
			estimated = selection.video.ContentLength + selection.audio.ContentLength
			// The muxer writes a new WebM container with clusters, cues, and seek
			// metadata, so its output can be larger than the two source tracks.
			// Reserve bounded headroom instead of rejecting a valid final mux.
			estimated += max(int64(1024*1024), estimated/20)
		} else {
			audioBytes := selection.audio.ContentLength
			if selection.progressive != nil && selection.progressive.ContentLength > audioBytes {
				audioBytes = selection.progressive.ContentLength
			}
			if audioBytes <= 0 {
				return 0
			}
			estimated = selection.video.ContentLength + audioBytes
		}
	} else {
		if format == nil || format.ContentLength <= 0 {
			return 0
		}
		estimated = format.ContentLength
	}
	return estimated
}

func (s *server) acquireItemSlot(ctx context.Context, j *jobState, index int, title string) bool {
	for {
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			return false
		}
		limit := s.settings.MaxConcurrentDownloads
		if limit < 1 {
			limit = 1
		}
		if s.activeItems < limit {
			s.activeItems++
			j.ActiveItemCount++
			if j.processingItems == 0 {
				j.Status = "downloading"
			}
			if j.itemProgress == nil {
				j.itemProgress = make(map[int]*itemProgress)
			}
			j.itemProgress[index] = &itemProgress{title: title, progressAt: time.Now()}
			s.refreshQueueItemLocked(j, index)
			s.refreshProgressLocked(j)
			s.publishJobEventLocked("job-status", j)
			s.mu.Unlock()
			return true
		}
		changed := s.scheduleChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
}

func (s *server) releaseItemSlot(j *jobState, index int) {
	s.mu.Lock()
	delete(j.itemProgress, index)
	if j.ActiveItemCount > 0 {
		j.ActiveItemCount--
	}
	if s.activeItems > 0 {
		s.activeItems--
	}
	s.refreshQueueItemLocked(j, index)
	s.refreshProgressLocked(j)
	s.publishJobEventLocked("job-progress", j)
	s.notifySchedulerLocked()
	s.mu.Unlock()
}

func (s *server) setItemMetadata(j *jobState, index int, video *youtube.Video) {
	if video == nil {
		return
	}
	s.mu.Lock()
	if index > 0 && index <= len(j.Items) {
		item := &j.Items[index-1]
		item.VideoID = video.ID
		item.Title = video.Title
		item.Author = video.Author
		item.DurationSeconds = int64(video.Duration.Seconds())
		item.ThumbnailURL = safeThumbnailURL(video.Thumbnails)
	}
	if progress := j.itemProgress[index]; progress != nil {
		progress.title = video.Title
		s.refreshProgressLocked(j)
	}
	s.refreshQueueItemLocked(j, index)
	s.persistJobLocked(j)
	s.mu.Unlock()
}

func (s *server) setProcessing(j *jobState, index int, processing bool) {
	s.mu.Lock()
	if processing {
		j.processingItems++
	} else if j.processingItems > 0 {
		j.processingItems--
	}
	if item := j.itemProgress[index]; item != nil {
		item.processing = processing
	}
	if j.processingItems > 0 {
		j.Status = "processing"
	} else if j.ActiveItemCount > 0 {
		j.Status = "downloading"
	}
	s.refreshQueueItemLocked(j, index)
	s.publishJobEventLocked("job-status", j)
	s.mu.Unlock()
}

func makeQueueItem(index, playlistIndex int, entry *youtube.PlaylistEntry) queueItem {
	item := queueItem{Index: index, PlaylistIndex: playlistIndex, Title: fmt.Sprintf("Item %d", index), Status: "queued"}
	if entry != nil {
		item.VideoID = entry.ID
		if entry.Title != "" {
			item.Title = entry.Title
		}
		item.Author = entry.Author
		item.DurationSeconds = int64(entry.Duration.Seconds())
		item.ThumbnailURL = safeThumbnailURL(entry.Thumbnails)
	}
	return item
}

func (s *server) setQueueItems(j *jobState, work []playlistWorkItem, retryTargets map[int]struct{}) {
	s.mu.Lock()
	previous := append([]queueItem(nil), j.Items...)
	items := make([]queueItem, len(work))
	for index, selected := range work {
		playlistIndex := 0
		if j.Kind == "playlist" {
			playlistIndex = selected.playlistIndex
		}
		items[index] = makeQueueItem(index+1, playlistIndex, selected.entry)
		_, items[index].RetryRequested = retryTargets[index+1]
		if len(retryTargets) > 0 && !items[index].RetryRequested && index < len(previous) {
			items[index].Status = previous[index].Status
			items[index].Error = previous[index].Error
			items[index].Progress = previous[index].Progress
			items[index].DownloadedBytes = previous[index].DownloadedBytes
			items[index].TotalBytes = previous[index].TotalBytes
			items[index].FileID = previous[index].FileID
			items[index].FileIDs = append([]string(nil), previous[index].FileIDs...)
		}
	}
	j.Items = items
	s.refreshAllQueueItemsLocked(j)
	s.persistJobLocked(j)
	s.mu.Unlock()
}

func (s *server) refreshAllQueueItemsLocked(j *jobState) {
	for index := range j.Items {
		s.refreshQueueItemLocked(j, index+1)
	}
}

func (s *server) refreshQueueItemLocked(j *jobState, index int) {
	if index < 1 || index > len(j.Items) {
		return
	}
	item := &j.Items[index-1]
	if files := itemFiles(j, index); len(files) > 0 {
		file := files[0]
		item.Status = "completed"
		item.Progress = floatPointer(100)
		var totalSize int64
		item.FileIDs = make([]string, 0, len(files))
		for _, output := range files {
			totalSize += output.Size
			item.FileIDs = append(item.FileIDs, output.ID)
		}
		item.DownloadedBytes, item.TotalBytes = totalSize, totalSize
		item.SpeedBytesPerSec, item.ETASeconds = 0, 0
		item.FileID = file.ID
		item.Error = ""
		if file.Title != "" && file.ChapterIndex == 0 {
			item.Title = file.Title
		}
		if file.Author != "" {
			item.Author = file.Author
		}
		if file.DurationSeconds > 0 && file.ChapterIndex == 0 {
			item.DurationSeconds = file.DurationSeconds
		}
		if file.ThumbnailURL != "" {
			item.ThumbnailURL = file.ThumbnailURL
		}
		return
	}
	for _, failure := range j.Failures {
		if failure.Index == index {
			item.Status, item.Error = "failed", failure.Error
			item.SpeedBytesPerSec, item.ETASeconds = 0, 0
			item.FileID, item.FileIDs = "", nil
			return
		}
	}
	if progress := j.itemProgress[index]; progress != nil {
		item.Status = "downloading"
		if progress.processing {
			item.Status = "processing"
		}
		item.Progress = nil
		if progress.total > 0 {
			value := min(100.0, 100*float64(progress.downloaded)/float64(progress.total))
			item.Progress = &value
		}
		item.DownloadedBytes, item.TotalBytes = progress.downloaded, progress.total
		item.SpeedBytesPerSec = progress.speed
		item.ETASeconds = 0
		if progress.total > progress.downloaded && progress.speed > 0 {
			item.ETASeconds = (progress.total - progress.downloaded) / progress.speed
		}
		item.Error, item.FileID, item.FileIDs = "", "", nil
		return
	}
	item.SpeedBytesPerSec, item.ETASeconds = 0, 0
	retryMode := false
	for _, candidate := range j.Items {
		if candidate.RetryRequested {
			retryMode = true
			break
		}
	}
	if retryMode && !item.RetryRequested && (item.Status == "failed" || item.Status == "cancelled") {
		return
	}
	item.FileID, item.FileIDs = "", nil
	switch {
	case j.pauseRequested || j.Status == "paused":
		item.Status = "paused"
	case j.cancelRequested || j.Status == "cancelled":
		item.Status = "cancelled"
	case j.Status == "partial" && item.Status == "cancelled":
		item.Status, item.Error = "cancelled", ""
	case j.Status == "failed" || (j.Status == "partial" && j.Error != ""):
		item.Status, item.Error = "failed", j.Error
	default:
		item.Status, item.Error = "queued", ""
	}
}

func (s *server) refreshProgressLocked(j *jobState) {
	indices := make([]int, 0, len(j.itemProgress))
	for index := range j.itemProgress {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	var downloaded, total, speed int64
	totalKnown := len(indices) > 0
	titles := make([]string, 0, len(indices))
	for _, index := range indices {
		item := j.itemProgress[index]
		if item.title != "" {
			titles = append(titles, item.title)
		}
		downloaded += item.downloaded
		speed += item.speed
		if item.total > 0 {
			total += item.total
		} else {
			totalKnown = false
		}
	}
	j.ActiveItemCount = len(indices)
	if len(titles) > 0 {
		j.CurrentItem = titles[0]
		if len(titles) > 1 {
			j.CurrentItem += fmt.Sprintf(" + %d more", len(titles)-1)
		}
	}
	j.DownloadedBytes, j.SpeedBytesPerSec = downloaded, speed
	j.TotalBytes, j.ETASeconds = 0, 0
	if totalKnown {
		j.TotalBytes = total
		if total > 0 {
			j.Progress = floatPointer(min(100.0, 100*float64(downloaded)/float64(total)))
		}
		if total > downloaded && speed > 0 {
			j.ETASeconds = (total - downloaded) / speed
		}
	} else if len(indices) > 0 {
		j.Progress = nil
	}
}

func (s *server) updateProgress(j *jobState, index int, downloaded, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := j.itemProgress[index]
	if item == nil {
		item = &itemProgress{progressAt: time.Now()}
		j.itemProgress[index] = item
	}
	now := time.Now()
	if total > 0 {
		item.total = total
	}
	item.downloaded = downloaded
	if elapsed := now.Sub(item.progressAt).Seconds(); elapsed > 0.2 && downloaded >= item.progressBytes {
		item.speed = int64(float64(downloaded-item.progressBytes) / elapsed)
		item.progressAt, item.progressBytes = now, downloaded
	}
	s.refreshQueueItemLocked(j, index)
	s.refreshProgressLocked(j)
	if item.eventAt.IsZero() || now.Sub(item.eventAt) >= 200*time.Millisecond || (total > 0 && downloaded >= total) {
		item.eventAt = now
		s.publishJobEventLocked("job-progress", j)
	}
}

func (s *server) processItem(ctx context.Context, j *jobState, entry *youtube.PlaylistEntry, current, outputIndex int, tracker *jobBudget) error {
	s.mu.Lock()
	if completed := itemFiles(j, current); len(completed) > 0 {
		valid := true
		for _, file := range completed {
			if !s.validCompletedFile(j, file) {
				valid = false
				break
			}
		}
		if valid {
			if progress := j.itemProgress[current]; progress != nil {
				var size int64
				for _, file := range completed {
					size += file.Size
				}
				progress.downloaded, progress.total = size, size
			}
			s.refreshProgressLocked(j)
			s.mu.Unlock()
			return nil
		}
		delete(j.fileItems, current)
		delete(j.fileGroups, current)
		s.rebuildFilesLocked(j)
		s.persistJobLocked(j)
	}
	s.mu.Unlock()
	if entry == nil || !videoID.MatchString(entry.ID) {
		return errMetadata
	}
	engine := s.operationEngine()
	var video *youtube.Video
	var err error
	metadataCtx, cancelMetadata := context.WithTimeout(ctx, metadataTimeout)
	if j.Kind == "video" {
		video, err = engine.GetVideoContext(metadataCtx, j.URL)
	} else {
		video, err = engine.VideoFromPlaylistEntryContext(metadataCtx, entry)
	}
	cancelMetadata()
	if err != nil || video == nil || video.ID != entry.ID {
		if err != nil {
			if deadlineErr := itemDeadlineError(ctx, metadataCtx, err); deadlineErr != err {
				return deadlineErr
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else if errors.Is(metadataCtx.Err(), context.DeadlineExceeded) {
			return errItemTimeout
		}
		return errMetadata
	}
	if extractor, ok := engine.(interface{ FallbackUsed() bool }); ok && extractor.FallbackUsed() {
		s.noteExtractionFallback(j)
	}
	s.setItemMetadata(j, current, video)
	var selection streamSelection
	var format *youtube.Format
	var extension string
	if j.MediaType == "audio" {
		format, extension, err = selectAudioFormat(video)
	} else {
		selection, err = selectFormatForStrategy(video, j.Quality, j.VideoStrategy)
		if err == nil && selection.preferenceFallback != "" {
			s.noteCodecPreferenceFallback(j, selection.preferenceFallback)
		}
	}
	if err != nil {
		return err
	}
	requested := estimatedItemBudget(j, video, format, selection)
	budget, err := tracker.reserve(ctx, requested)
	if err != nil {
		return err
	}
	if requested <= 0 {
		requested = s.cfg.maxBytes
	}
	bandwidthLimit := int64(0)
	if s.bandwidth != nil {
		bandwidthLimit = s.bandwidth.Limit()
	}
	transferCtx, cancelTransfer := context.WithTimeout(ctx, itemTransferTimeout(video, requested, bandwidthLimit))
	defer cancelTransfer()
	leaseOpen := true
	defer func() {
		if leaseOpen {
			_ = tracker.release(budget, 0)
		}
	}()
	var files []mediaFile
	if j.MediaType == "audio" {
		if j.SplitByChapter && j.AudioFormat == "mp3" && len(parseDescriptionChapters(video.Description, video.Duration)) > 1 {
			files, err = s.transferChapterMP3(transferCtx, j, engine, video, format, extension, current, outputIndex, budget)
		} else if j.AudioFormat == "m4a" {
			var file mediaFile
			file, err = s.transferOriginalAudio(transferCtx, j, engine, video, format, current, outputIndex, budget)
			files = []mediaFile{file}
		} else {
			var file mediaFile
			file, err = s.transferAudio(transferCtx, j, engine, video, format, extension, current, outputIndex, budget)
			files = []mediaFile{file}
		}
	} else {
		var file mediaFile
		file, err = s.transfer(transferCtx, j, engine, video, selection, current, outputIndex, budget)
		files = []mediaFile{file}
	}
	if err != nil {
		return itemDeadlineError(ctx, transferCtx, err)
	}
	if len(files) == 0 {
		return errAudioConvert
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		for _, file := range files {
			if file.ManagedAvailable {
				_ = os.Remove(filepath.Join(j.dir, file.Name))
			}
			if file.PublishedAvailable {
				_ = os.Remove(file.OutputPath)
			}
			if file.Subtitle != nil {
				_ = os.Remove(filepath.Join(j.dir, file.Subtitle.Name))
				if file.Subtitle.OutputPath != "" {
					_ = os.Remove(file.Subtitle.OutputPath)
				}
			}
		}
	}()
	var outputSize int64
	formatForName := format
	if j.MediaType != "audio" {
		formatForName = selection.video
	}
	for index := range files {
		file := &files[index]
		if j.MediaType != "audio" {
			applyVideoMetadata(file, video)
			file.Category = j.Category
			file.ManagedAvailable = true
			s.captureThumbnail(ctx, j, file)
		} else if !video.PublishDate.IsZero() && file.PublishDate == "" {
			file.PublishDate = video.PublishDate.UTC().Format("2006-01-02")
		}
		if file.SourceItemIndex == 0 {
			file.SourceItemIndex = current
		}
		if supportsContainerMetadata(file.MimeType) {
			metadataExtra := tracker.reserveOptional(containerMetadataHeadroom)
			if taggedSize, tagErr := rewriteContainerMetadata(transferCtx, j, *file, video.ID, budget+metadataExtra); tagErr != nil {
				if metadataExtra > 0 {
					_ = tracker.release(metadataExtra, 0)
				}
				if errors.Is(tagErr, errStorage) || errors.Is(tagErr, context.Canceled) || errors.Is(tagErr, context.DeadlineExceeded) {
					return tagErr
				}
				log.Printf("container metadata was not embedded for %s: %v", file.Name, tagErr)
			} else if taggedSize > file.Size {
				budget += metadataExtra
				file.Size = taggedSize
			} else if metadataExtra > 0 {
				_ = tracker.release(metadataExtra, 0)
			}
		}
		file.naming.ID = video.ID
		file.naming.UploadDate = file.PublishDate
		file.naming.Index = strconv.Itoa(outputIndex)
		if j.Kind == "playlist" {
			file.naming.Playlist = j.Title
		}
		if file.naming.FPS == "" && formatForName != nil && formatForName.FPS > 0 {
			file.naming.FPS = strconv.Itoa(formatForName.FPS)
		}
		if file.naming.Codec == "" && formatForName != nil {
			file.naming.Codec = namingCodec(formatForName.MimeType)
		}
		if j.MediaType == "audio" && j.AudioFormat == "mp3" {
			file.naming.Codec = "mp3"
		}
		if j.StorageMode != "managed-only" {
			if err := s.publishOutput(j, file); err != nil {
				return errStorage
			}
		}
		if j.StorageMode == "published-only" {
			if err := os.Remove(filepath.Join(j.dir, file.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return errStorage
			}
			file.ManagedAvailable = false
		}
		outputSize += file.Size
	}
	if err := tracker.release(budget, outputSize); err != nil {
		return err
	}
	leaseOpen = false
	if j.SubtitleLanguage != "" && len(files) == 1 {
		file := &files[0]
		captionBudget := tracker.reserveOptional(maxCaptionBytes)
		if captionBudget == 0 {
			file.SubtitleError = "Caption sidecar was skipped because the job storage limit is exhausted"
		} else {
			subtitleSize := s.captureSubtitle(ctx, j, file, video, captionBudget)
			if file.Subtitle != nil && file.Subtitle.ManagedAvailable && j.StorageMode != "managed-only" {
				if err := publishSubtitleOutput(j, file); err != nil {
					file.SubtitleError = "Caption sidecar was saved in the Library but could not be published next to the media file"
				}
			}
			if file.Subtitle != nil && file.Subtitle.ManagedAvailable && j.StorageMode == "published-only" {
				if err := os.Remove(filepath.Join(j.dir, file.Subtitle.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
					file.SubtitleError = "Published caption sidecar exists, but its temporary managed copy could not be removed"
				} else {
					file.Subtitle.ManagedAvailable = false
				}
			}
			if releaseErr := tracker.release(captionBudget, subtitleSize); releaseErr != nil {
				if file.Subtitle != nil && file.Subtitle.ManagedAvailable {
					_ = os.Remove(filepath.Join(j.dir, file.Subtitle.Name))
				}
				if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
					_ = os.Remove(file.Subtitle.OutputPath)
				}
				file.Subtitle = nil
				file.SubtitleError = "Caption sidecar was skipped because the job storage limit was exceeded"
			}
		}
	} else if j.SubtitleLanguage != "" {
		for index := range files {
			files[index].SubtitleError = "Captions remain unsplit when audio is split by chapter"
		}
	}
	s.mu.Lock()
	setItemFiles(j, current, files)
	s.rebuildFilesLocked(j)
	s.refreshQueueItemLocked(j, current)
	progress := 100.0
	j.Progress = &progress
	s.persistJobLocked(j)
	s.publishJobEventLocked("job-file-finalized", j)
	s.mu.Unlock()
	committed = true
	return nil
}

func itemTransferTimeout(video *youtube.Video, expectedBytes, bandwidthLimit int64) time.Duration {
	rate := int64(defaultTransferRate)
	if bandwidthLimit > 0 {
		rate = bandwidthLimit
	}
	byteSeconds := float64(max(int64(0), expectedBytes)) / float64(rate) * 2
	durationSeconds := 0.0
	if video != nil && video.Duration > 0 {
		durationSeconds = video.Duration.Seconds() * 4
	}
	seconds := max(float64(minimumItemTimeout.Seconds()), byteSeconds+itemTimeoutOverhead.Seconds(), durationSeconds+itemTimeoutOverhead.Seconds())
	if seconds >= maximumItemTimeout.Seconds() {
		return maximumItemTimeout
	}
	return time.Duration(seconds * float64(time.Second))
}

func (s *server) safeProcessItem(ctx context.Context, j *jobState, entry *youtube.PlaylistEntry, current, outputIndex int, tracker *jobBudget) (err error) {
	defer func() {
		if recover() != nil {
			err = errNative
		}
	}()
	return s.processItem(ctx, j, entry, current, outputIndex, tracker)
}

func (s *server) rebuildFilesLocked(j *jobState) {
	groups := groupedJobFiles(j)
	indices := make([]int, 0, len(groups))
	for index := range groups {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	j.Files = make([]mediaFile, 0, len(indices))
	for _, index := range indices {
		j.Files = append(j.Files, groups[index]...)
	}
	j.CompletedCount = len(indices)
}

func itemFiles(j *jobState, index int) []mediaFile {
	if j == nil {
		return nil
	}
	if group := j.fileGroups[index]; len(group) > 0 {
		return group
	}
	if file, ok := j.fileItems[index]; ok {
		return []mediaFile{file}
	}
	return nil
}

func setItemFiles(j *jobState, index int, files []mediaFile) {
	if j.fileItems == nil {
		j.fileItems = make(map[int]mediaFile)
	}
	if j.fileGroups == nil {
		j.fileGroups = make(map[int][]mediaFile)
	}
	if len(files) == 0 {
		delete(j.fileItems, index)
		delete(j.fileGroups, index)
		return
	}
	j.fileItems[index] = files[0]
	j.fileGroups[index] = append([]mediaFile(nil), files...)
}

func syncStoredFile(j *jobState, file mediaFile) {
	for itemIndex, saved := range j.fileItems {
		if saved.ID == file.ID {
			j.fileItems[itemIndex] = file
		}
	}
	for itemIndex := range j.fileGroups {
		for groupIndex := range j.fileGroups[itemIndex] {
			if j.fileGroups[itemIndex][groupIndex].ID == file.ID {
				j.fileGroups[itemIndex][groupIndex] = file
			}
		}
	}
}

func (s *server) transferAudio(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, extension string, queueIndex, outputIndex int, budget int64) (result mediaFile, err error) {
	if budget <= 0 || (format.ContentLength > 0 && format.ContentLength > budget) {
		return result, errLimit
	}
	result = mediaFile{ID: randomID(16), Name: fmt.Sprintf("%06d-%s.mp3", outputIndex, video.ID), MimeType: "audio/mpeg", MediaType: "audio", naming: namingValues{Codec: "mp3"}}
	applyVideoMetadata(&result, video)
	result.Category = j.Category
	result.ManagedAvailable = true
	s.captureThumbnail(ctx, j, &result)
	defer func() {
		if err != nil && result.ThumbnailLocalAvailable {
			if path, pathErr := thumbnailPath(j, result); pathErr == nil {
				_ = os.Remove(path)
			}
			result.ThumbnailLocalAvailable = false
		}
	}()
	finalPath := filepath.Join(j.dir, result.Name)
	sourcePath := filepath.Join(j.dir, fmt.Sprintf("%06d-%s.source.%s", outputIndex, video.ID, extension))
	outputPart := finalPath + ".part"
	_ = os.Remove(finalPath)
	_ = os.Remove(outputPart)
	defer func() {
		if removeErr := os.Remove(outputPart); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = errStorage
		}
		if err != nil && !s.keepPartial(j) {
			if removeErr := os.Remove(sourcePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, errStorage)
			}
			if checkpointErr := s.deleteDownloadPart(j, queueIndex, "mp3-source"); checkpointErr != nil {
				err = errors.Join(err, errStorage)
			}
		}
	}()
	sourceSize, err := s.downloadSource(ctx, j, engine, video, format, sourcePath, "mp3-source", queueIndex, budget, func(written int64) {
		s.updateProgress(j, queueIndex, written, format.ContentLength)
	})
	if err != nil {
		return result, err
	}
	outputBudget := budget - sourceSize
	if outputBudget <= 0 {
		return result, errLimit
	}
	s.updateProgress(j, queueIndex, sourceSize, sourceSize+outputBudget)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	output, openErr := os.OpenFile(outputPart, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if openErr != nil {
		return result, errStorage
	}
	s.setProcessing(j, queueIndex, true)
	defer s.setProcessing(j, queueIndex, false)
	s.mu.Lock()
	j.Progress = floatPointer(99)
	s.persistJobLocked(j)
	s.mu.Unlock()
	source, openErr := os.Open(sourcePath)
	if openErr != nil {
		_ = output.Close()
		return result, errStorage
	}
	tag, tagErr := buildID3v23Tag(mp3MetadataFor(j, result, video.ID, outputIndex))
	if tagErr != nil {
		tag = nil // Metadata is best-effort; never fail playable audio over tags.
	}
	outputSize, convertErr := convertAACToMP3Tagged(ctx, source, output, j.AudioBitrate, outputBudget, tag, func(written int64) {
		s.updateProgress(j, queueIndex, sourceSize+written, 0)
	})
	sourceCloseErr := source.Close()
	outputSyncErr := output.Sync()
	outputCloseErr := output.Close()
	if convertErr != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, convertErr
	}
	if sourceCloseErr != nil || outputSyncErr != nil || outputCloseErr != nil {
		return result, errStorage
	}
	if outputSize <= 0 {
		return result, errLength
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if _, statErr := os.Lstat(finalPath); !errors.Is(statErr, os.ErrNotExist) {
		return result, errStorage
	}
	if durableRename(outputPart, finalPath) != nil {
		return result, errStorage
	}
	if removeErr := os.Remove(sourcePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return result, errStorage
	}
	if checkpointErr := s.deleteDownloadPart(j, queueIndex, "mp3-source"); checkpointErr != nil {
		return result, errStorage
	}
	result.Size = outputSize
	s.updateProgress(j, queueIndex, sourceSize+outputSize, sourceSize+outputSize)
	return result, nil
}

func (s *server) transferOriginalAudio(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, queueIndex, outputIndex int, budget int64) (result mediaFile, err error) {
	if budget <= 0 || (format.ContentLength > 0 && format.ContentLength > budget) {
		return result, errLimit
	}
	result = mediaFile{ID: randomID(16), Name: fmt.Sprintf("%06d-%s.m4a", outputIndex, video.ID), MimeType: "audio/mp4", MediaType: "audio", naming: formatNamingValues(format)}
	applyVideoMetadata(&result, video)
	result.Category = j.Category
	result.ManagedAvailable = true
	s.captureThumbnail(ctx, j, &result)
	defer func() {
		if err != nil && result.ThumbnailLocalAvailable {
			if path, pathErr := thumbnailPath(j, result); pathErr == nil {
				_ = os.Remove(path)
			}
			result.ThumbnailLocalAvailable = false
		}
	}()

	finalPath := filepath.Join(j.dir, result.Name)
	if existing, openErr := openFinal(j.dir, result.Name); openErr == nil {
		info, statErr := existing.Stat()
		_ = existing.Close()
		if statErr == nil && format.ContentLength > 0 && info.Size() == format.ContentLength {
			result.Size = info.Size()
			if err := s.deleteDownloadPart(j, queueIndex, "m4a"); err != nil {
				return result, errStorage
			}
			return result, nil
		}
		_ = os.Remove(filepath.Join(j.dir, result.Name))
	}
	part := finalPath + ".part"
	rangeFormat := s.resolveRangeFormat(ctx, engine, video, format)
	if rangeFormat.ContentLength > 0 && rangeFormat.URL != "" {
		result.Size, err = s.downloadSource(ctx, j, engine, video, rangeFormat, part, "m4a", queueIndex, budget, func(written int64) {
			s.updateProgress(j, queueIndex, written, format.ContentLength)
		})
		if err != nil {
			return result, err
		}
		if err := durableRename(part, finalPath); err != nil {
			return result, errStorage
		}
		if err := s.deleteDownloadPart(j, queueIndex, "m4a"); err != nil {
			return result, errStorage
		}
		return result, nil
	}
	_ = os.Remove(part)
	stream, reported, streamErr := engine.GetStreamContext(ctx, video, format)
	if stream == nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errRead
	}
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(func() { _ = stream.Close() }) }
	stopClose := context.AfterFunc(ctx, closeStream)
	defer func() { stopClose(); closeStream() }()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if streamErr != nil {
		return result, errRead
	}
	expected := format.ContentLength
	if reported > 0 {
		if expected > 0 && reported != expected {
			return result, errLength
		}
		expected = reported
	}
	if expected > budget {
		return result, errLimit
	}
	if expected <= 0 {
		s.mu.Lock()
		if !strings.Contains(j.Note, unknownLengthNote) {
			j.Note += " " + unknownLengthNote
		}
		s.mu.Unlock()
	}
	output, openErr := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if openErr != nil {
		return result, errStorage
	}
	finalized := false
	defer func() {
		_ = output.Close()
		if !finalized && !s.keepPartial(j) {
			if removeErr := os.Remove(part); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
				err = errStorage
			}
		}
	}()
	result.Size, err = copyStream(ctx, output, s.bandwidthReader(ctx, idleDeadlineReader{reader: stream, timeout: transferIdleTimeout, onIdle: closeStream}), budget, expected, func(written int64) {
		s.updateProgress(j, queueIndex, written, expected)
	})
	if err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if result.Size <= 0 || (expected > 0 && result.Size != expected) {
		return result, errLength
	}
	if _, statErr := os.Lstat(finalPath); !errors.Is(statErr, os.ErrNotExist) {
		return result, errStorage
	}
	if syncCloseRename(output, part, finalPath) != nil {
		return result, errStorage
	}
	finalized = true
	s.updateProgress(j, queueIndex, result.Size, result.Size)
	return result, nil
}

func (s *server) validCompletedFile(j *jobState, file mediaFile) bool {
	available := false
	if file.ManagedAvailable {
		f, err := openFinal(j.dir, file.Name)
		if err != nil {
			return false
		}
		info, err := f.Stat()
		_ = f.Close()
		if err != nil || info.Size() != file.Size {
			return false
		}
		available = true
	}
	if file.PublishedAvailable {
		if err := validateOutputCopy(j, file); err != nil {
			return false
		}
		output, statErr := os.Lstat(file.OutputPath)
		if statErr != nil || !output.Mode().IsRegular() || output.Size() != file.Size {
			return false
		}
		available = true
	}
	return available
}

func (s *server) keepPartial(j *jobState) bool {
	s.mu.Lock()
	paused, cancelled := j.pauseRequested, j.cancelRequested
	s.mu.Unlock()
	if paused {
		return true
	}
	if s.ctx.Err() == nil {
		return false
	}
	return !cancelled
}

func (s *server) recordFailure(j *jobState, index int, err error) {
	message := "Download interrupted"
	for _, safe := range []error{errMetadata, errPlaylist, errCombined, errManifest, errRead, errLength, errMux, errStorage, errLimit, errNative, errNoAudio, errAudioConvert, errItemTimeout} {
		if errors.Is(err, safe) {
			message = safe.Error()
			break
		}
	}
	s.mu.Lock()
	position := sort.Search(len(j.Failures), func(i int) bool { return j.Failures[i].Index > index })
	j.Failures = append(j.Failures, itemFailure{})
	copy(j.Failures[position+1:], j.Failures[position:])
	j.Failures[position] = itemFailure{Index: index, Error: message}
	s.refreshQueueItemLocked(j, index)
	s.persistJobLocked(j)
	s.publishJobEventLocked("job-error", j)
	s.mu.Unlock()
}

func (s *server) finish(ctx context.Context, j *jobState, fatal error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j.cancel, j.done = nil, time.Now()
	j.Status = "failed"
	if len(j.Files) > 0 {
		j.Status = "partial"
	}
	switch {
	case j.persistenceFailed:
		j.Status = "failed"
		if len(j.Files) > 0 {
			j.Status = "partial"
		}
		j.Error = "Could not save job state; download stopped"
	case j.pauseRequested && !j.cancelRequested:
		j.Status, j.Error, j.done = "paused", "Paused. The current playlist item may restart when resumed.", time.Time{}
	case s.ctx.Err() != nil && !j.cancelRequested:
		j.Status, j.Error, j.done = "queued", "Download paused; it will resume when the service restarts", time.Time{}
	case j.cancelRequested:
		j.Status, j.Error = "cancelled", "Download cancelled; only finalized files are available"
		if err := s.persistOperationLocked("delete resumable download state", j.ID, func(store *jobStore) error {
			return store.deletePartsForJob(j.ID)
		}); err != nil {
			s.recordPersistenceFailure("delete resumable download state", j.ID, err)
		} else {
			s.recordPersistenceSuccess()
		}
	case ctx.Err() != nil:
		j.Error = "Job timeout reached; remaining entries were not downloaded"
	case fatal != nil:
		j.Error = fatal.Error()
	case len(j.Failures) > 0:
		j.Error = fmt.Sprintf("%d entry/entries failed; first failure at item %d: %s", len(j.Failures), j.Failures[0].Index, j.Failures[0].Error)
	case j.TotalCount == nil || *j.TotalCount != j.CompletedCount || j.CompletedCount == 0:
		j.Error = "No complete output, or not every exposed entry was downloaded"
	default:
		j.Status, j.Error = "completed", ""
	}
	s.refreshAllQueueItemsLocked(j)
	if terminal(j.Status) {
		for index := range j.Items {
			j.Items[index].RetryRequested = false
		}
	}
	s.persistJobLocked(j)
	if j.Error != "" {
		s.publishJobEventLocked("job-error", j)
	}
	s.evictTerminalJobLocked(j)
}

func (s *server) transfer(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, selection streamSelection, queueIndex, outputIndex int, budget int64) (mediaFile, error) {
	if selection.audio != nil {
		return s.transferAdaptive(ctx, j, engine, video, selection, queueIndex, outputIndex, budget)
	}
	return s.transferProgressive(ctx, j, engine, video, selection.video, selection.kind, queueIndex, outputIndex, budget)
}

func formatNamingValues(format *youtube.Format) namingValues {
	if format == nil {
		return namingValues{}
	}
	values := namingValues{Codec: namingCodec(format.MimeType)}
	if format.FPS > 0 {
		values.FPS = strconv.Itoa(format.FPS)
	}
	return values
}

func (s *server) transferProgressive(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, kind string, queueIndex, outputIndex int, budget int64) (result mediaFile, err error) {
	if budget <= 0 || format.ContentLength > budget {
		return result, errLimit
	}
	result = mediaFile{ID: randomID(16), Name: fmt.Sprintf("%06d-%s.%s", outputIndex, video.ID, strings.TrimPrefix(kind, "video/")), Height: format.Height, MimeType: kind, naming: formatNamingValues(format)}
	path := filepath.Join(j.dir, result.Name)
	part := path + ".part"
	rangeFormat := s.resolveRangeFormat(ctx, engine, video, format)
	if rangeFormat.ContentLength > 0 && rangeFormat.URL != "" {
		if existing, openErr := openFinal(j.dir, result.Name); openErr == nil {
			info, statErr := existing.Stat()
			_ = existing.Close()
			if statErr == nil && info.Size() == rangeFormat.ContentLength {
				result.Size = info.Size()
				if err := s.deleteDownloadPart(j, queueIndex, "progressive"); err != nil {
					return result, errStorage
				}
				return result, nil
			}
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return result, errStorage
		}
		result.Size, err = s.downloadSource(ctx, j, engine, video, rangeFormat, part, "progressive", queueIndex, budget, func(written int64) {
			s.updateProgress(j, queueIndex, written, rangeFormat.ContentLength)
		})
		if err != nil {
			return result, err
		}
		if err := durableRename(part, path); err != nil {
			return result, errStorage
		}
		if err := s.deleteDownloadPart(j, queueIndex, "progressive"); err != nil {
			return result, errStorage
		}
		return result, nil
	}
	stream, reported, streamErr := engine.GetStreamContext(ctx, video, format)
	if stream == nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errRead
	}
	var once sync.Once
	var closeErr error
	closeStream := func() {
		once.Do(func() {
			defer func() {
				if recover() != nil {
					closeErr = errNative
				}
			}()
			closeErr = stream.Close()
		})
	}
	stopClose := context.AfterFunc(ctx, closeStream)
	defer func() { stopClose(); closeStream() }()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if streamErr != nil {
		return result, errRead
	}
	expected := format.ContentLength
	if reported > 0 {
		if expected > 0 && reported != expected {
			return result, errLength
		}
		expected = reported
	}
	if expected > budget {
		return result, errLimit
	}
	if expected <= 0 {
		s.mu.Lock()
		if !strings.Contains(j.Note, unknownLengthNote) {
			j.Note += " " + unknownLengthNote
		}
		s.mu.Unlock()
	}
	if existing, openErr := openFinal(j.dir, result.Name); openErr == nil {
		info, statErr := existing.Stat()
		_ = existing.Close()
		if statErr == nil && expected > 0 && info.Size() == expected {
			result.Size = info.Size()
			return result, nil
		}
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return result, errStorage
	}
	_ = os.Remove(part)
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, errStorage
	}
	finalized := false
	defer func() {
		_ = f.Close()
		if !finalized && !s.keepPartial(j) {
			if removeErr := os.Remove(part); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errStorage
			}
		}
	}()
	result.Size, err = copyStream(ctx, f, s.bandwidthReader(ctx, idleDeadlineReader{reader: stream, timeout: transferIdleTimeout, onIdle: closeStream}), budget, expected, func(written int64) {
		s.updateProgress(j, queueIndex, written, expected)
	})
	closeStream()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, errRead
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
		return result, errStorage
	}
	if syncCloseRename(f, part, path) != nil {
		return result, errStorage
	}
	finalized = true
	return result, nil
}

func (s *server) transferAdaptive(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, selection streamSelection, queueIndex, outputIndex int, budget int64) (mediaFile, error) {
	kind, _, ok := formatType(selection.video)
	if ok && kind == "video/webm" {
		return s.transferAdaptiveWebM(ctx, j, engine, video, selection, queueIndex, outputIndex, budget)
	}
	return s.transferAdaptiveMP4(ctx, j, engine, video, selection, queueIndex, outputIndex, budget)
}

func (s *server) noteAdaptiveFallback(j *jobState) {
	s.mu.Lock()
	if !strings.Contains(j.Note, adaptiveFallbackNote) {
		j.Note += " " + adaptiveFallbackNote
	}
	s.mu.Unlock()
}

func (s *server) noteAdaptiveMP4AudioFallback(j *jobState) {
	s.mu.Lock()
	if !strings.Contains(j.Note, adaptiveMP4AudioFallbackNote) {
		j.Note += " " + adaptiveMP4AudioFallbackNote
	}
	s.mu.Unlock()
}

func (s *server) noteExtractionFallback(j *jobState) {
	s.mu.Lock()
	if !strings.Contains(j.Note, extractionFallbackNote) {
		j.Note += " " + extractionFallbackNote
	}
	s.mu.Unlock()
}

func (s *server) noteBrowserAdaptive(j *jobState) {
	s.mu.Lock()
	if !strings.Contains(j.Note, browserAdaptiveNote) {
		j.Note += " " + browserAdaptiveNote
	}
	s.mu.Unlock()
}

func (s *server) noteCodecPreferenceFallback(j *jobState, strategy string) {
	note := ""
	switch strategy {
	case "vp9":
		note = vp9PreferenceFallbackNote
	case "av1":
		note = av1PreferenceFallbackNote
	default:
		return
	}
	s.mu.Lock()
	if !strings.Contains(j.Note, note) {
		j.Note += " " + note
	}
	s.mu.Unlock()
}

func downloadAdaptiveMP4Audio(preferred, progressive *youtube.Format, download func(*youtube.Format) error) (used *youtube.Format, usedFallback bool, err error) {
	if preferred == nil || download == nil {
		return nil, false, errNoAudio
	}
	if preferred.ContentLength <= 0 {
		if progressive == nil {
			return nil, false, errLimit
		}
		if err := download(progressive); err != nil {
			return nil, false, err
		}
		return progressive, true, nil
	}
	if err := download(preferred); err == nil {
		return preferred, false, nil
	} else if !errors.Is(err, errRead) || progressive == nil || progressive.ItagNo == preferred.ItagNo {
		return nil, false, err
	}
	if err := download(progressive); err != nil {
		return nil, false, err
	}
	return progressive, true, nil
}

func (s *server) transferAdaptiveMP4(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, selection streamSelection, queueIndex, outputIndex int, budget int64) (result mediaFile, err error) {
	if selection.video == nil || selection.audio == nil || budget <= 0 {
		return result, errLimit
	}
	result = mediaFile{
		ID: randomID(16), Name: fmt.Sprintf("%06d-%s.mp4", outputIndex, video.ID),
		Height: selection.video.Height, MimeType: "video/mp4", naming: formatNamingValues(selection.video),
	}
	path := filepath.Join(j.dir, result.Name)
	_ = os.Remove(path)
	videoPart := path + ".video.part"
	audioPart := path + ".audio.part"
	outputPart := path + ".part"
	finalized := false
	defer func() {
		for _, temporary := range []string{audioPart, outputPart} {
			if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
				err = errStorage
			}
		}
		if !s.keepPartial(j) {
			if removeErr := os.Remove(videoPart); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
				err = errStorage
			}
		}
		if !finalized {
			_ = os.Remove(path)
		}
	}()
	var browserProvider browserMediaProvider
	if s.browserFactory != nil && !s.bandwidthLimited() {
		if provider, providerErr := s.browserFactory(ctx); providerErr == nil && provider != nil {
			defer provider.Close()
			browserProvider = provider
		}
	}

	audioExpected := selection.audio.ContentLength
	if selection.progressive != nil && selection.progressive.ContentLength > audioExpected {
		audioExpected = selection.progressive.ContentLength
	}
	totalExpected := selection.video.ContentLength + audioExpected
	progressTotal := int64(float64(totalExpected) * 100 / 90)
	var downloaded int64
	progress := func(current int64) {
		if totalExpected <= 0 {
			return
		}
		s.updateProgress(j, queueIndex, downloaded+current, progressTotal)
	}
	videoSize, browserUsed, err := s.downloadAdaptiveRanges(ctx, j, engine, video, selection.video, videoPart, "video", queueIndex, budget, progress, browserProvider)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, errRead) && adaptiveFallbackAllowed(j, selection) {
			s.noteAdaptiveFallback(j)
			fallback, fallbackErr := s.transferProgressive(ctx, j, engine, video, selection.progressiveFallback, "video/mp4", queueIndex, outputIndex, budget)
			if fallbackErr == nil {
				finalized = true
			}
			return fallback, fallbackErr
		}
		return result, err
	}
	if browserUsed {
		s.noteBrowserAdaptive(j)
	}
	downloaded = videoSize
	if budget-videoSize <= 0 {
		return result, errLimit
	}
	// Prefer the selected highest-quality adaptive AAC track. The downloader
	// tries browser capture then native ranges and validates per-track identity.
	// Only a retryable read failure downgrades to the progressive AAC source.
	var audioSize int64
	var audioBrowserUsed bool
	actualAudio, usedFallback, err := downloadAdaptiveMP4Audio(selection.audio, selection.progressive, func(format *youtube.Format) error {
		size, usedBrowser, downloadErr := s.downloadAdaptiveRanges(ctx, j, engine, video, format, audioPart, "audio", queueIndex, budget-videoSize, progress, browserProvider)
		if downloadErr == nil {
			audioSize, audioBrowserUsed = size, usedBrowser
		}
		return downloadErr
	})
	if err != nil {
		return result, err
	}
	if usedFallback {
		s.noteAdaptiveMP4AudioFallback(j)
	}
	if audioBrowserUsed {
		s.noteBrowserAdaptive(j)
	}
	downloaded += audioSize
	if err := muxMP4(ctx, videoPart, audioPart, outputPart, selection.video, actualAudio); err != nil {
		if errors.Is(err, errStorage) {
			s.recordPersistenceFailure("finalize MP4 output", j.ID, err)
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, errMux
	}
	info, err := os.Stat(outputPart)
	if err != nil || info.Size() <= 0 {
		return result, errLength
	}
	if info.Size() > budget {
		return result, errLimit
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if totalExpected > 0 {
		s.updateProgress(j, queueIndex, progressTotal, progressTotal)
	}
	if durableRename(outputPart, path) != nil {
		return result, errStorage
	}
	if err := s.deleteDownloadPart(j, queueIndex, "video"); err != nil {
		return result, errStorage
	}
	if err := s.deleteDownloadPart(j, queueIndex, "audio"); err != nil {
		return result, errStorage
	}
	result.Size = info.Size()
	finalized = true
	return result, nil
}

func (s *server) transferAdaptiveWebM(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, selection streamSelection, queueIndex, outputIndex int, budget int64) (result mediaFile, err error) {
	if selection.video == nil || selection.audio == nil || budget <= 0 {
		return result, errLimit
	}
	result = mediaFile{
		ID: randomID(16), Name: fmt.Sprintf("%06d-%s.webm", outputIndex, video.ID),
		Height: selection.video.Height, MimeType: "video/webm", naming: formatNamingValues(selection.video),
	}
	path := filepath.Join(j.dir, result.Name)
	_ = os.Remove(path)
	videoPart := path + ".video.part"
	audioPart := path + ".audio.part"
	outputPart := path + ".part"
	transferStarted := time.Now()
	var capturePhase, muxPhase time.Duration
	defer func() {
		if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
			log.Printf("download metrics mode=webm-transfer elapsed=%s capture=%s mux=%s output_bytes=%d err=%v", time.Since(transferStarted).Round(time.Millisecond), capturePhase.Round(time.Millisecond), muxPhase.Round(time.Millisecond), result.Size, err)
		}
	}()
	finalized := false
	defer func() {
		if removeErr := os.Remove(outputPart); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
			err = errStorage
		}
		if !s.keepPartial(j) {
			for _, temporary := range []string{videoPart, audioPart} {
				if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && err == nil {
					err = errStorage
				}
			}
		}
		if !finalized {
			_ = os.Remove(path)
		}
	}()

	var browserProvider browserMediaProvider
	if s.browserFactory != nil && !s.bandwidthLimited() {
		if provider, providerErr := s.browserFactory(ctx); providerErr == nil && provider != nil {
			defer provider.Close()
			browserProvider = provider
		}
	}
	totalExpected := selection.video.ContentLength + selection.audio.ContentLength
	progressTotal := int64(float64(totalExpected) * 100 / 90)
	var downloaded int64
	progress := func(current int64) {
		if totalExpected <= 0 {
			return
		}
		s.updateProgress(j, queueIndex, downloaded+current, progressTotal)
	}

	fallback := func(cause error) (mediaFile, error) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if !adaptiveFallbackAllowed(j, selection) {
			return result, cause
		}
		s.noteAdaptiveFallback(j)
		return s.transferProgressive(ctx, j, engine, video, selection.progressiveFallback, "video/mp4", queueIndex, outputIndex, budget)
	}

	var videoSize, audioSize int64
	var videoBrowserUsed, audioBrowserUsed bool
	dualUsed := false
	dualAttempted := false
	if dualProvider, ok := browserProvider.(browserDualTrackProvider); ok {
		_, videoPartErr := os.Stat(videoPart)
		_, audioPartErr := os.Stat(audioPart)
		if errors.Is(videoPartErr, os.ErrNotExist) && errors.Is(audioPartErr, os.ErrNotExist) {
			dualAttempted = true
			if err := s.saveDownloadPart(j, video.ID, queueIndex, "video", videoPart, "browser-sabr", selection.video, 0, nativeProfileID(engine)); err != nil {
				return result, errStorage
			}
			if err := s.saveDownloadPart(j, video.ID, queueIndex, "audio", audioPart, "browser-sabr", selection.audio, 0, nativeProfileID(engine)); err != nil {
				return result, errStorage
			}
			dualProgress := func(videoCurrent, audioCurrent int64) {
				if totalExpected > 0 {
					s.updateProgress(j, queueIndex, videoCurrent+audioCurrent, progressTotal)
				}
			}
			captureStarted := time.Now()
			// A full dual capture already consumes its byte-aware deadline. Replaying
			// the same adaptive session a second time only delays a definitive error.
			const dualCaptureAttempts = 1
			for attempt := 0; attempt < dualCaptureAttempts; attempt++ {
				videoCandidates := browserWebMVideoCandidates(video, selection.video)
				videoSize, audioSize, err = dualProvider.CaptureTracks(ctx, video.ID, selection.video, videoCandidates, selection.audio, videoPart, audioPart, budget, budget, dualProgress)
				if err == nil && videoSize > 0 && audioSize > 0 && videoSize <= budget && audioSize <= budget-videoSize {
					videoInfo, videoStatErr := os.Stat(videoPart)
					audioInfo, audioStatErr := os.Stat(audioPart)
					if videoStatErr == nil && audioStatErr == nil && videoInfo.Size() == videoSize && audioInfo.Size() == audioSize {
						if err := s.deleteDownloadPart(j, queueIndex, "video"); err != nil {
							return result, errStorage
						}
						if err := s.deleteDownloadPart(j, queueIndex, "audio"); err != nil {
							return result, errStorage
						}
						dualUsed = true
						videoBrowserUsed, audioBrowserUsed = true, true
						break
					}
				}
				_ = os.Remove(videoPart)
				_ = os.Remove(audioPart)
				_ = s.deleteDownloadPart(j, queueIndex, "video")
				_ = s.deleteDownloadPart(j, queueIndex, "audio")
			}
			capturePhase += time.Since(captureStarted)
		}
	}
	if !dualUsed {
		captureStarted := time.Now()
		rangeBrowserProvider := browserProvider
		if dualAttempted {
			// A dual browser capture has already spent its bounded playback attempt.
			// Retry through native ranges now instead of replaying the same browser
			// session serially for video and audio.
			rangeBrowserProvider = nil
		}
		videoFormats := []*youtube.Format{selection.video}
		for _, candidate := range browserWebMVideoCandidates(video, selection.video) {
			if candidate != nil && candidate.ItagNo != selection.video.ItagNo {
				videoFormats = append(videoFormats, candidate)
			}
		}
		for formatIndex, videoFormat := range videoFormats {
			if formatIndex > 0 {
				// A partial representation cannot be resumed as another itag.
				_ = os.Remove(videoPart)
				if checkpointErr := s.deleteDownloadPart(j, queueIndex, "video"); checkpointErr != nil {
					return result, errStorage
				}
			}
			videoSize, videoBrowserUsed, err = s.downloadAdaptiveRanges(ctx, j, engine, video, videoFormat, videoPart, "video", queueIndex, budget, progress, rangeBrowserProvider)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
		}
		capturePhase += time.Since(captureStarted)
		if err != nil {
			if errors.Is(err, errRead) {
				return fallback(err)
			}
			return result, err
		}
		downloaded = videoSize
		if budget-videoSize <= 0 {
			return result, errLimit
		}
		captureStarted = time.Now()
		audioSize, audioBrowserUsed, err = s.downloadAdaptiveRanges(ctx, j, engine, video, selection.audio, audioPart, "audio", queueIndex, budget-videoSize, progress, rangeBrowserProvider)
		capturePhase += time.Since(captureStarted)
		if err != nil {
			if errors.Is(err, errRead) {
				return fallback(err)
			}
			return result, err
		}
	}
	if videoBrowserUsed || audioBrowserUsed {
		s.noteBrowserAdaptive(j)
	}
	downloaded = videoSize + audioSize
	muxStarted := time.Now()
	if err := muxWebMForDimensions(ctx, videoPart, audioPart, outputPart, selection.video.Width, selection.video.Height); err != nil {
		muxPhase += time.Since(muxStarted)
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return fallback(err)
	}
	muxPhase += time.Since(muxStarted)
	info, err := os.Stat(outputPart)
	if err != nil || info.Size() <= 0 {
		return result, errLength
	}
	if info.Size() > budget {
		return result, errLimit
	}
	if totalExpected > 0 {
		s.updateProgress(j, queueIndex, progressTotal, progressTotal)
	}
	if durableRename(outputPart, path) != nil {
		return result, errStorage
	}
	if err := s.deleteDownloadPart(j, queueIndex, "video"); err != nil {
		return result, errStorage
	}
	if err := s.deleteDownloadPart(j, queueIndex, "audio"); err != nil {
		return result, errStorage
	}
	result.Size = info.Size()
	finalized = true
	return result, nil
}

func adaptiveFallbackAllowed(j *jobState, selection streamSelection) bool {
	return j != nil && j.Allow360pFallback && selection.progressiveFallback != nil
}

const (
	adaptiveRangeChunkSize int64 = 8 * 1024 * 1024
	adaptiveRangeAttempts        = 3
)

func adaptiveRangeRetryDelay(retry int) time.Duration {
	if retry < 1 {
		return 0
	}
	base := 250 * time.Millisecond * time.Duration(1<<min(retry-1, 4))
	return base + time.Duration(rand.Int64N(int64(base)))
}

func validRangeResponse(response *http.Response, start, end, total int64) bool {
	if response.StatusCode == http.StatusOK {
		// Some servers ignore Range and return the entire source. Accept that
		// only for the initial request, where byte zero still matches the
		// requested offset; never append a full response at a resume offset.
		return start == 0
	}
	if response.StatusCode != http.StatusPartialContent {
		return false
	}
	var actualStart, actualEnd, actualTotal int64
	if _, err := fmt.Sscanf(response.Header.Get("Content-Range"), "bytes %d-%d/%d", &actualStart, &actualEnd, &actualTotal); err != nil {
		return false
	}
	return actualStart == start && actualEnd == end && actualTotal == total
}

func waitAdaptiveRangeRetry(ctx context.Context, retry int) error {
	delay := adaptiveRangeRetryDelay(retry)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nativeProfileID(engine nativeClient) string {
	if provider, ok := engine.(interface{ RequestProfile() youtubeClientProfile }); ok {
		return provider.RequestProfile().id
	}
	return ""
}

func downloadSourceFingerprint(videoID string, format *youtube.Format, profileIDs ...string) string {
	profileID := ""
	if len(profileIDs) > 0 {
		profileID = profileIDs[0]
	}
	identity := fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%s\x00%d\x00%s\x00%s\x00%d\x00%s\x00%s",
		videoID, format.ItagNo, format.MimeType, format.Quality, format.ContentLength,
		format.Width, format.Height, format.FPS, format.LastModified, format.Bitrate, format.AudioQuality,
		format.AudioSampleRate, format.AudioChannels, format.ProjectionType, profileID)
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func (s *server) saveDownloadPart(j *jobState, videoID string, itemIndex int, partKey, path, method string, format *youtube.Format, completed int64, profileIDs ...string) error {
	if s.store == nil {
		return nil
	}
	err := s.store.savePart(j.ID, itemIndex, partKey, downloadPart{
		Path: path, CompletedBytes: completed, ExpectedBytes: format.ContentLength,
		Itag: format.ItagNo, SourceFingerprint: downloadSourceFingerprint(videoID, format, profileIDs...), Method: method,
	})
	if err == nil {
		s.recordPersistenceSuccess()
	}
	return err
}

func (s *server) deleteDownloadPart(j *jobState, itemIndex int, partKey string) error {
	if s.store == nil {
		return nil
	}
	err := s.store.deletePart(j.ID, itemIndex, partKey)
	if err == nil {
		s.recordPersistenceSuccess()
	}
	return err
}

func (s *server) downloadAdaptiveRanges(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, path, partKey string, itemIndex int, budget int64, progress func(int64), browserProvider browserMediaProvider) (result int64, browserUsed bool, err error) {
	if budget <= 0 || format.ContentLength <= 0 || format.ContentLength > budget {
		return 0, false, errLimit
	}
	if progress == nil {
		progress = func(int64) {}
	}
	start := int64(0)
	profileID := nativeProfileID(engine)
	fingerprint := downloadSourceFingerprint(video.ID, format, profileID)
	info, statErr := os.Lstat(path)
	if statErr == nil {
		valid := false
		if s.store != nil {
			checkpoint, checkpointErr := s.store.loadPart(j.ID, itemIndex, partKey)
			if checkpointErr == nil && checkpoint.Method == "native-range" && checkpoint.Path == path &&
				checkpoint.Itag == format.ItagNo && checkpoint.ExpectedBytes == format.ContentLength &&
				checkpoint.SourceFingerprint == fingerprint && checkpoint.CompletedBytes >= 0 &&
				checkpoint.CompletedBytes <= format.ContentLength && info.Mode().IsRegular() &&
				info.Size() >= checkpoint.CompletedBytes && info.Size() <= format.ContentLength {
				file, openErr := os.OpenFile(path, os.O_WRONLY, 0600)
				if openErr == nil {
					truncateErr := file.Truncate(checkpoint.CompletedBytes)
					closeErr := file.Close()
					if truncateErr == nil && closeErr == nil {
						start = checkpoint.CompletedBytes
						valid = true
					}
				}
			} else if checkpointErr != nil && !errors.Is(checkpointErr, sql.ErrNoRows) {
				return 0, false, errStorage
			}
		}
		if !valid {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return 0, false, errStorage
			}
			if err := s.deleteDownloadPart(j, itemIndex, partKey); err != nil {
				return 0, false, errStorage
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return 0, false, errStorage
	} else if err := s.deleteDownloadPart(j, itemIndex, partKey); err != nil {
		return 0, false, errStorage
	}
	if browserProvider != nil && start == 0 {
		const browserCaptureAttempts = 2
		for attempt := 0; attempt < browserCaptureAttempts; attempt++ {
			if err := s.saveDownloadPart(j, video.ID, itemIndex, partKey, path, "browser-sabr", format, 0, profileID); err != nil {
				s.recordPersistenceFailure("save browser download checkpoint", j.ID, err)
				return 0, false, errStorage
			}
			captured, captureErr := browserProvider.CaptureTrack(ctx, video.ID, format, path, budget, progress)
			if captureErr == nil && captured == format.ContentLength && captured <= budget {
				if info, statErr := os.Stat(path); statErr == nil && info.Size() == captured {
					if err := s.deleteDownloadPart(j, itemIndex, partKey); err != nil {
						return 0, false, errStorage
					}
					return captured, true, nil
				}
			}
			_ = os.Remove(path)
			if err := s.deleteDownloadPart(j, itemIndex, partKey); err != nil {
				return 0, false, errStorage
			}
			if ctx.Err() != nil {
				return 0, false, ctx.Err()
			}
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return 0, false, errStorage
	}
	committed := false
	closed := false
	defer func() {
		if !closed {
			if closeErr := f.Close(); closeErr != nil {
				s.recordPersistenceFailure("close adaptive download part", j.ID, closeErr)
				if err == nil {
					err = errStorage
				}
			} else {
				s.recordPersistenceSuccess()
			}
		}
		if !committed && !s.keepPartial(j) {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, errStorage)
			}
			if checkpointErr := s.deleteDownloadPart(j, itemIndex, partKey); checkpointErr != nil {
				s.recordPersistenceFailure("delete download checkpoint", j.ID, checkpointErr)
				err = errors.Join(err, errStorage)
			}
		}
	}()
	client := s.rangeHTTPClient
	if client == nil {
		client = nativeHTTPClient(0)
	}
	current := format
	result = start
	if start > 0 {
		progress(start)
	}
	if err := s.saveDownloadPart(j, video.ID, itemIndex, partKey, path, "native-range", format, start, profileID); err != nil {
		s.recordPersistenceFailure("save download checkpoint", j.ID, err)
		return result, browserUsed, errStorage
	}
	for start < format.ContentLength {
		if err := ctx.Err(); err != nil {
			return result, browserUsed, err
		}
		end := min(start+adaptiveRangeChunkSize, format.ContentLength) - 1
		chunkSize := end - start + 1
		chunkOK := false
		for attempt := 0; attempt < adaptiveRangeAttempts; attempt++ {
			if attempt > 0 {
				if retryErr := waitAdaptiveRangeRetry(ctx, attempt); retryErr != nil {
					return result, browserUsed, retryErr
				}
			}
			if attempt > 0 || current == nil || current.URL == "" {
				if engine == nil {
					continue
				}
				previousProfile := nativeProfileID(engine)
				current, err = s.refreshFormat(ctx, engine, video.ID, format.ItagNo)
				if err != nil {
					if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
						log.Printf("adaptive range refresh failed itag=%d start=%d attempt=%d err=%v", format.ItagNo, start, attempt+1, err)
					}
					continue
				}
				profileID = nativeProfileID(engine)
				if profileID != previousProfile {
					return result, browserUsed, errRead
				}
				if downloadSourceFingerprint(video.ID, current, profileID) != fingerprint {
					return result, browserUsed, errLength
				}
			}
			parsedURL, err := url.Parse(current.URL)
			if err != nil {
				continue
			}
			query := parsedURL.Query()
			query.Set("range", fmt.Sprintf("%d-%d", start, end))
			parsedURL.RawQuery = query.Encode()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
			if err != nil {
				continue
			}
			profile := youtubeClientProfile{id: "android", client: youtube.AndroidClient, origin: "https://youtube.com"}
			if currentProfile, ok := engine.(interface{ RequestProfile() youtubeClientProfile }); ok {
				profile = currentProfile.RequestProfile()
			}
			req.Header.Set("User-Agent", profile.client.UserAgent)
			if profile.origin != "" {
				req.Header.Set("Origin", profile.origin)
			}
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			resp, requestErr := client.Do(req)
			if requestErr != nil {
				if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
					log.Printf("adaptive range request failed itag=%d start=%d attempt=%d err=%v", format.ItagNo, start, attempt+1, requestErr)
				}
				continue
			}
			if _, seekErr := f.Seek(start, io.SeekStart); seekErr != nil {
				_ = resp.Body.Close()
				s.recordPersistenceFailure("seek adaptive download part", j.ID, seekErr)
				return result, browserUsed, errStorage
			}
			if truncateErr := f.Truncate(start); truncateErr != nil {
				_ = resp.Body.Close()
				s.recordPersistenceFailure("truncate adaptive download part", j.ID, truncateErr)
				return result, browserUsed, errStorage
			}
			idleBody := idleDeadlineReader{reader: resp.Body, timeout: transferIdleTimeout, onIdle: func() { _ = resp.Body.Close() }}
			written, copyErr := io.Copy(f, io.LimitReader(s.bandwidthReader(ctx, idleBody), chunkSize+1))
			_ = resp.Body.Close()
			if copyErr == nil && written == chunkSize && validRangeResponse(resp, start, end, format.ContentLength) {
				chunkOK = true
				break
			}
			if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
				log.Printf("adaptive range incomplete itag=%d start=%d attempt=%d status=%d bytes=%d expected=%d err=%v", format.ItagNo, start, attempt+1, resp.StatusCode, written, chunkSize, copyErr)
			}
		}
		if !chunkOK {
			if ctx.Err() != nil {
				return result, browserUsed, ctx.Err()
			}
			return result, browserUsed, errRead
		}
		start = end + 1
		result = start
		progress(result)
		if err := f.Sync(); err != nil {
			s.recordPersistenceFailure("sync adaptive download checkpoint", j.ID, err)
			return result, browserUsed, errStorage
		}
		if checkpointErr := s.saveDownloadPart(j, video.ID, itemIndex, partKey, path, "native-range", format, result, profileID); checkpointErr != nil {
			s.recordPersistenceFailure("save download checkpoint", j.ID, checkpointErr)
			return result, browserUsed, errStorage
		}
	}
	if err := f.Sync(); err != nil {
		s.recordPersistenceFailure("sync adaptive download part", j.ID, err)
		return result, browserUsed, errStorage
	}
	if err := f.Close(); err != nil {
		closed = true
		s.recordPersistenceFailure("close adaptive download part", j.ID, err)
		return result, browserUsed, errStorage
	}
	closed = true
	s.recordPersistenceSuccess()
	committed = true
	return result, browserUsed, nil
}

func (s *server) refreshFormat(ctx context.Context, engine nativeClient, videoID string, itag int) (*youtube.Format, error) {
	video, err := engine.GetVideoContext(ctx, "https://www.youtube.com/watch?v="+videoID)
	if err != nil || video == nil {
		return nil, errMetadata
	}
	for i := range video.Formats {
		if video.Formats[i].ItagNo == itag && video.Formats[i].URL != "" {
			return &video.Formats[i], nil
		}
	}
	return nil, errMetadata
}

func (s *server) downloadSource(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, format *youtube.Format, path, partKey string, itemIndex int, budget int64, progress func(int64)) (result int64, err error) {
	if budget <= 0 || format.ContentLength > budget {
		return 0, errLimit
	}
	rangeFormat := s.resolveRangeFormat(ctx, engine, video, format)
	if rangeFormat.ContentLength > 0 && rangeFormat.URL != "" {
		result, _, err = s.downloadAdaptiveRanges(ctx, j, engine, video, rangeFormat, path, partKey, itemIndex, budget, progress, nil)
		return result, err
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return 0, errStorage
	}
	if err := s.deleteDownloadPart(j, itemIndex, partKey); err != nil {
		return 0, errStorage
	}
	stream, reported, streamErr := engine.GetStreamContext(ctx, video, format)
	if stream == nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, errRead
	}
	var once sync.Once
	var closeErr error
	closeStream := func() {
		once.Do(func() {
			defer func() {
				if recover() != nil {
					closeErr = errNative
				}
			}()
			closeErr = stream.Close()
		})
	}
	stopClose := context.AfterFunc(ctx, closeStream)
	defer func() { stopClose(); closeStream() }()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if streamErr != nil {
		return 0, errRead
	}
	expected := format.ContentLength
	if reported > 0 {
		if expected > 0 && reported != expected {
			return 0, errLength
		}
		expected = reported
	}
	if expected > budget {
		return 0, errLimit
	}
	if expected <= 0 {
		s.mu.Lock()
		if !strings.Contains(j.Note, unknownLengthNote) {
			j.Note += " " + unknownLengthNote
		}
		s.mu.Unlock()
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, errStorage
	}
	committed := false
	defer func() {
		_ = f.Close()
		if !committed {
			_ = os.Remove(path)
		}
	}()
	if progress == nil {
		progress = func(int64) {}
	}
	result, err = copyStream(ctx, f, s.bandwidthReader(ctx, idleDeadlineReader{reader: stream, timeout: transferIdleTimeout, onIdle: closeStream}), budget, expected, progress)
	closeStream()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, errRead
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err := f.Sync(); err != nil {
		return result, errStorage
	}
	if err := f.Close(); err != nil {
		return result, errStorage
	}
	committed = true
	return result, nil
}

func (s *server) resolveRangeFormat(ctx context.Context, engine nativeClient, video *youtube.Video, format *youtube.Format) *youtube.Format {
	if format == nil || format.URL != "" {
		return format
	}
	resolver, ok := engine.(interface {
		GetStreamURLContext(context.Context, *youtube.Video, *youtube.Format) (string, error)
	})
	if !ok {
		return format
	}
	streamURL, err := resolver.GetStreamURLContext(ctx, video, format)
	if err != nil || streamURL == "" {
		return format
	}
	resolved := *format
	resolved.URL = streamURL
	return &resolved
}

func muxMP4(ctx context.Context, videoPath, audioPath, outputPath string, videoFormat, audioFormat *youtube.Format) (muxErr error) {
	out, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("%w: create MP4 output: %v", errStorage, err)
	}
	closed := false
	defer func() {
		if !closed {
			if closeErr := out.Close(); closeErr != nil {
				muxErr = errors.Join(muxErr, fmt.Errorf("%w: close MP4 output: %v", errStorage, closeErr))
			}
		}
	}()
	muxer, err := mp4.CreateMp4Muxer(out)
	if err != nil {
		return err
	}
	videoTrack := muxer.AddVideoTrack(mp4.MP4_CODEC_H264, mp4.WithVideoWidth(uint32(videoFormat.Width)), mp4.WithVideoHeight(uint32(videoFormat.Height)))
	audioTrack := muxer.AddAudioTrack(mp4.MP4_CODEC_AAC, mp4.WithAudioChannelCount(uint8(audioFormat.AudioChannels)))
	if err := muxMP4Track(ctx, videoPath, muxer, videoTrack, mp4.MP4_CODEC_H264); err != nil {
		return err
	}
	if err := muxMP4Track(ctx, audioPath, muxer, audioTrack, mp4.MP4_CODEC_AAC); err != nil {
		return err
	}
	if err := muxer.WriteTrailer(); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("%w: sync MP4 output: %v", errStorage, err)
	}
	if err := out.Close(); err != nil {
		closed = true
		return fmt.Errorf("%w: close MP4 output: %v", errStorage, err)
	}
	closed = true
	return nil
}

func muxMP4Track(ctx context.Context, path string, muxer *mp4.Movmuxer, track uint32, codec mp4.MP4_CODEC_TYPE) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	demuxer := mp4.CreateMp4Demuxer(f)
	if _, err := demuxer.ReadHead(); err != nil {
		return err
	}
	written := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		packet, err := demuxer.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if packet == nil || packet.Cid != codec || len(packet.Data) == 0 {
			continue
		}
		if err := muxer.Write(track, packet.Data, packet.Pts, packet.Dts); err != nil {
			return err
		}
		written = true
	}
	if !written {
		return errMux
	}
	return nil
}

type idleDeadlineReader struct {
	reader  io.Reader
	timeout time.Duration
	onIdle  func()
}

func (r idleDeadlineReader) Read(buffer []byte) (int, error) {
	if r.timeout <= 0 || r.onIdle == nil {
		return r.reader.Read(buffer)
	}
	var timedOut atomic.Bool
	timerDone := make(chan struct{})
	timer := time.AfterFunc(r.timeout, func() {
		timedOut.Store(true)
		func() {
			defer func() { _ = recover() }()
			r.onIdle()
		}()
		close(timerDone)
	})
	n, err := r.reader.Read(buffer)
	if !timer.Stop() {
		<-timerDone
		if timedOut.Load() {
			return n, errRead
		}
	}
	return n, err
}

func copyStream(ctx context.Context, dst io.Writer, src io.Reader, budget, expected int64, progress func(int64)) (int64, error) {
	buffer := make([]byte, 64*1024)
	var written int64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		readSize := int64(len(buffer))
		if budget-written < readSize {
			readSize = budget - written + 1
		}
		n, readErr := src.Read(buffer[:int(readSize)])
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if n < 0 || n > int(readSize) {
			return written, errRead
		}
		if int64(n) > budget-written {
			return written, errLimit
		}
		if expected > 0 && int64(n) > expected-written {
			return written, errLength
		}
		if n > 0 {
			count, writeErr := dst.Write(buffer[:n])
			written += int64(count)
			if writeErr != nil || count != n {
				return written, errStorage
			}
			emptyReads = 0
			progress(written)
		} else {
			emptyReads++
		}
		if readErr == io.EOF {
			if written == 0 || (expected > 0 && written != expected) {
				return written, errLength
			}
			return written, ctx.Err()
		}
		if readErr != nil || emptyReads >= 100 {
			return written, errRead
		}
	}
}

const failedScratchRetention = 24 * time.Hour

// retentionAge distinguishes Library records from failed or cancelled jobs
// that never finalized a file. A zero configured retention keeps Library
// records indefinitely while still cleaning empty scratch jobs after a day.
func (s *server) retentionAge(j *jobState) (time.Duration, bool) {
	if j == nil || !terminal(j.Status) {
		return 0, false
	}
	if len(j.Files) == 0 && (j.Status == "failed" || j.Status == "cancelled") {
		if s.cfg.retain > 0 {
			return s.cfg.retain, true
		}
		return failedScratchRetention, true
	}
	if s.cfg.retain <= 0 || !publishedCopiesIntact(j) {
		return 0, false
	}
	return s.cfg.retain, true
}

// publishedCopiesIntact checks the actual destination before background
// cleanup removes any managed media or finalized caption sidecars.
func publishedCopiesIntact(j *jobState) bool {
	if len(j.Files) == 0 {
		return false
	}
	for _, file := range j.Files {
		if !file.PublishedAvailable || validateOutputCopy(j, file) != nil {
			return false
		}
		info, err := os.Lstat(file.OutputPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size {
			return false
		}
		if file.Subtitle != nil {
			subtitle := file.Subtitle
			if subtitle.ManagedAvailable && !subtitle.PublishedAvailable {
				return false
			}
			if subtitle.PublishedAvailable {
				if validateSubtitleOutputCopy(j, *subtitle) != nil {
					return false
				}
				subtitleInfo, err := os.Lstat(subtitle.OutputPath)
				if err != nil || !subtitleInfo.Mode().IsRegular() || subtitleInfo.Size() != subtitle.Size {
					return false
				}
			}
		}
	}
	return true
}

func (s *server) prune(now time.Time) {
	ids, err := s.store.loadRetentionCandidateIDs(now, s.cfg.retain)
	if err != nil {
		log.Printf("retention failure operation=load candidates: %v", err)
		return
	}
	s.mu.Lock()
	for id, t := range s.tickets {
		if !now.Before(t.expires) {
			delete(s.tickets, id)
		}
	}
	s.mu.Unlock()

	for _, id := range ids {
		s.mu.Lock()
		if s.jobs[id] != nil {
			s.mu.Unlock()
			continue
		}
		j, hydrated, err := s.hydrateTerminalJobLocked(id)
		if err != nil {
			s.mu.Unlock()
			log.Printf("retention failure operation=load job job=%s: %v", id, err)
			continue
		}
		if j == nil || !hydrated {
			s.mu.Unlock()
			continue
		}
		age, eligible := s.retentionAge(j)
		if !eligible || j.done.IsZero() || now.Sub(j.done) < age || j.readers > 0 || j.deleting {
			s.evictTerminalJobLocked(j)
			s.mu.Unlock()
			continue
		}
		held := false
		for _, ticket := range s.tickets {
			if ticket.jobID == id && now.Before(ticket.expires) {
				held = true
				break
			}
		}
		if held {
			s.evictTerminalJobLocked(j)
			s.mu.Unlock()
			continue
		}
		// Retention expires app-managed history and private media only.
		// Published output belongs to the user and must survive pruning.
		if err := removeManagedCopies(j); err != nil {
			s.evictTerminalJobLocked(j)
			s.mu.Unlock()
			log.Printf("retention failure operation=remove managed copies job=%s: %v", id, err)
			continue
		}
		j.deleting = true
		s.mu.Unlock()

		done := s.persistenceWriter.enqueue(func(store *jobStore) error {
			return store.deleteJob(id)
		})
		err := <-done
		s.mu.Lock()
		if err != nil {
			s.recordPersistenceFailure("retention delete job", id, err)
			j.deleting = false
			if saveErr := s.persistJobLocked(j); saveErr != nil {
				log.Printf("retention failure operation=save job job=%s: %v", id, saveErr)
			}
			s.evictTerminalJobLocked(j)
		} else {
			s.recordPersistenceSuccess()
			s.forgetJobLocked(j)
		}
		s.mu.Unlock()
	}
}
