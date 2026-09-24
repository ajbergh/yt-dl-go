package main

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxVideoChapters = 255

type mediaChapter struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Title   string `json:"title"`
}

type chapterStart struct {
	startMs int64
	title   string
}

// parseDescriptionChapters recognizes YouTube's timestamp/title description
// lines and returns validated, ordered ranges bounded by the video duration.
func parseDescriptionChapters(description string, duration time.Duration) []mediaChapter {
	durationMs := duration.Milliseconds()
	if durationMs <= 0 {
		return nil
	}
	starts := make([]chapterStart, 0, 16)
	for _, line := range strings.Split(description, "\n") {
		if len(starts) >= maxVideoChapters {
			break
		}
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•\t "))
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		startMs, ok := parseChapterTimestamp(fields[0])
		if !ok || startMs >= durationMs {
			continue
		}
		title := strings.TrimSpace(strings.Join(fields[1:], " "))
		title = truncateUTF8(title, 255)
		if title == "" {
			continue
		}
		starts = append(starts, chapterStart{startMs: startMs, title: title})
	}
	if len(starts) < 2 {
		return nil
	}
	// Keep source ordering: silently sorting malformed descriptions can make
	// chapter titles appear attached to the wrong timestamps.
	chapters := make([]mediaChapter, 0, min(len(starts), maxVideoChapters))
	for index, item := range starts {
		if len(chapters) > 0 && item.startMs <= chapters[len(chapters)-1].StartMs {
			continue
		}
		endMs := durationMs
		for next := index + 1; next < len(starts); next++ {
			if starts[next].startMs > item.startMs {
				endMs = min(starts[next].startMs, durationMs)
				break
			}
		}
		if endMs <= item.startMs {
			continue
		}
		chapters = append(chapters, mediaChapter{StartMs: item.startMs, EndMs: endMs, Title: item.title})
		if len(chapters) == maxVideoChapters {
			break
		}
	}
	if len(chapters) < 2 {
		return nil
	}
	for index := range chapters {
		if index+1 < len(chapters) {
			chapters[index].EndMs = chapters[index+1].StartMs
		}
	}
	return chapters
}

func parseChapterTimestamp(value string) (int64, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, false
	}
	values := make([]int64, len(parts))
	for index, part := range parts {
		if part == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(part, 10, 32)
		if err != nil || parsed < 0 {
			return 0, false
		}
		values[index] = parsed
	}
	var seconds int64
	if len(values) == 2 {
		if values[1] >= 60 {
			return 0, false
		}
		seconds = values[0]*60 + values[1]
	} else {
		if values[1] >= 60 || values[2] >= 60 {
			return 0, false
		}
		seconds = values[0]*3600 + values[1]*60 + values[2]
	}
	return seconds * 1000, true
}

func truncateUTF8(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
