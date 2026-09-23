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

func TestAdaptiveFallbackRequiresExplicitOptIn(t *testing.T) {
	selection := streamSelection{progressiveFallback: &youtube.Format{ItagNo: 18, Height: 360, AudioChannels: 2}}
	if adaptiveFallbackAllowed(&jobState{}, selection) {
		t.Fatal("360p fallback is enabled by default")
	}
	if !adaptiveFallbackAllowed(&jobState{Job: Job{Allow360pFallback: true}}, selection) {
		t.Fatal("explicit 360p fallback opt-in was ignored")
	}
	if adaptiveFallbackAllowed(&jobState{Job: Job{Allow360pFallback: true}}, streamSelection{}) {
		t.Fatal("fallback was allowed without a verified 360p progressive format")
	}
}

func TestBrowserWebMCandidatesStayWithinResolutionAndCodecFamily(t *testing.T) {
	var selected, alternate, lowerResolution, otherCodec, combined youtube.Format
	formats := []struct {
		target *youtube.Format
		raw    string
	}{
		{&selected, `{"itag":337,"mimeType":"video/webm; codecs=\"vp09.02.51.12\"","height":2160,"width":3840,"fps":60,"audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`},
		{&alternate, `{"itag":315,"mimeType":"video/webm; codecs=\"vp09.00.51.08\"","height":2160,"width":3840,"fps":30,"audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`},
		{&lowerResolution, `{"itag":308,"mimeType":"video/webm; codecs=\"vp09.00.41.08\"","height":1440,"width":2560,"fps":60,"audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`},
		{&otherCodec, `{"itag":401,"mimeType":"video/webm; codecs=\"av01.0.12M.08\"","height":2160,"width":3840,"fps":60,"audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`},
		{&combined, `{"itag":303,"mimeType":"video/webm; codecs=\"vp09.00.51.08, opus\"","height":2160,"width":3840,"fps":60,"audioChannels":2,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`},
	}
	video := fixtureVideo("dQw4w9WgXcQ")
	for _, item := range formats {
		if err := json.Unmarshal([]byte(item.raw), item.target); err != nil {
			t.Fatal(err)
		}
		video.Formats = append(video.Formats, *item.target)
	}
	selectedFormat := &video.Formats[len(video.Formats)-len(formats)]
	candidates := browserWebMVideoCandidates(video, selectedFormat)
	if len(candidates) != 2 || candidates[0].ItagNo != 337 || candidates[1].ItagNo != 315 {
		var got []int
		for _, candidate := range candidates {
			got = append(got, candidate.ItagNo)
		}
		t.Fatalf("browser candidate itags = %v, want [337 315]", got)
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
	if selection.progressiveFallback == nil || selection.progressiveFallback.Height > 360 || selection.progressiveFallback.AudioChannels <= 0 {
		t.Fatalf("adaptive selection has no verified 360p-or-lower progressive fallback: %+v", selection.progressiveFallback)
	}

	// High-resolution adaptive WebM uses VP9/AV1 video plus Opus audio while
	// keeping the existing H.264/AAC path available at the 1080p ceiling.
	high := fixtureVideo("dQw4w9WgXcQ")
	var h264, aac, vp9, av1, opus youtube.Format
	for target, raw := range map[*youtube.Format]string{
		&h264: `{"itag":299,"mimeType":"video/mp4; codecs=\"avc1.64002a\"","height":1080,"width":1920,"fps":60,"bitrate":5000000,"contentLength":"6000","audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`,
		&aac:  `{"itag":140,"mimeType":"audio/mp4; codecs=\"mp4a.40.2\"","audioChannels":2,"bitrate":128000,"contentLength":"1000","initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`,
		&vp9:  `{"itag":308,"mimeType":"video/webm; codecs=\"vp09.00.51.08\"","height":1440,"width":2560,"fps":60,"bitrate":9000000,"contentLength":"9000","audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`,
		&av1:  `{"itag":401,"mimeType":"video/webm; codecs=\"av01.0.12M.08\"","height":2160,"width":3840,"fps":60,"bitrate":12000000,"contentLength":"12000","audioChannels":0,"initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`,
		&opus: `{"itag":251,"mimeType":"audio/webm; codecs=\"opus\"","audioChannels":2,"bitrate":160000,"contentLength":"1200","initRange":{"start":"0","end":"1"},"indexRange":{"start":"0","end":"1"}}`,
	} {
		if err := json.Unmarshal([]byte(raw), target); err != nil {
			t.Fatal(err)
		}
	}
	high.Formats = append(high.Formats, h264, aac, vp9, av1, opus)

	compatibility, err := selectFormatForStrategy(high, "2160", "compatibility")
	if err != nil || compatibility.video == nil || compatibility.video.Height != 1080 {
		t.Fatalf("compatibility strategy selection = %+v %v", compatibility, err)
	}
	compatKind, compatCodecs, _ := formatType(compatibility.video)
	if compatKind != "video/mp4" || codecFamily(compatCodecs) != "h264" || compatibility.audio == nil {
		t.Fatalf("compatibility strategy emitted non-H.264/AAC MP4: %+v", compatibility)
	}

	vp9Preferred, err := selectFormatForStrategy(high, "2160", "vp9")
	if err != nil || vp9Preferred.video == nil || vp9Preferred.video.Height != 1440 || vp9Preferred.preferenceFallback != "" {
		t.Fatalf("VP9 preference selection = %+v %v", vp9Preferred, err)
	}
	_, vp9Codecs, _ := formatType(vp9Preferred.video)
	if codecFamily(vp9Codecs) != "vp9" {
		t.Fatalf("VP9 preference selected codecs %q", vp9Codecs)
	}

	av1Preferred, err := selectFormatForStrategy(high, "2160", "av1")
	if err != nil || av1Preferred.video == nil || av1Preferred.video.Height != 2160 || av1Preferred.preferenceFallback != "" {
		t.Fatalf("AV1 preference selection = %+v %v", av1Preferred, err)
	}
	_, av1Codecs, _ := formatType(av1Preferred.video)
	if codecFamily(av1Codecs) != "av1" {
		t.Fatalf("AV1 preference selected codecs %q", av1Codecs)
	}

	vp9Fallback, err := selectFormatForStrategy(high, "1080", "vp9")
	if err != nil || vp9Fallback.video == nil || vp9Fallback.video.Height != 1080 || vp9Fallback.preferenceFallback != "vp9" {
		t.Fatalf("VP9 preference fallback = %+v %v", vp9Fallback, err)
	}
	if _, err := selectFormatForStrategy(high, "2160", "hevc"); !errors.Is(err, errCombined) {
		t.Fatalf("invalid strategy error = %v", err)
	}

	// Best quality preserves the P2.4 safety invariant that adaptive MP4 only
	// participates in automatic selection when a progressive MP4 fallback is
	// available. Explicit Compatibility MP4 may still choose the adaptive pair.
	noMP4Fallback := fixtureVideo("dQw4w9WgXcQ")
	noMP4Fallback.Formats[0].MimeType = `video/webm; codecs="vp9, opus"`
	noMP4Fallback.Formats = append(noMP4Fallback.Formats, h264, aac)
	automaticWithoutFallback, err := selectFormatForStrategy(noMP4Fallback, "best", "best")
	if err != nil || automaticWithoutFallback.video == nil || automaticWithoutFallback.video.Height != noMP4Fallback.Formats[0].Height {
		t.Fatalf("automatic policy changed without progressive MP4 fallback: %+v %v", automaticWithoutFallback, err)
	}
	compatWithoutFallback, err := selectFormatForStrategy(noMP4Fallback, "best", "compatibility")
	if err != nil || compatWithoutFallback.video == nil || compatWithoutFallback.video.Height != 1080 || compatWithoutFallback.audio == nil {
		t.Fatalf("explicit compatibility strategy did not select adaptive MP4: %+v %v", compatWithoutFallback, err)
	}

	for quality, wantHeight := range map[string]int{"1080": 1080, "1440": 1440, "2160": 2160, "best": 2160} {
		selected, err := selectFormat(high, quality)
		if err != nil || selected.video == nil || selected.video.Height != wantHeight {
			t.Fatalf("high-res quality %s selected %+v: %v", quality, selected, err)
		}
		kind, codecs, _ := formatType(selected.video)
		if wantHeight <= 1080 {
			if kind != "video/mp4" || codecFamily(codecs) != "h264" || selected.audio == nil {
				t.Fatalf("1080 compatibility selection = %+v", selected)
			}
		} else if kind != "video/webm" || selected.audio == nil {
			t.Fatalf("high-res WebM selection = %+v", selected)
		} else if audioKind, audioCodecs, _ := formatType(selected.audio); audioKind != "audio/webm" || codecFamily(audioCodecs) != "opus" {
			t.Fatalf("high-res audio selection = %+v", selected.audio)
		}
	}

	// Prefer VP9 to AV1 when resolution and frame rate tie, even if AV1 has the
	// higher advertised bitrate; this keeps the default high-res path broadly
	// decodable without dropping AV1-only 4K support.
	tie := fixtureVideo("dQw4w9WgXcQ")
	vp9Tie, av1Tie := vp9, av1
	vp9Tie.Height, vp9Tie.Width, vp9Tie.Bitrate = 2160, 3840, 8_000_000
	av1Tie.Height, av1Tie.Width, av1Tie.Bitrate = 2160, 3840, 14_000_000
	tie.Formats = append(tie.Formats, vp9Tie, av1Tie, opus)
	tieSelection, err := selectFormat(tie, "2160")
	if err != nil || tieSelection.video == nil {
		t.Fatalf("equal-resolution WebM selection failed: %+v %v", tieSelection, err)
	}
	_, tieCodecs, _ := formatType(tieSelection.video)
	if codecFamily(tieCodecs) != "vp9" {
		t.Fatalf("equal-resolution high-res selection codec = %q, want VP9", tieCodecs)
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
	s := testServer(t, fake, func(c *config) { c.timeout = 30 * time.Second })
	j := waitJobFor(t, s, createJob(t, s, testPlaylist).ID, 45*time.Second, func(j Job) bool { return terminal(j.Status) })
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

func TestOriginalM4ABudgetUsesOnlySourceBytes(t *testing.T) {
	format := &youtube.Format{ContentLength: 4096}
	video := &youtube.Video{Duration: 5 * time.Minute}
	m4a := &jobState{Job: Job{MediaType: "audio", AudioFormat: "m4a"}}
	if got := estimatedItemBudget(m4a, video, format, streamSelection{}); got != format.ContentLength {
		t.Fatalf("M4A budget = %d, want source size %d", got, format.ContentLength)
	}
	mp3 := &jobState{Job: Job{MediaType: "audio", AudioFormat: "mp3", AudioBitrate: "192k"}}
	if got := estimatedItemBudget(mp3, video, format, streamSelection{}); got <= format.ContentLength {
		t.Fatalf("MP3 budget = %d, want source plus encoded output", got)
	}

	webMVideo := &youtube.Format{MimeType: `video/webm; codecs="vp09.00.51.08"`, ContentLength: 10_000}
	webMAudio := &youtube.Format{MimeType: `audio/webm; codecs="opus"`, ContentLength: 2_000}
	videoJob := &jobState{Job: Job{MediaType: "video"}}
	selection := streamSelection{video: webMVideo, audio: webMAudio, kind: "video/webm"}
	if got := estimatedItemBudget(videoJob, video, webMVideo, selection); got != 12_000+1024*1024 {
		t.Fatalf("WebM adaptive budget = %d, want source bytes plus mux headroom", got)
	}
}

type retryBrowserProvider struct {
	calls int
	data  []byte
}

func (provider *retryBrowserProvider) CaptureTrack(_ context.Context, _ string, _ *youtube.Format, path string, _ int64, progress func(int64)) (int64, error) {
	provider.calls++
	if provider.calls == 1 {
		return 0, errBrowserUnavailable
	}
	if err := os.WriteFile(path, provider.data, 0600); err != nil {
		return 0, err
	}
	if progress != nil {
		progress(int64(len(provider.data)))
	}
	return int64(len(provider.data)), nil
}

func (*retryBrowserProvider) Close() error { return nil }

func TestAdaptiveRangesRetriesBrowserCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "video.part")
	provider := &retryBrowserProvider{data: []byte("verified adaptive media")}
	format := &youtube.Format{ItagNo: 315, ContentLength: 1024}
	video := &youtube.Video{ID: "JapSnYBq3U8"}
	s := &server{}
	size, browserUsed, err := s.downloadAdaptiveRanges(context.Background(), &jobState{}, nil, video, format, path, 1, 2048, nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 || !browserUsed || size != int64(len(provider.data)) {
		t.Fatalf("browser retry calls=%d used=%t size=%d", provider.calls, browserUsed, size)
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
	if response := request(s, "PUT", "/api/settings", `{"defaultQuality":"best","maxConcurrentDownloads":1}`, nil); response.Code != 200 {
		t.Fatalf("limit concurrent downloads for cancellation fixture: %d %s", response.Code, response.Body.String())
	}
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
