package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
)

var configureYouTubeClientOnce sync.Once
var youtubeProfileMu sync.Mutex
var youtubeProfileProbeOnce sync.Once
var youtubeProfileProbe profileProbeResult
var errNoYouTubeProfiles = errors.New("no YouTube extraction profiles are configured")

type profileProbeResult struct {
	Ready    bool     `json:"ready"`
	Probe    string   `json:"probe"`
	Profiles []string `json:"profiles"`
}

type youtubeClientProfile struct {
	id     string
	client youtube.ClientInfo
	origin string
}

var supportedYouTubeProfiles = []youtubeClientProfile{
	{id: "android", client: youtube.AndroidClient, origin: "https://youtube.com"},
	{id: "ios", client: youtube.IOSClient, origin: "https://youtube.com"},
	{id: "embedded", client: youtube.EmbeddedClient, origin: "https://youtube.com"},
	{id: "web", client: youtube.WebClient, origin: "https://youtube.com"},
}

type youtubeExtractor struct {
	timeout  time.Duration
	mu       sync.RWMutex
	profile  int
	fallback bool
}

// newNativeClient selects the compatible YouTube client profile and applies
// the outbound-network guard and timeout used for metadata and direct streams.
func newNativeClient(timeout time.Duration) *youtube.Client {
	// v2.10.6 defaults to AndroidVRClient, whose range requests can be rejected
	// partway through otherwise valid progressive downloads. AndroidClient is
	// the supported profile for these downloads and avoids that failure mode.
	configureYouTubeClientOnce.Do(func() {
		youtube.DefaultClient = youtube.AndroidClient
	})
	initializeYouTubeProfileProbe()
	return &youtube.Client{HTTPClient: nativeHTTPClient(timeout), MaxRoutines: 4, ChunkSize: 1024 * 1024}
}

func initializeYouTubeProfileProbe() {
	youtubeProfileProbeOnce.Do(func() {
		result := profileProbeResult{Ready: len(supportedYouTubeProfiles) > 0, Probe: "local-profile-config"}
		for _, profile := range supportedYouTubeProfiles {
			if profile.id == "" || profile.client.Name == "" || profile.client.UserAgent == "" || profile.client.Key == "" || profile.origin == "" {
				result.Ready = false
				continue
			}
			result.Profiles = append(result.Profiles, profile.id)
		}
		if len(result.Profiles) != len(supportedYouTubeProfiles) {
			result.Ready = false
		}
		youtubeProfileProbe = result
	})
}

func youtubeExtractorHealth() profileProbeResult {
	initializeYouTubeProfileProbe()
	result := youtubeProfileProbe
	result.Profiles = append([]string(nil), result.Profiles...)
	return result
}

func newYouTubeExtractor(timeout time.Duration) *youtubeExtractor {
	configureYouTubeClientOnce.Do(func() {
		youtube.DefaultClient = youtube.AndroidClient
	})
	initializeYouTubeProfileProbe()
	return &youtubeExtractor{timeout: timeout}
}

func withYouTubeProfile[T any](profile youtubeClientProfile, timeout time.Duration, call func(*youtube.Client) (T, error)) (value T, err error) {
	client := &youtube.Client{HTTPClient: nativeHTTPClient(timeout), MaxRoutines: 4, ChunkSize: 1024 * 1024}
	return withYouTubeProfileClient(profile, client, call)
}

func withYouTubeProfileClient[T any](profile youtubeClientProfile, client *youtube.Client, call func(*youtube.Client) (T, error)) (value T, err error) {
	youtubeProfileMu.Lock()
	defer youtubeProfileMu.Unlock()
	configureYouTubeClientOnce.Do(func() {
		youtube.DefaultClient = youtube.AndroidClient
	})
	previous := youtube.DefaultClient
	youtube.DefaultClient = profile.client
	defer func() { youtube.DefaultClient = previous }()
	return call(client)
}

func canRetryYouTubeProfile(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, youtube.ErrInvalidCharactersInVideoID) || errors.Is(err, youtube.ErrVideoIDMinLength) ||
		errors.Is(err, youtube.ErrInvalidPlaylist) || errors.Is(err, youtube.ErrVideoPrivate) ||
		errors.Is(err, youtube.ErrLoginRequired) || errors.Is(err, youtube.ErrNotPlayableInEmbed) {
		return false
	}
	var playability *youtube.ErrPlayabiltyStatus
	if errors.As(err, &playability) {
		return false
	}
	var playlistStatus youtube.ErrPlaylistStatus
	if errors.As(err, &playlistStatus) {
		return false
	}
	var status youtube.ErrUnexpectedStatusCode
	if errors.As(err, &status) {
		code := int(status)
		return code == 429 || code >= 500
	}
	return true
}

func extractorProfileOrder(start int) []int {
	return extractorProfileOrderForCount(start, len(supportedYouTubeProfiles))
}

func extractorProfileOrderForCount(start, count int) []int {
	order := make([]int, 0, count)
	if count == 0 {
		return order
	}
	if start < 0 || start >= count {
		start = 0
	}
	for offset := range count {
		order = append(order, (start+offset)%count)
	}
	return order
}

func extractAcrossProfiles[T any](ctx context.Context, profiles []youtubeClientProfile, start int, call func(youtubeClientProfile) (T, error), valid func(T) bool) (T, int, error) {
	var zero T
	if len(profiles) == 0 {
		return zero, start, errNoYouTubeProfiles
	}
	order := extractorProfileOrderForCount(start, len(profiles))
	var lastErr error
	for _, index := range order {
		if err := ctx.Err(); err != nil {
			return zero, index, err
		}
		value, err := call(profiles[index])
		if err == nil && valid(value) {
			return value, index, nil
		}
		if err == nil {
			err = errMetadata
		}
		lastErr = err
		if !canRetryYouTubeProfile(err) {
			return zero, index, err
		}
	}
	return zero, start, lastErr
}

func (e *youtubeExtractor) startingProfile() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.profile
}

func (e *youtubeExtractor) recordProfile(index, start int) {
	e.mu.Lock()
	e.profile = index
	e.fallback = index != start
	e.mu.Unlock()
}

func (e *youtubeExtractor) FallbackUsed() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.fallback
}

func (e *youtubeExtractor) YouTubeProfile() youtube.ClientInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	index := e.profile
	if index < 0 || index >= len(supportedYouTubeProfiles) {
		index = 0
	}
	return supportedYouTubeProfiles[index].client
}

func (e *youtubeExtractor) RequestProfile() youtubeClientProfile {
	return e.currentProfile()
}

func (e *youtubeExtractor) currentProfile() youtubeClientProfile {
	e.mu.RLock()
	defer e.mu.RUnlock()
	index := e.profile
	if index < 0 || index >= len(supportedYouTubeProfiles) {
		index = 0
	}
	return supportedYouTubeProfiles[index]
}

func (e *youtubeExtractor) GetVideoContext(ctx context.Context, raw string) (*youtube.Video, error) {
	start := e.startingProfile()
	expectedID, idErr := youtube.ExtractVideoID(raw)
	if idErr != nil {
		return nil, idErr
	}
	video, selected, err := extractAcrossProfiles(ctx, supportedYouTubeProfiles, start, func(profile youtubeClientProfile) (*youtube.Video, error) {
		return withYouTubeProfile(profile, e.timeout, func(client *youtube.Client) (*youtube.Video, error) {
			return client.GetVideoContext(ctx, raw)
		})
	}, func(video *youtube.Video) bool { return video != nil && video.ID == expectedID })
	if err != nil {
		return nil, err
	}
	e.recordProfile(selected, start)
	return video, nil
}

func (e *youtubeExtractor) GetPlaylistContext(ctx context.Context, raw string) (*youtube.Playlist, error) {
	start := e.startingProfile()
	playlist, selected, err := extractAcrossProfiles(ctx, supportedYouTubeProfiles, start, func(profile youtubeClientProfile) (*youtube.Playlist, error) {
		return withYouTubeProfile(profile, e.timeout, func(client *youtube.Client) (*youtube.Playlist, error) {
			return client.GetPlaylistContext(ctx, raw)
		})
	}, func(playlist *youtube.Playlist) bool { return playlist != nil })
	if err != nil {
		return nil, err
	}
	e.recordProfile(selected, start)
	return playlist, nil
}

func (e *youtubeExtractor) VideoFromPlaylistEntryContext(ctx context.Context, entry *youtube.PlaylistEntry) (*youtube.Video, error) {
	if entry == nil || !videoID.MatchString(entry.ID) {
		return nil, errMetadata
	}
	start := e.startingProfile()
	video, selected, err := extractAcrossProfiles(ctx, supportedYouTubeProfiles, start, func(profile youtubeClientProfile) (*youtube.Video, error) {
		return withYouTubeProfile(profile, e.timeout, func(client *youtube.Client) (*youtube.Video, error) {
			return client.VideoFromPlaylistEntryContext(ctx, entry)
		})
	}, func(video *youtube.Video) bool { return video != nil && video.ID == entry.ID })
	if err != nil {
		return nil, err
	}
	e.recordProfile(selected, start)
	return video, nil
}

func (e *youtubeExtractor) GetStreamContext(ctx context.Context, video *youtube.Video, format *youtube.Format) (io.ReadCloser, int64, error) {
	type streamResult struct {
		reader io.ReadCloser
		length int64
	}
	result, err := withYouTubeProfile(e.currentProfile(), e.timeout, func(client *youtube.Client) (streamResult, error) {
		reader, length, err := client.GetStreamContext(ctx, video, format)
		return streamResult{reader: reader, length: length}, err
	})
	return result.reader, result.length, err
}

func (e *youtubeExtractor) GetStreamURLContext(ctx context.Context, video *youtube.Video, format *youtube.Format) (string, error) {
	return withYouTubeProfile(e.currentProfile(), e.timeout, func(client *youtube.Client) (string, error) {
		return client.GetStreamURLContext(ctx, video, format)
	})
}
