package main

import (
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/kkdai/youtube/v2"
)

// Extended tags and audio-track IDs distinguish representations that can share
// an itag, byte length, and codec. Expiring request credentials are excluded.
func formatVariantKey(format *youtube.Format) string {
	var variant struct{ AudioTrack, XTags, DRC, VoiceBoost string }
	if format != nil {
		if format.AudioTrack != nil {
			variant.AudioTrack = format.AudioTrack.ID
		}
		if parsed, err := url.Parse(format.URL); err == nil {
			variant.XTags = parsed.Query().Get("xtags")
			variant.DRC = parsed.Query().Get("drc")
		}
		if format.XTags != "" {
			variant.XTags = format.XTags
		}
		variant.DRC = strconv.FormatBool(format.IsDRC || variant.DRC == "1" || variant.DRC == "true")
		variant.VoiceBoost = strconv.FormatBool(format.IsVoiceBoost)
	}
	data, _ := json.Marshal(variant)
	return string(data)
}

func audioTrackPreference(format *youtube.Format) int {
	if format == nil {
		return -1
	}
	if format.AudioTrack == nil {
		return 1
	}
	if format.AudioTrack.AudioIsDefault {
		return 2
	}
	return 0
}
