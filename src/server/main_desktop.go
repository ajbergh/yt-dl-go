//go:build desktop

package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/appicon.png
var desktopIcon []byte

// DesktopApp exposes bootstrap, native dialogs, and save operations. The
// existing authenticated HTTP API remains the shared engine control plane.
type DesktopApp struct {
	ctx     context.Context
	service *desktopService
	ready   chan struct{}
	err     error
}

func (a *DesktopApp) Connection() (desktopConnection, error) {
	if os.Getenv("DESKTOP_SMOKE_OUTPUT") != "" {
		log.Print("Desktop Connection binding entered")
	}
	<-a.ready
	if os.Getenv("DESKTOP_SMOKE_OUTPUT") != "" {
		log.Print("Desktop Connection binding unblocked")
	}
	if a.err != nil {
		return desktopConnection{}, a.err
	}
	return a.service.connection, nil
}

func (a *DesktopApp) Save(path string) (bool, error) {
	<-a.ready
	if a.err != nil {
		return false, a.err
	}
	return a.service.save(a.ctx, path, func(filename string) (string, error) {
		return wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
			Title: "Save download", DefaultFilename: filename, CanCreateDirectories: true,
		})
	})
}

func (a *DesktopApp) Confirm(title, message string) (bool, error) {
	result, err := wruntime.MessageDialog(a.ctx, wruntime.MessageDialogOptions{
		Type: wruntime.QuestionDialog, Title: title, Message: message,
		Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No",
	})
	return err == nil && result == "Yes", err
}

func (a *DesktopApp) startup(ctx context.Context, c config) {
	if os.Getenv("DESKTOP_SMOKE_OUTPUT") != "" {
		log.Print("Desktop service startup entered")
	}
	defer close(a.ready)
	a.service, a.err = newDesktopService(c)
	if os.Getenv("DESKTOP_SMOKE_OUTPUT") != "" {
		log.Printf("Desktop service initialized: %t", a.err == nil)
	}
	if a.err != nil {
		return
	}
	a.service.server.folderSelector = func(context.Context) (string, error) {
		selected, err := wruntime.OpenDirectoryDialog(ctx, wruntime.OpenDialogOptions{Title: "Choose download folder", CanCreateDirectories: true})
		if err == nil && selected == "" {
			return "", errFolderSelectionCancelled
		}
		return selected, err
	}
}

func main() {
	// The desktop binary retains the existing version, migration and explicit
	// background modes for diagnostics, automation and package smoke testing.
	if len(os.Args) > 1 {
		runBrowserApplication()
		return
	}
	// GUI executables may have no usable stderr. An explicit diagnostic file
	// keeps native startup failures inspectable without exposing the bearer.
	if destination := os.Getenv("DESKTOP_LOG_FILE"); destination != "" {
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			fatalStartup(fmt.Errorf("open desktop diagnostic log: %w", err))
		}
		defer file.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, file))
	}
	c, err := loadConfig()
	if err != nil {
		fatalStartup(err)
	}
	assets, err := fs.Sub(embeddedAssets, "dist")
	if err != nil {
		fatalStartup(err)
	}
	app := &DesktopApp{ready: make(chan struct{})}
	appMenu := menu.NewMenu()
	if runtime.GOOS == "darwin" {
		appMenu.Append(menu.AppMenu())
	}
	fileMenu := appMenu.AddSubmenu("File")
	fileMenu.AddText("Exit", nil, func(*menu.CallbackData) {
		wruntime.Quit(app.ctx)
	})
	if runtime.GOOS == "darwin" {
		appMenu.Append(menu.EditMenu())
	}
	helpMenu := appMenu.AddSubmenu("Help")
	helpMenu.AddText("About", nil, func(*menu.CallbackData) {
		info := currentBuildInfo()
		_, dialogErr := wruntime.MessageDialog(app.ctx, wruntime.MessageDialogOptions{
			Type: wruntime.InfoDialog, Title: "About YouTube Vault",
			Message: fmt.Sprintf("YouTube Vault\nVideo and playlist downloads\n\nVersion: %s\nCommit: %s\nBuilt: %s\n\nYour download folder is your vault.", info.Version, info.Commit, info.Date),
			Buttons: []string{"OK"}, DefaultButton: "OK",
		})
		if dialogErr != nil {
			log.Printf("show About dialog: %v", dialogErr)
		}
	})
	err = wails.Run(&options.App{
		Title: "YouTube Vault", Width: 1280, Height: 860, MinWidth: 780, MinHeight: 600,
		BackgroundColour: options.NewRGB(10, 10, 10),
		AssetServer: &assetserver.Options{
			Assets: assets,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !serveStaticFS(w, r, embeddedAssets) {
					http.NotFound(w, r)
				}
			}),
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					<-app.ready
					base := ""
					if app.service != nil {
						base = app.service.connection.Base
					}
					desktopAssetMiddleware(base)(next).ServeHTTP(w, r)
				})
			},
		},
		Menu: appMenu, Bind: []interface{}{app},
		OnStartup: func(ctx context.Context) {
			app.ctx = ctx
			go app.startup(ctx, c)
		},
		OnDomReady: func(context.Context) { log.Print("Desktop WebView DOM ready") },
		OnShutdown: func(context.Context) {
			<-app.ready
			if app.service != nil {
				app.service.close()
			}
		},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "2f732518-cd20-4a1f-b096-c26e08945283"},
		Windows:            &windows.Options{WebviewUserDataPath: filepath.Join(c.root, "webview")},
		Linux:              &linux.Options{Icon: desktopIcon, ProgramName: "youtube-vault", WebviewGpuPolicy: linux.WebviewGpuPolicyNever},
	})
	if err != nil {
		fatalStartup(fmt.Errorf("open desktop window: %w", err))
	}
}

// desktopAssetMiddleware protects bundled UI assets and permits authenticated
// access to the ephemeral loopback service. Only native builds use it.
func desktopAssetMiddleware(base string) func(http.Handler) http.Handler {
	// Wails injects its trusted bridge as inline script. All other script and
	// connection origins stay restricted to bundled assets and this one service.
	policy := strings.Replace(contentSecurityPolicy, "script-src 'self'", "script-src 'self' 'unsafe-inline'", 1)
	policy = strings.Replace(policy, "media-src 'self' blob:", "media-src 'self' blob: "+base, 1)
	policy = strings.Replace(policy, "img-src 'self'", "img-src 'self' "+base, 1)
	policy += "; connect-src 'self' " + base
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", policy)
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, r)
		})
	}
}
