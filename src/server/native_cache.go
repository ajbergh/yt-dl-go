package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
)

type metadataCacheEntry struct {
	body    []byte
	profile int
	expires time.Time
}
type metadataFlight struct {
	done  chan struct{}
	entry metadataCacheEntry
	err   error
}
type videoMetadataCache struct {
	mu      sync.Mutex
	entries map[string]metadataCacheEntry
	flights map[string]*metadataFlight
}

var sharedVideoMetadata = &videoMetadataCache{}

func metadataLifetime(video *youtube.Video, now time.Time) time.Duration {
	lifetime := 20 * time.Second
	for _, format := range video.Formats {
		parsed, err := url.Parse(format.URL)
		if err != nil {
			continue
		}
		if seconds, err := strconv.ParseInt(parsed.Query().Get("expire"), 10, 64); err == nil {
			remaining := time.Unix(seconds, 0).Sub(now) - 30*time.Second
			if remaining < lifetime {
				lifetime = remaining
			}
		}
	}
	return lifetime
}

func (cache *videoMetadataCache) load(ctx context.Context, key string, fetch func() (*youtube.Video, int, error)) (*youtube.Video, int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		cache.mu.Lock()
		if cache.entries == nil {
			cache.entries = make(map[string]metadataCacheEntry)
			cache.flights = make(map[string]*metadataFlight)
		}
		if entry, ok := cache.entries[key]; ok && time.Now().Before(entry.expires) {
			cache.mu.Unlock()
			var video youtube.Video
			if err := json.Unmarshal(entry.body, &video); err != nil {
				return nil, 0, err
			}
			return &video, entry.profile, nil
		}
		if flight := cache.flights[key]; flight != nil {
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-flight.done:
			}
			if errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
				continue
			}
			if flight.err != nil {
				return nil, 0, flight.err
			}
			var video youtube.Video
			if err := json.Unmarshal(flight.entry.body, &video); err != nil {
				return nil, 0, err
			}
			return &video, flight.entry.profile, nil
		}
		flight := &metadataFlight{done: make(chan struct{})}
		cache.flights[key] = flight
		cache.mu.Unlock()
		video, profile, err := fetch()
		entry := metadataCacheEntry{profile: profile}
		if err == nil && video != nil {
			entry.body, err = json.Marshal(video)
			entry.expires = time.Now().Add(metadataLifetime(video, time.Now()))
		} else if err == nil {
			err = errMetadata
		}
		cache.mu.Lock()
		if err == nil && len(entry.body) <= 1024*1024 && time.Now().Before(entry.expires) {
			if len(cache.entries) >= 32 {
				oldestKey := ""
				var oldest time.Time
				for k, v := range cache.entries {
					if oldestKey == "" || v.expires.Before(oldest) {
						oldestKey, oldest = k, v.expires
					}
				}
				delete(cache.entries, oldestKey)
			}
			cache.entries[key] = entry
		}
		flight.entry, flight.err = entry, err
		delete(cache.flights, key)
		close(flight.done)
		cache.mu.Unlock()
		if err != nil {
			return nil, profile, err
		}
		// Return a separate value so later format resolution cannot mutate cache.
		var cloned youtube.Video
		if err := json.Unmarshal(entry.body, &cloned); err != nil {
			return nil, profile, err
		}
		return &cloned, profile, nil
	}
}

func (e *youtubeExtractor) GetVideoContext(ctx context.Context, raw string) (*youtube.Video, error) {
	id, err := youtube.ExtractVideoID(raw)
	if err != nil {
		return nil, err
	}
	start := e.startingProfile()
	key := "kkdai-2.10.6:" + strconv.Itoa(start) + ":" + id
	video, profile, err := sharedVideoMetadata.load(ctx, key, func() (*youtube.Video, int, error) {
		video, err := e.getVideoUncached(ctx, raw)
		return video, e.startingProfile(), err
	})
	if err == nil {
		e.recordProfile(profile, start)
	}
	return video, err
}

// Transfers explicitly bypass the short metadata cache after a source failure.
func (e *youtubeExtractor) RefreshVideoContext(ctx context.Context, raw string) (*youtube.Video, error) {
	if id, err := youtube.ExtractVideoID(raw); err == nil {
		sharedVideoMetadata.mu.Lock()
		for key := range sharedVideoMetadata.entries {
			if strings.HasSuffix(key, ":"+id) {
				delete(sharedVideoMetadata.entries, key)
			}
		}
		sharedVideoMetadata.mu.Unlock()
	}
	return e.getVideoUncached(ctx, raw)
}

type playerAssetEntry struct {
	body    []byte
	headers http.Header
	expires time.Time
}
type playerAssetCache struct {
	mu      sync.Mutex
	entries map[string]playerAssetEntry
}

var sharedPlayerAssets = &playerAssetCache{entries: make(map[string]playerAssetEntry)}

type playerAssetTransport struct {
	base    http.RoundTripper
	cache   *playerAssetCache
	profile string
}

func publicPlayerAssetLifetime(headers http.Header, now time.Time) time.Duration {
	lifetime := 15 * time.Minute
	for _, value := range headers.Values("Cache-Control") {
		for _, directive := range strings.Split(value, ",") {
			parts := strings.SplitN(strings.TrimSpace(directive), "=", 2)
			switch strings.ToLower(parts[0]) {
			case "no-store", "no-cache", "private":
				return 0
			case "max-age":
				if len(parts) != 2 {
					return 0
				}
				seconds, err := strconv.ParseInt(strings.Trim(parts[1], "\" "), 10, 64)
				if err != nil || seconds <= 0 {
					return 0
				}
				// Compare before multiplying to avoid duration overflow.
				if seconds < int64(lifetime/time.Second) {
					lifetime = time.Duration(seconds) * time.Second
				}
			}
		}
	}
	if expires, err := http.ParseTime(headers.Get("Expires")); err == nil && expires.Sub(now) < lifetime {
		lifetime = expires.Sub(now)
	}
	if age, err := strconv.ParseInt(headers.Get("Age"), 10, 64); err == nil && age > 0 {
		if age >= int64(lifetime/time.Second) {
			return 0
		}
		lifetime -= time.Duration(age) * time.Second
	}
	return lifetime
}

func publicPlayerAssetRequest(request *http.Request) bool {
	if request.Method != http.MethodGet || (request.URL.Host != "youtube.com" && request.URL.Host != "www.youtube.com") || !strings.HasPrefix(request.URL.Path, "/s/player/") || !strings.HasSuffix(request.URL.Path, "/base.js") || request.Header.Get("Authorization") != "" {
		return false
	}
	for _, cookie := range request.Cookies() {
		if cookie.Name != "CONSENT" {
			return false
		}
	}
	return true
}

func (transport playerAssetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	eligible := publicPlayerAssetRequest(request)
	if !eligible {
		return transport.base.RoundTrip(request)
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	key := "kkdai-2.10.6:" + transport.profile + ":" + request.URL.String()
	transport.cache.mu.Lock()
	entry, ok := transport.cache.entries[key]
	transport.cache.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: entry.headers.Clone(), Body: io.NopCloser(bytes.NewReader(entry.body)), ContentLength: int64(len(entry.body)), Request: request}, nil
	}
	response, err := transport.base.RoundTrip(request)
	if err != nil || response.StatusCode != 200 || response.Header.Get("Set-Cookie") != "" {
		return response, err
	}
	lifetime := publicPlayerAssetLifetime(response.Header, time.Now())
	if lifetime <= 0 {
		return response, nil
	}
	const maximum = 4 * 1024 * 1024
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if readErr != nil {
		response.Body.Close()
		return nil, readErr
	}
	if len(body) > maximum {
		response.Body = &prefixResponseBody{Reader: io.MultiReader(bytes.NewReader(body), response.Body), closer: response.Body}
		return response, nil
	}
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(body))
	transport.cache.mu.Lock()
	if len(transport.cache.entries) >= 8 {
		for k := range transport.cache.entries {
			delete(transport.cache.entries, k)
			break
		}
	}
	transport.cache.entries[key] = playerAssetEntry{body: body, headers: response.Header.Clone(), expires: time.Now().Add(lifetime)}
	transport.cache.mu.Unlock()
	return response, nil
}

type prefixResponseBody struct {
	io.Reader
	closer io.Closer
}

func (body *prefixResponseBody) Close() error { return body.closer.Close() }
