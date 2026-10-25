//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// An Explorer launch owns its console: it disappears as soon as startup exits.
// Keep the failure visible without pausing an existing shell or background job.
func showStartupError(message string) {
	if os.Getenv("NO_BROWSER") == "1" {
		return
	}
	for _, arg := range os.Args[1:] {
		if arg == "--background" || arg == "--no-browser" {
			return
		}
	}
	var processIDs [2]uint32
	count, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList").Call(
		uintptr(unsafe.Pointer(&processIDs[0])), uintptr(len(processIDs)))
	if count != 1 {
		return
	}
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	title, err := windows.UTF16PtrFromString("YouTube Downloader could not start")
	if err != nil {
		return
	}
	const errorIcon = 0x10
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), errorIcon)
}
