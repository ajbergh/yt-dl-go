//go:build desktop

package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// DesktopApp exposes only bootstrap and user-confirmed save operations. The
// existing authenticated HTTP API remains the shared engine control plane.
type DesktopApp struct {
	ctx     context.Context
	service *desktopService
	ready   chan struct{}
	err     error
}

func (a *DesktopApp) Connection() (desktopConnection, error) {
	<-a.ready
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

func (a *DesktopApp) startup(ctx context.Context, c config) {
	defer close(a.ready)
	a.ctx = ctx
	a.service, a.err = newDesktopService(c)
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
	appMenu.Append(menu.AppMenu())
	appMenu.Append(menu.EditMenu())
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
		OnStartup: func(ctx context.Context) { app.startup(ctx, c) },
		OnShutdown: func(context.Context) {
			<-app.ready
			if app.service != nil {
				app.service.close()
			}
		},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "2f732518-cd20-4a1f-b096-c26e08945283"},
		Windows:            &windows.Options{WebviewUserDataPath: filepath.Join(c.root, "webview")},
	})
	if err != nil {
		fatalStartup(fmt.Errorf("open desktop window: %w", err))
	}
}
