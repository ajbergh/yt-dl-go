package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type desktopConnection struct {
	Base  string `json:"base"`
	Token string `json:"token"`
}

// Keep SSE, range previews and ZIP streams on real HTTP. Wails' Windows asset
// server buffers responses, so it must only serve the static desktop interface.
type desktopService struct {
	connection desktopConnection
	server     *server
	http       *http.Server
	once       sync.Once
}

func newDesktopService(c config) (*desktopService, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start private desktop service: %w", err)
	}
	c.addr = listener.Addr().String()
	c.token = randomID(32)
	c.hosts = map[string]bool{c.addr: true}
	// WebKit serializes custom-scheme origins as null on some versions. Even
	// these requests require the per-launch bearer, known only to the Wails UI.
	c.origins = map[string]bool{"http://wails.localhost": true, "wails://wails": true, "null": true}
	s, err := newServer(c)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.hosts[r.Host] && c.origins[r.Header.Get("Origin")] {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		s.ServeHTTP(w, r)
	})
	h := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	service := &desktopService{connection: desktopConnection{Base: "http://" + c.addr, Token: c.token}, server: s, http: h}
	s.start()
	go func() { _ = h.Serve(listener) }()
	return service, nil
}

func (d *desktopService) close() {
	d.once.Do(func() {
		// Cancel long-lived event streams before draining the workers/store.
		_ = d.http.Close()
		d.server.stop()
	})
}

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

var desktopTicketPath = regexp.MustCompile(`^/api/downloads/[A-Za-z0-9_-]+$`)

func desktopSavePath(path string) bool {
	return path == "/api/library/export" || desktopTicketPath.MatchString(path)
}

// Only server-issued download tickets and the Library export can be saved.
// No arbitrary URL or caller-supplied filesystem path is exposed to bindings.
func (d *desktopService) save(ctx context.Context, path string, choose func(string) (string, error)) (bool, error) {
	if !desktopSavePath(path) {
		return false, errors.New("unsupported desktop save request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.connection.Base+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+d.connection.Token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return false, errors.New("could not read the requested download")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("download is unavailable (%d)", response.StatusCode)
	}
	_, parameters, _ := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	filename := filepath.Base(strings.ReplaceAll(parameters["filename"], `\`, "/"))
	if filename == "" || filename == "." || filename == ".." || filename == "/" {
		filename = "download"
	}
	destination, err := choose(filename)
	if err != nil || destination == "" {
		return false, err // A cancelled native dialog is not an error.
	}
	if !filepath.IsAbs(destination) {
		return false, errors.New("the save destination must be an absolute path")
	}
	if err := writeDesktopDownload(response.Body, destination); err != nil {
		return false, err
	}
	return true, nil
}

// Stage beside the destination so failed transfers leave an existing file
// intact. Copy with bounded memory; never materialize media/ZIP bytes in JS.
func writeDesktopDownload(source io.Reader, destination string) error {
	file, err := os.CreateTemp(filepath.Dir(destination), ".yt-dl-go-save-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, copyErr := io.Copy(file, source)
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return fmt.Errorf("finish download: %w", err)
	}
	return nil
}
