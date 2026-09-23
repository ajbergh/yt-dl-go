//go:build e2e

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/kkdai/youtube/v2"
)

const e2eFixtureVideoID = "E2EVIDEO001"
const e2eFixtureBytes = 2 << 20

type e2eFixtureClient struct {
	streamCalls atomic.Int32
}

func (f *e2eFixtureClient) GetPlaylistContext(context.Context, string) (*youtube.Playlist, error) {
	return &youtube.Playlist{
		Title: "E2E Fixture Playlist",
		Videos: []*youtube.PlaylistEntry{
			{ID: e2eFixtureVideoID, Title: "E2E Fixture Video", Author: "E2E Fixture Channel", Duration: 2 * time.Minute},
		},
	}, nil
}

func (f *e2eFixtureClient) GetVideoContext(ctx context.Context, raw string) (*youtube.Video, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	id := parsed.Query().Get("v")
	if id == "" {
		id = e2eFixtureVideoID
	}
	return f.VideoFromPlaylistEntryContext(ctx, &youtube.PlaylistEntry{ID: id})
}

func (f *e2eFixtureClient) VideoFromPlaylistEntryContext(_ context.Context, entry *youtube.PlaylistEntry) (*youtube.Video, error) {
	if entry == nil || entry.ID != e2eFixtureVideoID {
		return nil, errors.New("unknown E2E fixture video")
	}
	return &youtube.Video{
		ID:       entry.ID,
		Title:    "E2E Fixture Video",
		Author:   "E2E Fixture Channel",
		Duration: 2 * time.Minute,
		Formats: youtube.FormatList{{
			ItagNo:        18,
			MimeType:      `video/mp4; codecs="avc1.42001E, mp4a.40.2"`,
			Height:        360,
			Width:         640,
			FPS:           30,
			AudioChannels: 2,
			ContentLength: e2eFixtureBytes,
		}},
	}, nil
}

type e2eSlowStream struct {
	ctx       context.Context
	remaining int
}

func (r *e2eSlowStream) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-time.After(12 * time.Millisecond):
	}
	n := len(p)
	if n > 8192 {
		n = 8192
	}
	if n > r.remaining {
		n = r.remaining
	}
	copy(p[:n], bytes.Repeat([]byte{0x2a}, n))
	r.remaining -= n
	return n, nil
}

func (r *e2eSlowStream) Close() error { return nil }

func (f *e2eFixtureClient) GetStreamContext(ctx context.Context, _ *youtube.Video, _ *youtube.Format) (io.ReadCloser, int64, error) {
	f.streamCalls.Add(1)
	return &e2eSlowStream{ctx: ctx, remaining: e2eFixtureBytes}, e2eFixtureBytes, nil
}

func configureE2EFixture(s *server) bool {
	if os.Getenv("YT_DL_GO_E2E_FIXTURE") != "1" {
		return false
	}
	s.engine = &e2eFixtureClient{}
	s.settings.DownloadLocation = filepath.Join(s.cfg.root, "published")
	_ = s.store.saveAppSettings(s.settings)
	s.thumbnailFetcher = func(context.Context, string, string) (string, error) {
		return "", errors.New("E2E fixture thumbnails are intentionally disabled")
	}
	return true
}
