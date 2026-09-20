// worker.go selects supported streams, downloads each job's exposed playlist
// items sequentially, records durable progress, and prunes expired job output.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
	"github.com/yapingcat/gomedia/go-mp4"
)

const formatNote = "Native Go engine: progressive MP4/WebM or remuxed MP4 streams with combined audio/video. Quality is a maximum, not a guarantee; HLS/DASH manifest and live sources are unsupported."
const playlistNote = "Playlist totals cover all entries exposed by YouTube, not independently verified hidden entries."
const unknownLengthNote = "An unknown-length stream is complete only at clean EOF; its original size cannot be independently verified."
const adaptiveFallbackNote = "YouTube rejected the adaptive HD stream; the highest verified progressive MP4 stream was downloaded instead."
const browserAdaptiveNote = "Adaptive HD media was streamed through a temporary browser session."

var (
	errMetadata = errors.New("Video metadata is unavailable or invalid")
	errPlaylist = errors.New("Playlist enumeration failed; completeness could not be verified")
	errCombined = errors.New("No compatible combined or adaptive MP4 stream fits the requested maximum height")
	errManifest = errors.New("HLS/DASH manifest or live sources are unsupported")
	errRead     = errors.New("Media stream could not be read completely")
	errLength   = errors.New("Media stream is empty or does not match its declared size")
	errMux      = errors.New("Video and audio streams could not be combined into a complete file")
	errStorage  = errors.New("Private media storage could not be safely written or cleaned")
	errLimit    = errors.New("Job storage limit reached; remaining entries were not downloaded")
	errNative   = errors.New("Native engine failed unexpectedly; completeness could not be verified")
)

type nativeClient interface {
	GetVideoContext(context.Context, string) (*youtube.Video, error)
	GetPlaylistContext(context.Context, string) (*youtube.Playlist, error)
	VideoFromPlaylistEntryContext(context.Context, *youtube.PlaylistEntry) (*youtube.Video, error)
	GetStreamContext(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error)
}

type streamSelection struct {
	video       *youtube.Format
	audio       *youtube.Format
	progressive *youtube.Format
	kind        string
}

func formatType(f *youtube.Format) (string, string, bool) {
	kind, params, err := mime.ParseMediaType(f.MimeType)
	if err != nil {
		return "", "", false
	}
	return kind, strings.ToLower(params["codecs"]), true
}

func betterVideoFormat(candidate, current *youtube.Format) bool {
	return current == nil || candidate.Height > current.Height ||
		(candidate.Height == current.Height && (candidate.FPS > current.FPS ||
			(candidate.FPS == current.FPS && candidate.Bitrate > current.Bitrate)))
}

func betterAudioFormat(candidate, current *youtube.Format) bool {
	return current == nil || candidate.Bitrate > current.Bitrate ||
		(candidate.Bitrate == current.Bitrate && candidate.ContentLength > current.ContentLength)
}

// selectFormat chooses a compatible stream under the requested maximum height.
// It prefers adaptive H.264/AAC only when its video outranks the best combined
// stream by height, frame rate, or bitrate; otherwise it uses the combined file.
func selectFormat(video *youtube.Video, quality string) (streamSelection, error) {
	if video == nil {
		return streamSelection{}, errMetadata
	}
	maxHeight := 0
	if quality != "best" {
		maxHeight, _ = strconv.Atoi(quality)
	}
	var progressive streamSelection
	var progressiveMP4Selection streamSelection
	for i := range video.Formats {
		f := &video.Formats[i]
		kind, _, ok := formatType(f)
		if !ok || (kind != "video/mp4" && kind != "video/webm") || f.AudioChannels <= 0 || f.Height <= 0 || f.Width <= 0 || f.InitRange != nil || f.IndexRange != nil || (maxHeight > 0 && f.Height > maxHeight) {
			continue
		}
		if betterVideoFormat(f, progressive.video) {
			progressive = streamSelection{video: f, kind: kind}
		}
		if kind == "video/mp4" && betterVideoFormat(f, progressiveMP4Selection.video) {
			progressiveMP4Selection = streamSelection{video: f, kind: kind}
		}
	}

	var audio *youtube.Format
	for i := range video.Formats {
		f := &video.Formats[i]
		kind, codecs, ok := formatType(f)
		if !ok || kind != "audio/mp4" || !strings.Contains(codecs, "mp4a") || f.AudioChannels <= 0 || f.Height != 0 || f.InitRange == nil || f.IndexRange == nil {
			continue
		}
		if betterAudioFormat(f, audio) {
			audio = f
		}
	}

	var adaptive streamSelection
	progressiveMP4 := progressiveMP4Selection.video
	if audio != nil && progressiveMP4 != nil {
		for i := range video.Formats {
			f := &video.Formats[i]
			kind, codecs, ok := formatType(f)
			if !ok || kind != "video/mp4" || !strings.Contains(codecs, "avc1") || f.AudioChannels != 0 || f.Height <= 0 || f.Width <= 0 || f.InitRange == nil || f.IndexRange == nil || (maxHeight > 0 && f.Height > maxHeight) {
				continue
			}
			if betterVideoFormat(f, adaptive.video) {
				adaptive = streamSelection{video: f, audio: audio, progressive: progressiveMP4, kind: kind}
			}
		}
	}

	if adaptive.video != nil && betterVideoFormat(adaptive.video, progressive.video) {
		return adaptive, nil
	}
	if progressive.video != nil {
		return progressive, nil
	}
	if adaptive.video != nil {
		return adaptive, nil
	}
	if video.HLSManifestURL != "" || video.DASHManifestURL != "" {
		return streamSelection{}, errManifest
	}
	return streamSelection{}, errCombined
}

// start launches the single sequential download worker and the periodic
// retention-pruning loop. Both stop when the server context is cancelled.
func (s *server) start() {
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.ctx.Done():
				return
			case id := <-s.queue:
				s.mu.Lock()
				j := s.jobs[id]
				if j == nil || j.Status != "queued" {
					s.mu.Unlock()
					continue
				}
				ctx, cancel := context.WithTimeout(s.ctx, s.cfg.timeout)
				j.Status, j.cancel = "downloading", cancel
				s.persistJobLocked(j)
				s.mu.Unlock()
				s.run(ctx, j)
				cancel()
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

// run processes one video or every playlist entry exposed by the upstream
// library, reuses validated finalized items, records per-item failures, and
// finalizes job status even if an unexpected panic occurs.
func (s *server) run(ctx context.Context, j *jobState) {
	var fatal error
	current := 0
	defer func() {
		if recover() != nil {
			fatal = errNative
			if current > 0 {
				s.recordFailure(j, current, errNative)
			}
		}
		s.finish(ctx, j, fatal)
	}()
	entries := []*youtube.PlaylistEntry{{ID: strings.TrimPrefix(j.URL, "https://www.youtube.com/watch?v=")}}
	if j.Kind == "playlist" {
		playlist, err := s.engine.GetPlaylistContext(ctx, j.URL)
		if playlist == nil {
			fatal = errPlaylist
			return
		}
		entries = playlist.Videos
		s.mu.Lock()
		total := len(entries)
		j.TotalCount = &total
		if playlist.Title != "" {
			j.Title = playlist.Title
		}
		s.mu.Unlock()
		if err != nil {
			fatal = errPlaylist
		}
	}
	var used int64
	s.mu.Lock()
	for _, file := range j.Files {
		used += file.Size
	}
	s.mu.Unlock()
	for index, entry := range entries {
		current = index + 1
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if completed, ok := j.fileItems[current]; ok {
			if s.validCompletedFile(j, completed) {
				j.CurrentItem = fmt.Sprintf("Item %d", current)
				j.Progress = floatPointer(100)
				s.mu.Unlock()
				continue
			}
			for fileIndex, file := range j.Files {
				if file.ID == completed.ID {
					j.Files = append(j.Files[:fileIndex], j.Files[fileIndex+1:]...)
					break
				}
			}
			delete(j.fileItems, current)
			j.CompletedCount = len(j.Files)
			s.persistJobLocked(j)
		}
		j.Progress, j.CurrentItem = nil, fmt.Sprintf("Item %d", current)
		if entry != nil && entry.Title != "" {
			j.CurrentItem = entry.Title
		}
		s.mu.Unlock()
		if entry == nil || !videoID.MatchString(entry.ID) {
			s.recordFailure(j, current, errMetadata)
			continue
		}
		var video *youtube.Video
		var err error
		if j.Kind == "video" {
			video, err = s.engine.GetVideoContext(ctx, j.URL)
		} else {
			video, err = s.engine.VideoFromPlaylistEntryContext(ctx, entry)
		}
		if err != nil || video == nil || video.ID != entry.ID {
			if ctx.Err() != nil {
				return
			}
			s.recordFailure(j, current, errMetadata)
			continue
		}
		s.mu.Lock()
		if video.Title != "" {
			j.CurrentItem = video.Title
			if j.Kind == "video" {
				j.Title = video.Title
			}
		}
		s.mu.Unlock()
		selection, err := selectFormat(video, j.Quality)
		var file mediaFile
		if err == nil {
			file, err = s.transfer(ctx, j, video, selection, current, s.cfg.maxBytes-used)
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.recordFailure(j, current, err)
			if errors.Is(err, errLimit) || errors.Is(err, errStorage) {
				fatal = err
				return
			}
			continue
		}
		applyVideoMetadata(&file, video)
		used += file.Size
		s.mu.Lock()
		j.Files = append(j.Files, file)
		j.fileItems[current] = file
		j.CompletedCount = len(j.Files)
		progress := 100.0
		j.Progress = &progress
		s.mu.Unlock()
		s.mu.Lock()
		s.persistJobLocked(j)
		s.mu.Unlock()
	}
}

func floatPointer(value float64) *float64 { return &value }

func (s *server) validCompletedFile(j *jobState, file mediaFile) bool {
	f, err := openFinal(j.dir, file.Name)
	if err != nil {
		return false
	}
	info, err := f.Stat()
	_ = f.Close()
	return err == nil && info.Size() == file.Size
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
	for _, safe := range []error{errMetadata, errPlaylist, errCombined, errManifest, errRead, errLength, errMux, errStorage, errLimit, errNative} {
		if errors.Is(err, safe) {
			message = safe.Error()
			break
		}
	}
	s.mu.Lock()
	j.Failures = append(j.Failures, itemFailure{Index: index, Error: message})
	s.persistJobLocked(j)
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
	case j.pauseRequested && !j.cancelRequested:
		j.Status, j.Error, j.done = "paused", "Paused. The current playlist item may restart when resumed.", time.Time{}
	case s.ctx.Err() != nil && !j.cancelRequested:
		j.Status, j.Error, j.done = "queued", "Download paused; it will resume when the service restarts", time.Time{}
	case j.cancelRequested:
		j.Status, j.Error = "cancelled", "Download cancelled; only finalized files are available"
		_ = s.store.deletePartsForJob(j.ID)
	case ctx.Err() != nil:
		j.Error = "Job timeout reached; remaining entries were not downloaded"
	case fatal != nil:
		j.Error = fatal.Error()
	case len(j.Failures) > 0:
		j.Error = fmt.Sprintf("%d entry/entries failed; first failure at item %d: %s", len(j.Failures), j.Failures[0].Index, j.Failures[0].Error)
	case j.TotalCount == nil || *j.TotalCount != len(j.Files) || len(j.Files) == 0:
		j.Error = "No complete output, or not every exposed entry was downloaded"
	default:
		j.Status, j.Error = "completed", ""
	}
	s.persistJobLocked(j)
}

func (s *server) transfer(ctx context.Context, j *jobState, video *youtube.Video, selection streamSelection, index int, budget int64) (mediaFile, error) {
	if selection.audio != nil {
		return s.transferAdaptive(ctx, j, video, selection, index, budget)
	}
	return s.transferProgressive(ctx, j, video, selection.video, selection.kind, index, budget)
}

func (s *server) transferProgressive(ctx context.Context, j *jobState, video *youtube.Video, format *youtube.Format, kind string, index int, budget int64) (result mediaFile, err error) {
	if budget <= 0 || format.ContentLength > budget {
		return result, errLimit
	}
	stream, reported, streamErr := s.engine.GetStreamContext(ctx, video, format)
	if stream == nil {
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
	result = mediaFile{ID: randomID(16), Name: fmt.Sprintf("%06d-%s.%s", index, video.ID, strings.TrimPrefix(kind, "video/")), Height: format.Height, MimeType: kind}
	path := filepath.Join(j.dir, result.Name)
	part := path + ".part"
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > 0 && (expected <= 0 || info.Size() == expected) {
		result.Size = info.Size()
		return result, nil
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
	result.Size, err = copyStream(ctx, f, stream, budget, expected, func(written int64) {
		if expected > 0 {
			value := min(100.0, 100*float64(written)/float64(expected))
			s.mu.Lock()
			j.Progress = &value
			s.mu.Unlock()
		}
	})
	closeStream()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, errRead
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if f.Sync() != nil || f.Close() != nil {
		return result, errStorage
	}
	if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
		return result, errStorage
	}
	if os.Rename(part, path) != nil {
		return result, errStorage
	}
	finalized = true
	return result, nil
}

func (s *server) transferAdaptive(ctx context.Context, j *jobState, video *youtube.Video, selection streamSelection, index int, budget int64) (result mediaFile, err error) {
	if selection.video == nil || selection.audio == nil || budget <= 0 {
		return result, errLimit
	}
	result = mediaFile{
		ID: randomID(16), Name: fmt.Sprintf("%06d-%s.mp4", index, video.ID),
		Height: selection.video.Height, MimeType: "video/mp4",
	}
	path := filepath.Join(j.dir, result.Name)
	if f, openErr := openFinal(j.dir, result.Name); openErr == nil {
		info, statErr := f.Stat()
		_ = f.Close()
		if statErr == nil {
			result.Size = info.Size()
			return result, nil
		}
	}
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
	if s.browserFactory != nil {
		if provider, providerErr := s.browserFactory(ctx); providerErr == nil && provider != nil {
			defer provider.Close()
			browserProvider = provider
		}
	}

	totalExpected := selection.video.ContentLength + selection.audio.ContentLength
	var downloaded int64
	progress := func(current int64) {
		if totalExpected <= 0 {
			return
		}
		value := min(90.0, 90*float64(downloaded+current)/float64(totalExpected))
		s.mu.Lock()
		j.Progress = &value
		s.mu.Unlock()
	}
	videoSize, browserUsed, err := s.downloadAdaptiveRanges(ctx, j, video, selection.video, videoPart, index, budget, progress, browserProvider)
	if err != nil {
		if errors.Is(err, errRead) && selection.progressive != nil {
			// YouTube may advertise adaptive formats while rejecting their later
			// byte ranges without a proof-of-origin token. Preserve a complete,
			// honest download rather than returning a corrupt or partial HD file.
			s.mu.Lock()
			if !strings.Contains(j.Note, adaptiveFallbackNote) {
				j.Note += " " + adaptiveFallbackNote
			}
			s.mu.Unlock()
			fallback, fallbackErr := s.transferProgressive(ctx, j, video, selection.progressive, "video/mp4", index, budget)
			if fallbackErr == nil {
				finalized = true
			}
			return fallback, fallbackErr
		}
		return result, err
	}
	if browserUsed {
		s.mu.Lock()
		if !strings.Contains(j.Note, browserAdaptiveNote) {
			j.Note += " " + browserAdaptiveNote
		}
		s.mu.Unlock()
	}
	downloaded = videoSize
	if budget-videoSize <= 0 {
		return result, errLimit
	}
	if selection.progressive == nil {
		return result, errCombined
	}
	// Adaptive AAC URLs for some videos are rejected after the first range.
	// The progressive MP4 is a reliable audio source; mux only its AAC track
	// with the higher-resolution adaptive video.
	audioSize, err := s.downloadSource(ctx, j, video, selection.progressive, audioPart, budget-videoSize, progress)
	if err != nil {
		return result, err
	}
	downloaded += audioSize
	if err := muxMP4(ctx, videoPart, audioPart, outputPart, selection.video, selection.audio); err != nil {
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
	value := 100.0
	s.mu.Lock()
	j.Progress = &value
	s.mu.Unlock()
	if os.Rename(outputPart, path) != nil {
		return result, errStorage
	}
	result.Size = info.Size()
	finalized = true
	return result, nil
}

const adaptiveRangeChunkSize int64 = 8 * 1024 * 1024

func (s *server) downloadAdaptiveRanges(ctx context.Context, j *jobState, video *youtube.Video, format *youtube.Format, path string, itemIndex int, budget int64, progress func(int64), browserProvider browserMediaProvider) (result int64, browserUsed bool, err error) {
	if budget <= 0 || format.ContentLength <= 0 || format.ContentLength > budget {
		return 0, false, errLimit
	}
	if progress == nil {
		progress = func(int64) {}
	}
	start := int64(0)
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > format.ContentLength {
			_ = os.Remove(path)
		} else {
			start = info.Size()
		}
	}
	if browserProvider != nil && start == 0 {
		captured, captureErr := browserProvider.CaptureTrack(ctx, video.ID, format, path, budget, progress)
		if captureErr == nil && captured > 0 && captured <= budget {
			if info, statErr := os.Stat(path); statErr == nil && info.Size() == captured {
				return captured, true, nil
			}
		}
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return 0, false, errStorage
	}
	committed := false
	defer func() {
		_ = f.Close()
		if !committed && !s.keepPartial(j) {
			_ = os.Remove(path)
		}
		if committed {
			s.store.deletePart(j.ID, itemIndex)
		}
	}()
	client := nativeHTTPClient(s.cfg.timeout)
	current := format
	result = start
	if start > 0 {
		progress(start)
		s.store.savePart(j.ID, itemIndex, path, start, format.ContentLength)
	}
	for start < format.ContentLength {
		end := min(start+adaptiveRangeChunkSize, format.ContentLength) - 1
		chunkSize := end - start + 1
		chunkOK := false
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 || current == nil || current.URL == "" {
				current, err = s.refreshFormat(ctx, video.ID, format.ItagNo)
				if err != nil {
					continue
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
			req.Header.Set("User-Agent", youtube.AndroidClient.UserAgent)
			req.Header.Set("Origin", "https://youtube.com")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			resp, requestErr := client.Do(req)
			if requestErr != nil {
				continue
			}
			_, _ = f.Seek(start, io.SeekStart)
			_ = f.Truncate(start)
			written, copyErr := io.Copy(f, io.LimitReader(resp.Body, chunkSize+1))
			_ = resp.Body.Close()
			if copyErr == nil && written == chunkSize && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
				chunkOK = true
				break
			}
		}
		if !chunkOK {
			return result, browserUsed, errRead
		}
		start = end + 1
		result = start
		progress(result)
		s.store.savePart(j.ID, itemIndex, path, result, format.ContentLength)
	}
	if err := f.Sync(); err != nil {
		return result, browserUsed, errStorage
	}
	if err := f.Close(); err != nil {
		return result, browserUsed, errStorage
	}
	committed = true
	return result, browserUsed, nil
}

func (s *server) refreshFormat(ctx context.Context, videoID string, itag int) (*youtube.Format, error) {
	video, err := s.engine.GetVideoContext(ctx, "https://www.youtube.com/watch?v="+videoID)
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

func (s *server) downloadSource(ctx context.Context, j *jobState, video *youtube.Video, format *youtube.Format, path string, budget int64, progress func(int64)) (result int64, err error) {
	if budget <= 0 || format.ContentLength > budget {
		return 0, errLimit
	}
	stream, reported, streamErr := s.engine.GetStreamContext(ctx, video, format)
	if stream == nil {
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
	result, err = copyStream(ctx, f, stream, budget, expected, progress)
	closeStream()
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

func muxMP4(ctx context.Context, videoPath, audioPath, outputPath string, videoFormat, audioFormat *youtube.Format) error {
	out, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
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
	return muxer.WriteTrailer()
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

func (s *server) prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := map[string]bool{}
	for id, t := range s.tickets {
		if !now.Before(t.expires) {
			delete(s.tickets, id)
		} else {
			held[t.jobID] = true
		}
	}
	keep := s.order[:0]
	for _, id := range s.order {
		j := s.jobs[id]
		if terminal(j.Status) && j.readers == 0 && !held[id] && now.Sub(j.done) >= s.cfg.retain {
			if os.RemoveAll(j.dir) == nil && s.store.deleteJob(id) == nil {
				delete(s.jobs, id)
				continue
			}
		}
		keep = append(keep, id)
	}
	s.order = keep
}
