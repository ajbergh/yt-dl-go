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

 // UIStage records only known readiness milestones, never bearer values or
 // URLs. It exists solely in e2e-tagged smoke binaries.
 func (a *DesktopApp) UIStage(stage string) error {
 	switch stage {
 	case "bootstrap", "connection", "health-request", "health-response", "health-not-ready", "service-error":
 	default:
 		return nil
 	}
 	output := os.Getenv("DESKTOP_SMOKE_OUTPUT")
 	if output == "" {
 		return nil
 	}
 	file, err := os.OpenFile(output+".stages", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
 	if err != nil {
 		return err
 	}
 	if _, err = file.WriteString(stage+"\n"); err != nil {
 		_ = file.Close()
 		return err
 	}
 	return file.Close()
 }
