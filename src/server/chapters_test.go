package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseDescriptionChapters(t *testing.T) {
	description := "Intro\n0:00 Opening\n- 1:05 Chapter one\n1:02:03 深い話\n1:02:03 duplicate\n99:99 invalid\n11:00:00 outside"
	want := []mediaChapter{
		{StartMs: 0, EndMs: 65_000, Title: "Opening"},
		{StartMs: 65_000, EndMs: 3_723_000, Title: "Chapter one"},
		{StartMs: 3_723_000, EndMs: 36_000_000, Title: "深い話"},
	}
	if got := parseDescriptionChapters(description, 10*time.Hour); !reflect.DeepEqual(got, want) {
		t.Fatalf("chapters = %+v, want %+v", got, want)
	}
}

func TestParseDescriptionChaptersRejectsInvalidSets(t *testing.T) {
	for _, test := range []struct {
		name        string
		description string
		duration    time.Duration
	}{
		{name: "one chapter", description: "0:00 only", duration: time.Minute},
		{name: "missing duration", description: "0:00 one\n0:20 two", duration: 0},
		{name: "out of order", description: "0:20 two\n0:00 one", duration: time.Minute},
		{name: "no title", description: "0:00\n0:10 second", duration: time.Minute},
		{name: "timestamps outside duration", description: "0:10 one\n0:20 two", duration: 10 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := parseDescriptionChapters(test.description, test.duration); len(got) != 0 {
				t.Fatalf("chapters = %+v, want none", got)
			}
		})
	}
}

func TestParseDescriptionChaptersBoundsChapterCountAndUTF8(t *testing.T) {
	description := ""
	for index := 0; index < maxVideoChapters+5; index++ {
		description += fmt.Sprintf("%d:00 Chapter %d\n", index, index)
	}
	description = strings.Replace(description, "0:00 Chapter 0", "0:00 "+strings.Repeat("界", 120), 1)
	chapters := parseDescriptionChapters(description, 5*time.Hour)
	if len(chapters) != maxVideoChapters {
		t.Fatalf("chapter count = %d, want %d", len(chapters), maxVideoChapters)
	}
	if !utf8.ValidString(chapters[0].Title) || len(chapters[0].Title) > 255 {
		t.Fatalf("truncated chapter title is invalid UTF-8 or too long: %d bytes", len(chapters[0].Title))
	}
}
