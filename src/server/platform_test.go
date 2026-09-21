package main

import (
	"reflect"
	"testing"
)

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
	if err != nil || command != "powershell.exe" || len(args) != 5 || args[0] != "-NoProfile" || args[1] != "-STA" {
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
			command, args, err := trackedOutputCommand(test.goos, test.action, path)
			if err != nil || command != test.command || !reflect.DeepEqual(args, test.args) {
				t.Fatalf("filesystem command = %q %v err=%v; want %q %v", command, args, err, test.command, test.args)
			}
		})
	}
	if _, _, err := trackedOutputCommand("linux", "execute", path); err == nil {
		t.Fatal("unsupported filesystem action was accepted")
	}
	if _, _, err := trackedOutputCommand("plan9", "reveal", path); err == nil {
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
