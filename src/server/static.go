// static.go serves the React SPA and hashed assets embedded in the executable.
package main

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embeddedAssets embed.FS

func getMimeType(filePath string) string {
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

// serveStatic serves only GET/HEAD requests, prefers embedded files, assigns
// long immutable caching to assets, and falls back to index.html for SPA paths.
func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) bool {
	return serveStaticFS(w, r, embeddedAssets)
}

func serveStaticFS(w http.ResponseWriter, r *http.Request, assets fs.FS) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}

	distFS, err := fs.Sub(assets, "dist")
	if err != nil {
		return false
	}

	reqPath := path.Clean(r.URL.Path)
	if reqPath == "/" || reqPath == "." {
		reqPath = "index.html"
	} else {
		reqPath = strings.TrimPrefix(reqPath, "/")
	}

	// Try serving the exact file if it exists and is not a directory.
	file, err := distFS.Open(reqPath)
	if err == nil {
		defer file.Close()
		stat, err := file.Stat()
		if err == nil && !stat.IsDir() {
			w.Header().Set("Content-Type", getMimeType(reqPath))
			if strings.HasPrefix(reqPath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.FileServer(http.FS(distFS)).ServeHTTP(w, r)
			return true
		}
	}

	// Fall back to the SPA entry point for any unresolved path.
	if indexFile, err := distFS.Open("index.html"); err == nil {
		_ = indexFile.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		r.URL.Path = "/"
		http.FileServer(http.FS(distFS)).ServeHTTP(w, r)
		return true
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>UI not built</title><body><main><h1>UI not built</h1>
<p>Build the web interface with <code>npm run build -- --outDir src/server/dist</code> from the repository root, then restart the server.</p>
</main></body></html>`))
	}
	return true
}
