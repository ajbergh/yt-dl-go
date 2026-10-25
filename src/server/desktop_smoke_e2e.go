//go:build desktop && e2e

package main

import (
	"os"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var desktopSmokeOnce sync.Once

// This binding is deliberately absent from distributable desktop builds. It
// proves the native WebView mounted React and connected to the actual service.
func (a *DesktopApp) UIReady() error {
	output := os.Getenv("DESKTOP_SMOKE_OUTPUT")
	if output == "" {
		return nil
	}
	var err error
	desktopSmokeOnce.Do(func() {
		err = os.WriteFile(output, []byte("{\"ready\":true}\n"), 0600)
		wruntime.Quit(a.ctx)
	})
	return err
}
