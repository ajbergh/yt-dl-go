package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNamingPatternExpandsOnceAndSupportsEveryToken(t *testing.T) {
	values := namingValues{
		Channel: "{title}", Title: "Title {channel}", Resolution: "1080p", Category: "Tech",
		ID: "dQw4w9WgXcQ", UploadDate: "2026-09-23", Playlist: "Series", Index: "7",
		Ext: "mp4", FPS: "60", Codec: "h264",
	}
	pattern := "{channel}|{title}|{resolution}|{category}|{id}|{upload_date}|{playlist}|{index}|{ext}|{fps}|{codec}"
	want := "{title}|Title {channel}|1080p|Tech|dQw4w9WgXcQ|2026-09-23|Series|7|mp4|60|h264"
	if got := expandNamingPattern(pattern, values); got != want {
		t.Fatalf("expandNamingPattern() = %q, want %q", got, want)
	}
}

func TestOutputNameCollisionSuffixKeepsExtensionLast(t *testing.T) {
	for _, test := range []struct{ base, want string }{
		{"recording", "recording (2).mp4"},
		{"recording.mp4", "recording (2).mp4"},
		{"recording.MP4", "recording (2).MP4"},
	} {
		if got := outputNameForPattern(test.base, ".mp4", 1); got != test.want {
			t.Errorf("outputNameForPattern(%q) = %q, want %q", test.base, got, test.want)
		}
	}
}

func TestNamingPatternAndOutputModesValidation(t *testing.T) {
	settings := defaultAppSettings()
	settings.NamingPattern = "{id}-{upload_date}-{playlist}-{index}-{ext}-{fps}-{codec}-{channel}-{title}-{resolution}-{category}"
	if err := validateAppSettings(settings); err != nil {
		t.Fatalf("all supported tokens were rejected: %v", err)
	}
	for _, pattern := range []string{"{unknown}", "}{channel}", "{channel", "{channel}}"} {
		settings.NamingPattern = pattern
		if err := validateAppSettings(settings); err == nil {
			t.Errorf("invalid pattern %q was accepted", pattern)
		}
	}
	settings.NamingPattern = defaultNamingPattern
	for _, mode := range []string{"0644", "0755", "0000", "0777"} {
		if _, err := parseOutputMode(mode); err != nil {
			t.Errorf("valid mode %q rejected: %v", mode, err)
		}
	}
	for _, mode := range []string{"777", "0788", "1000", "1777", "-rw-r--r--"} {
		if _, err := parseOutputMode(mode); err == nil {
			t.Errorf("invalid mode %q accepted", mode)
		}
	}
}

func TestPublishOutputAppliesConfiguredModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not supported by Windows")
	}
	s := testServer(t, fixtureClient(1), nil)
	j := &jobState{Job: Job{
		DownloadLocation: filepath.Join(s.cfg.root, "published"),
		NamingPattern:    "{channel} - {title} [{id}] {fps} {codec}.{ext}",
		SubfolderSorting: "flat", OutputFileMode: "0640", OutputFolderMode: "0750",
		StorageMode: "managed-published", Category: "Tech",
	}, dir: filepath.Join(s.cfg.root, "managed")}
	if err := os.Mkdir(j.dir, 0700); err != nil {
		t.Fatal(err)
	}
	const managedName = "000001-dQw4w9WgXcQ.mp4"
	if err := os.WriteFile(filepath.Join(j.dir, managedName), []byte(fixtureData), 0600); err != nil {
		t.Fatal(err)
	}
	file := mediaFile{
		Name: managedName, Size: int64(len(fixtureData)), Title: "A title", Author: "A channel", Height: 1080,
		naming: namingValues{ID: "dQw4w9WgXcQ", FPS: "60", Codec: "h264"},
	}
	if err := s.publishOutput(j, &file); err != nil {
		t.Fatal(err)
	}
	if file.OutputName != "A channel - A title [dQw4w9WgXcQ] 60 h264.mp4" {
		t.Fatalf("output name = %q", file.OutputName)
	}
	info, err := os.Stat(file.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("published file mode = %04o, want 0640", got)
	}
	info, err = os.Stat(j.DownloadLocation)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0750 {
		t.Fatalf("published folder mode = %04o, want 0750", got)
	}
}
