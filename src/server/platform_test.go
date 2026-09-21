package main

import (
	"reflect"
	"testing"
)

func TestRuntimeBackgroundOptions(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		env       string
		noBrowser bool
		wantErr   bool
	}{
		{name: "default opens browser"},
		{name: "legacy env", env: "1", noBrowser: true},
		{name: "no-browser flag", args: []string{"--no-browser"}, noBrowser: true},
		{name: "background alias", args: []string{"--background"}, noBrowser: true},
		{name: "flag overrides empty env", args: []string{"--background"}, env: "0", noBrowser: true},
		{name: "unknown argument rejected", args: []string{"--tray"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseRuntimeOptions(test.args, test.env)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected unsupported argument error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if options.noBrowser != test.noBrowser {
				t.Fatalf("noBrowser = %v; want %v", options.noBrowser, test.noBrowser)
			}
		})
	}
}

func TestBrowserOpenCommands(t *testing.T) {
	target := "http://127.0.0.1:8080/"
	tests := []struct {
		goos      string
		command   string
		args      []string
		supported bool
	}{
		{"windows", "rundll32.exe", []string{"url.dll,FileProtocolHandler", target}, true},
		{"darwin", "open", []string{target}, true},
		{"linux", "xdg-open", []string{target}, true},
		{"plan9", "", nil, false},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			command, args, supported := browserOpenCommand(test.goos, target)
			if command != test.command || supported != test.supported || !reflect.DeepEqual(args, test.args) {
				t.Fatalf("browser command = %q %v supported=%v; want %q %v supported=%v",
					command, args, supported, test.command, test.args, test.supported)
			}
		})
	}
}

func TestNativeFolderCommands(t *testing.T) {
	command, args, err := nativeFolderCommand("windows", "")
	if err != nil || command != "powershell.exe" || len(args) != 4 || args[0] != "-NoProfile" || args[1] != "-STA" {
		t.Fatalf("windows folder command = %q %v err=%v", command, args, err)
	}
	command, args, err = nativeFolderCommand("darwin", "")
	if err != nil || command != "osascript" || len(args) != 2 || args[0] != "-e" {
		t.Fatalf("macOS folder command = %q %v err=%v", command, args, err)
	}
	command, args, err = nativeFolderCommand("linux", "/usr/bin/zenity")
	if err != nil || command != "/usr/bin/zenity" || !reflect.DeepEqual(args, []string{"--file-selection", "--directory", "--title=Choose download folder"}) {
		t.Fatalf("Linux folder command = %q %v err=%v", command, args, err)
	}
	if _, _, err = nativeFolderCommand("linux", ""); err == nil {
		t.Fatal("Linux folder picker accepted a missing zenity path")
	}
	if _, _, err = nativeFolderCommand("plan9", ""); err == nil {
		t.Fatal("unsupported platform received a folder picker command")
	}
}

func TestTrackedOutputCommands(t *testing.T) {
	path := "/media/downloads/video.mp4"
	tests := []struct {
		name    string
		goos    string
		action  string
		command string
		args    []string
	}{
		{"windows reveal", "windows", "reveal", "explorer.exe", []string{"/select," + path}},
		{"windows folder", "windows", "open-folder", "explorer.exe", []string{"/media/downloads"}},
		{"macOS reveal", "darwin", "reveal", "open", []string{"-R", path}},
		{"macOS folder", "darwin", "open-folder", "open", []string{"/media/downloads"}},
		{"Linux reveal fallback", "linux", "reveal", "xdg-open", []string{"/media/downloads"}},
		{"Linux folder", "linux", "open-folder", "xdg-open", []string{"/media/downloads"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, args, err := trackedOutputCommand(test.goos, test.action, path, "/media/downloads")
			if err != nil || command != test.command || !reflect.DeepEqual(args, test.args) {
				t.Fatalf("filesystem command = %q %v err=%v; want %q %v", command, args, err, test.command, test.args)
			}
		})
	}
	if _, _, err := trackedOutputCommand("linux", "execute", path, "/media/downloads"); err == nil {
		t.Fatal("unsupported filesystem action was accepted")
	}
	if _, _, err := trackedOutputCommand("plan9", "reveal", path, "/media/downloads"); err == nil {
		t.Fatal("unsupported platform received a filesystem command")
	}
}

func TestBrowserURLUsesLoopbackForWildcardListeners(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080"} {
		if got := browserURL(addr); got != "http://127.0.0.1:8080/" {
			t.Fatalf("browserURL(%q) = %q", addr, got)
		}
	}
}

func TestRuntimeOptions(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		noBrowser bool
		help      bool
		wantErr   bool
	}{
		{name: "default"},
		{name: "background", args: []string{"--background"}, noBrowser: true},
		{name: "no browser", args: []string{"--no-browser"}, noBrowser: true},
		{name: "aliases together", args: []string{"--background", "--no-browser"}, noBrowser: true},
		{name: "short help", args: []string{"-h"}, help: true},
		{name: "long help", args: []string{"--help"}, help: true},
		{name: "unknown", args: []string{"--tray"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseRuntimeOptions(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseRuntimeOptions(%v) err=%v wantErr=%v", test.args, err, test.wantErr)
			}
			if err != nil {
				return
			}
			if options.noBrowser != test.noBrowser || options.help != test.help {
				t.Fatalf("parseRuntimeOptions(%v) = %+v, want noBrowser=%v help=%v", test.args, options, test.noBrowser, test.help)
			}
		})
	}
}

func TestAutomaticBrowserEnabled(t *testing.T) {
	if !automaticBrowserEnabled(runtimeOptions{}, "") {
		t.Fatal("default startup should open the UI")
	}
	if automaticBrowserEnabled(runtimeOptions{noBrowser: true}, "") {
		t.Fatal("--background should suppress automatic browser launch")
	}
	if automaticBrowserEnabled(runtimeOptions{}, "1") {
		t.Fatal("NO_BROWSER=1 should suppress automatic browser launch")
	}
	if !automaticBrowserEnabled(runtimeOptions{}, "true") {
		t.Fatal("legacy NO_BROWSER semantics should remain exact: only value 1 suppresses launch")
	}
}
