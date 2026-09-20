package main

import (
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
)

var configureYouTubeClientOnce sync.Once

// newNativeClient selects the compatible YouTube client profile and applies
// the outbound-network guard and timeout used for metadata and direct streams.
func newNativeClient(timeout time.Duration) *youtube.Client {
	// v2.10.6 defaults to AndroidVRClient, whose range requests can be rejected
	// partway through otherwise valid progressive downloads. AndroidClient is
	// the supported profile for these downloads and avoids that failure mode.
	configureYouTubeClientOnce.Do(func() {
		youtube.DefaultClient = youtube.AndroidClient
	})
	return &youtube.Client{HTTPClient: nativeHTTPClient(timeout), MaxRoutines: 1, ChunkSize: 1024 * 1024}
}
