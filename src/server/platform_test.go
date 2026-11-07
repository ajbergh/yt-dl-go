package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeBackgroundOptions(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		env         string
		wantNoUI    bool
		wantHelp    bool
		wantVersion bool
		wantErr     bool
	}{
		{name: "default opens browser"},
		{name: "legacy env", env: "1", wantNoUI: true},
		{name: "no-browser flag", args: []string{"--no-browser"}, wantNoUI: true},
		{name: "background alias", args: []string{"--background"}, wantNoUI: true},
		{name: "flag wins with nonlegacy env value", args: []string{"--background"}, env: "0", wantNoUI: true},
		{name: "help short", args: []string{"-h"}, wantHelp: true},
		{name: "help long", args: []string{"--help"}, wantHelp: true},
		{name: "version", args: []string{"--version"}, wantVersion: true},
		{name: "unknown argument rejected", args: []string{"--tray"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseRuntimeOptions(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected unsupported argument error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if options.help != test.wantHelp {
				t.Fatalf("help = %v; want %v", options.help, test.wantHelp)
			}
			if options.version != test.wantVersion {
				t.Fatalf("version = %v; want %v", options.version, test.wantVersion)
			}
			gotNoUI := !automaticBrowserEnabled(options, test.env)
			if gotNoUI != test.wantNoUI {
				t.Fatalf("no-browser behavior = %v; want %v", gotNoUI, test.wantNoUI)
			}
		})
	}
	if usage := runtimeUsage(); !strings.Contains(usage, "--background") || !strings.Contains(usage, "--no-browser") || !strings.Contains(usage, "--version") {
		t.Fatalf("runtime usage does not document supported flags: %q", usage)
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
	path := "/media/downloads/Channel Name/video #1, café.mp4"
	tests := []struct {
		name    string
		goos    string
		action  string
		command string
		args    []string
	}{
		{"windows reveal", "windows", "reveal", "explorer.exe", []string{"/select,", path}},
		{"windows folder", "windows", "open-folder", "explorer.exe", []string{"/media/downloads"}},
		{"macOS reveal", "darwin", "reveal", "open", []string{"-R", path}},
		{"macOS folder", "darwin", "open-folder", "open", []string{"/media/downloads"}},
		{"Linux reveal", "linux", "reveal", "dbus-send", []string{"--session", "--print-reply", "--type=method_call", "--dest=org.freedesktop.FileManager1", "/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems", "array:string:file:///media/downloads/Channel%20Name/video%20%231%2C%20caf%C3%A9.mp4", "string:"}},
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
		version   bool
		wantErr   bool
	}{
		{name: "default"},
		{name: "background", args: []string{"--background"}, noBrowser: true},
		{name: "no browser", args: []string{"--no-browser"}, noBrowser: true},
		{name: "aliases together", args: []string{"--background", "--no-browser"}, noBrowser: true},
		{name: "short help", args: []string{"-h"}, help: true},
		{name: "long help", args: []string{"--help"}, help: true},
		{name: "version", args: []string{"--version"}, version: true},
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
			if options.noBrowser != test.noBrowser || options.help != test.help || options.version != test.version {
				t.Fatalf("parseRuntimeOptions(%v) = %+v, want noBrowser=%v help=%v version=%v", test.args, options, test.noBrowser, test.help, test.version)
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

func TestWindowsRevealSeparatesSwitchAndPath(t *testing.T) {
	for _, path := range []string{`C:\Downloads\Channel Name\Video [1080p].mp4`, `\\server\share\Channel, Name\café #1.mp4`} {
		command, args, err := trackedOutputCommand("windows", "reveal", path, `C:\Downloads`)
		if err != nil || command != "explorer.exe" || !reflect.DeepEqual(args, []string{"/select,", path}) {
			t.Fatalf("invalid Explorer arguments: %q %q %v", command, args, err)
		}
	}
}

func TestLinuxRevealSelectsOrFallsBackToFolder(t *testing.T) {
	for _, selectionError := range []error{nil, errors.New("file manager does not implement ShowItems")} {
		var commands []string
		err := runTrackedOutput(context.Background(), "linux", "reveal", "/media/Channel Name/video.mp4", "/media/Channel Name", func(ctx context.Context, command string, args ...string) error {
			commands = append(commands, command)
			if command == "dbus-send" {
				if _, hasDeadline := ctx.Deadline(); !hasDeadline {
					t.Fatal("selection needs a timeout")
				}
				return selectionError
			}
			if !reflect.DeepEqual(args, []string{"/media/Channel Name"}) {
				t.Fatalf("wrong fallback folder: %q", args)
			}
			return nil
		})
		expected := []string{"dbus-send"}
		if selectionError != nil {
			expected = append(expected, "xdg-open")
		}
		if err != nil || !reflect.DeepEqual(commands, expected) {
			t.Fatalf("reveal sequence=%v err=%v", commands, err)
		}
	}
	launchError := errors.New("folder opener failed")
	err := runTrackedOutput(context.Background(), "linux", "reveal", "/media/video.mp4", "/media", func(context.Context, string, ...string) error { return launchError })
	if !errors.Is(err, launchError) {
		t.Fatalf("fallback error was hidden: %v", err)
	}
}
