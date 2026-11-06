package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestFormatIdentityDistinguishesAudioVariantsNotCredentials(t *testing.T) {
	var first youtube.Format
	if err := json.Unmarshal([]byte(`{"itag":140,"contentLength":"1000","audioTrack":{"id":"en.original","audioIsDefault":true}}`), &first); err != nil {
		t.Fatal(err)
	}
	first.URL = "https://rr.googlevideo.com/videoplayback?xtags=lang%3Den%3Adrc%3D0&pot=old"
	second := first
	second.URL = "https://rr.googlevideo.com/videoplayback?pot=new&xtags=lang%3Den%3Adrc%3D0"
	if downloadSourceFingerprint("video", &first) != downloadSourceFingerprint("video", &second) {
		t.Fatal("credentials changed media identity")
	}
	second.URL = "https://rr.googlevideo.com/videoplayback?xtags=lang%3Den%3Adrc%3D1"
	if downloadSourceFingerprint("video", &first) == downloadSourceFingerprint("video", &second) {
		t.Fatal("different DRC variants have the same identity")
	}
	second = first
	track := *first.AudioTrack
	track.ID = "es.dubbed"
	track.AudioIsDefault = false
	second.AudioTrack = &track
	if downloadSourceFingerprint("video", &first) == downloadSourceFingerprint("video", &second) {
		t.Fatal("different languages have the same identity")
	}
	second.Bitrate = first.Bitrate + 1000
	if betterAudioFormat(&second, &first) {
		t.Fatal("nondefault dubbed track replaced the requested default track")
	}
}

func TestNativePlayerResponseRetainsOmittedVariantFields(t *testing.T) {
	var first, second youtube.Format
	if err := json.Unmarshal([]byte(`{"itag":140,"isDrc":false,"xtags":"lang=en:acont=original"}`), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"itag":140,"isDrc":true,"xtags":"lang=en:acont=original"}`), &second); err != nil {
		t.Fatal(err)
	}
	if formatVariantKey(&first) == formatVariantKey(&second) {
		t.Fatal("omitted dependency fields erased DRC identity")
	}
}

func TestClientProfilesStayIsolatedAcrossConcurrentOperations(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "youtube", "player-response.html"))
	if err != nil {
		t.Fatal(err)
	}
	global := youtube.DefaultClient
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for _, profile := range supportedYouTubeProfiles[:2] {
		go func(profile youtubeClientProfile) {
			client := &youtube.Client{HTTPClient: &http.Client{Transport: cacheTestTransport(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("User-Agent") != profile.client.UserAgent {
					return nil, fmt.Errorf("request for %s used another profile", profile.id)
				}
				return (youtubeFixtureTransport{page: page}).RoundTrip(request)
			})}}
			_, err := withYouTubeProfileClient(profile, client, func(client *youtube.Client) (*youtube.Video, error) {
				ready <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return client.GetVideoContext(ctx, testVideo)
			})
			// The profile must also survive after the wrapper returns, when the
			// library's asynchronous stream workers can still be running.
			if err == nil {
				_, err = client.GetVideoContext(ctx, testVideo)
			}
			results <- err
		}(profile)
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("profile operations were serialized")
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if youtube.DefaultClient != global {
		t.Fatal("operation changed the global library profile")
	}
}

func TestMetadataCacheDeduplicatesAndReturnsIndependentValues(t *testing.T) {
	cache := &videoMetadataCache{}
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	fetch := func() (*youtube.Video, int, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return &youtube.Video{ID: "test", Formats: youtube.FormatList{{ItagNo: 140, MimeType: "audio/mp4"}}}, 2, nil
	}
	var wg sync.WaitGroup
	failures := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, p, err := cache.load(context.Background(), "key", fetch)
			if err != nil || v == nil || p != 2 {
				failures <- fmt.Errorf("profile=%d error=%v", p, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("fetched metadata %d times", calls.Load())
	}
	v, _, _ := cache.load(context.Background(), "key", fetch)
	v.Formats[0].MimeType = "changed"
	second, _, _ := cache.load(context.Background(), "key", fetch)
	if second.Formats[0].MimeType != "audio/mp4" {
		t.Fatal("cached metadata was mutated by a caller")
	}
	if metadataLifetime(&youtube.Video{Formats: youtube.FormatList{{URL: "https://media.invalid/?expire=1"}}}, time.Now()) > 0 {
		t.Fatal("expired URLs were cached")
	}
}

type cacheTestTransport func(*http.Request) (*http.Response, error)

func (transport cacheTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
func TestPlayerAssetCacheScopesProfilesAndPublicAssets(t *testing.T) {
	calls := 0
	cache := &playerAssetCache{entries: make(map[string]playerAssetEntry)}
	base := cacheTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString("player-js")), Request: r}, nil
	})
	transport := playerAssetTransport{base: base, cache: cache, profile: "web"}
	request, _ := http.NewRequest("GET", "https://youtube.com/s/player/version/player/base.js", nil)
	request.AddCookie(&http.Cookie{Name: "CONSENT", Value: "yes"})
	for range 2 {
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "player-js" {
			t.Fatal("cached body changed")
		}
	}
	if calls != 1 {
		t.Fatalf("public player fetched %d times", calls)
	}
	transport.profile = "android"
	response, _ := transport.RoundTrip(request)
	response.Body.Close()
	if calls != 2 {
		t.Fatal("player cache crossed profiles")
	}
	request.AddCookie(&http.Cookie{Name: "SESSION", Value: "private"})
	response, _ = transport.RoundTrip(request)
	response.Body.Close()
	if calls != 3 {
		t.Fatal("private request was served from public cache")
	}
}

func TestPlayerAssetCacheHonorsServerExpiry(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		control string
		want    time.Duration
	}{
		{"PUBLIC, MAX-AGE=30", 30 * time.Second},
		{"public, max-age=\"10\"", 10 * time.Second},
		{"No-Store", 0}, {"PRIVATE", 0}, {"max-age=0", 0}, {"max-age=invalid", 0},
		{"max-age=9223372036854775807", 15 * time.Minute},
	} {
		headers := make(http.Header)
		headers.Set("Cache-Control", test.control)
		if got := publicPlayerAssetLifetime(headers, now); got != test.want {
			t.Errorf("%q lifetime=%s want=%s", test.control, got, test.want)
		}
	}
}

func TestNativeRangesUseBoundedParallelWindowsAndOrderedOutput(t *testing.T) {
	payload := make([]byte, 4*adaptiveRangeChunkSize+123)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	var active, maximum, requests atomic.Int32
	barrier := make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); count > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, count) {
				break
			}
		}
		if requests.Add(1) == 4 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			http.Error(w, "ranges were serialized", 500)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, "range missing", 400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(206)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer fixture.Close()
	s := &server{rangeHTTPClient: fixture.Client()}
	path := filepath.Join(t.TempDir(), "media.part")
	format := &youtube.Format{ItagNo: 137, URL: fixture.URL, ContentLength: int64(len(payload))}
	size, _, err := s.downloadAdaptiveRanges(context.Background(), &jobState{}, nil, &youtube.Video{ID: "test"}, format, path, "video", 1, int64(len(payload)), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if size != int64(len(payload)) || !bytes.Equal(got, payload) || maximum.Load() != 4 {
		t.Fatalf("bytes=%d concurrency=%d", size, maximum.Load())
	}
}

func TestNativeRateLimitWaitHonorsCancellation(t *testing.T) {
	var calls atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s := &server{rangeHTTPClient: fixture.Client()}
	format := &youtube.Format{ItagNo: 137, URL: fixture.URL, ContentLength: 100}
	_, _, err := s.downloadAdaptiveRanges(ctx, &jobState{}, nil, &youtube.Video{ID: "test"}, format, filepath.Join(t.TempDir(), "part"), "video", 1, 100, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
	if shouldRefreshSlowRange(1000, time.Minute, 1024) {
		t.Fatal("configured bandwidth limit triggered throttling recovery")
	}
}

func TestAdaptivePairRejectsUnverifiedMP4AndEnforcesCombinedBudget(t *testing.T) {
	directory := t.TempDir()
	vp, ap := filepath.Join(directory, "video"), filepath.Join(directory, "audio")
	provider := &dualRetryFixture{capture: func(video, audio string) (int64, int64, error) {
		if err := os.WriteFile(video, []byte("video"), 0600); err != nil {
			return 0, 0, err
		}
		if err := os.WriteFile(audio, []byte("audio"), 0600); err != nil {
			return 0, 0, err
		}
		return 5, 5, nil
	}}
	selection := streamSelection{kind: "video/mp4", video: &youtube.Format{ItagNo: 137, ContentLength: 6}, audio: &youtube.Format{ItagNo: 140, ContentLength: 5}}
	_, _, used, err := (&server{}).captureAdaptivePair(context.Background(), &jobState{}, nil, &youtube.Video{ID: "test"}, selection, vp, ap, 1, 100, nil, provider)
	if used || !errors.Is(err, errLength) {
		t.Fatalf("unverified pair accepted: used=%t error=%v", used, err)
	}
	if _, err := os.Stat(vp); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed pair left video bytes")
	}
	_, _, _, err = (&server{}).captureAdaptivePair(context.Background(), &jobState{}, nil, &youtube.Video{}, selection, vp, ap, 1, 4, nil, provider)
	if !errors.Is(err, errItemReservation) {
		t.Fatal("combined item budget was ignored")
	}
}

type checkpointPairFixture struct {
	browserMediaProvider
	checkpoints sabrCheckpointIO
	cancel      context.CancelFunc
}

func (fixture *checkpointPairFixture) SetCheckpoints(checkpoints sabrCheckpointIO) {
	fixture.checkpoints = checkpoints
}
func (fixture *checkpointPairFixture) CaptureTracks(ctx context.Context, id string, video *youtube.Format, _ []*youtube.Format, audio *youtube.Format, videoPath, audioPath string, _ int64, _ int64, _ func(int64, int64)) (int64, int64, error) {
	for path, format := range map[string]*youtube.Format{videoPath: video, audioPath: audio} {
		data := []byte("init")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return 0, 0, err
		}
		hash := sha256.Sum256(data)
		state := sabrCheckpoint{VideoID: id, Itag: format.ItagNo, Bytes: 4, InitDigest: hash, Segments: []sabrSavedSegment{{Sequence: -1, End: 4, Hash: hash}}}
		if err := fixture.checkpoints.Save(path, format, state); err != nil {
			return 0, 0, err
		}
	}
	fixture.cancel()
	return 0, 0, ctx.Err()
}
func TestAdaptivePairRetainsVerifiedCheckpointsOnPause(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := &jobState{Job: Job{ID: "pair-pause", URL: testVideo, Kind: "video", Status: "downloading"}, pauseRequested: true}
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &checkpointPairFixture{cancel: cancel}
	dir := t.TempDir()
	vp, ap := filepath.Join(dir, "video"), filepath.Join(dir, "audio")
	selection := streamSelection{kind: "video/mp4", video: &youtube.Format{ItagNo: 137, ContentLength: 100}, audio: &youtube.Format{ItagNo: 140, ContentLength: 10}}
	_, _, _, err := s.captureAdaptivePair(ctx, j, nil, &youtube.Video{ID: "abcdefghijk"}, selection, vp, ap, 1, 200, nil, provider)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for key, path := range map[string]string{"video": vp, "audio": ap} {
		state, err := s.store.loadSABRCheckpoint(j.ID, 1, key)
		if err != nil || state == nil || state.Bytes != 4 {
			t.Fatalf("paused %s checkpoint was deleted: %v", key, err)
		}
		if info, err := os.Stat(path); err != nil || info.Size() != 4 {
			t.Fatalf("paused %s bytes were deleted: %v", key, err)
		}
	}
}
