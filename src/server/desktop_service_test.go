package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopServiceUsesPrivateAuthenticatedStreamingTransport(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	prepareTestDataDir(t, root)
	desktop, err := newDesktopService(config{root: root, maxJobs: 8, maxBytes: 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(desktop.close)
	if !strings.HasPrefix(desktop.connection.Base, "http://127.0.0.1:") || len(desktop.connection.Token) != 64 {
		t.Fatal("desktop service did not use an ephemeral loopback capability")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, tc := range []struct {
		origin, token, host string
		status              int
	}{
		{origin: "http://wails.localhost", token: desktop.connection.Token, status: 200},
		{origin: "wails://wails", token: desktop.connection.Token, status: 200},
		{origin: "null", token: desktop.connection.Token, status: 200},
		{origin: "http://wails.localhost", status: 401},
		{origin: "https://attacker.invalid", token: desktop.connection.Token, status: 403},
		{origin: "http://wails.localhost", token: desktop.connection.Token, host: "attacker.invalid", status: 403},
	} {
		req, err := http.NewRequest(http.MethodGet, desktop.connection.Base+"/api/jobs", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		if tc.host != "" {
			req.Host = tc.host
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("origin=%s host=%s: status=%d, want %d", tc.origin, tc.host, response.StatusCode, tc.status)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, desktop.connection.Base+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://wails.localhost")
	req.Header.Set("Authorization", "Bearer "+desktop.connection.Token)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || response.StatusCode != 200 || line == "" || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("live stream was not delivered before closing: status=%d line=%q err=%v", response.StatusCode, line, err)
	}
}

func TestDesktopSavePreservesExistingFileOnTransferFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "existing.mp4")
	if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	source := io.MultiReader(strings.NewReader("partial"), failingDesktopReader{})
	if err := writeDesktopDownload(source, destination); err == nil {
		t.Fatal("failed transfer was accepted")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "original" {
		t.Fatalf("existing file was changed: %q %v", data, err)
	}
	if err := writeDesktopDownload(strings.NewReader("complete"), destination); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(destination)
	if err != nil || string(data) != "complete" {
		t.Fatalf("successful transfer was not published: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(entries) != 1 {
		t.Fatalf("partial save files leaked: %v %v", entries, err)
	}
}

type failingDesktopReader struct{}

func (failingDesktopReader) Read([]byte) (int, error) { return 0, errors.New("interrupted transfer") }

func TestDesktopSaveScopesRequestsAndHandlesNativeCancellation(t *testing.T) {
	requests := 0
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer private-capability" {
			t.Error("native save omitted authentication")
		}
		w.Header().Set("Content-Disposition", `attachment; filename="sample.zip"`)
		_, _ = io.WriteString(w, "archive")
	}))
	defer host.Close()
	desktop := &desktopService{connection: desktopConnection{Base: host.URL, Token: "private-capability"}}
	for _, path := range []string{"https://attacker.invalid/", "/api/settings", "/api/downloads/a/../../settings", "/api/downloads/a?url=external"} {
		if _, err := desktop.save(context.Background(), path, nil); err == nil {
			t.Fatalf("accepted unsupported save route %s", path)
		}
	}
	if requests != 0 {
		t.Fatal("unsupported route reached HTTP")
	}
	saved, err := desktop.save(context.Background(), "/api/downloads/ticket", func(filename string) (string, error) {
		if filename != "sample.zip" {
			t.Errorf("native dialog filename=%q", filename)
		}
		return "", nil
	})
	if err != nil || saved {
		t.Fatalf("cancelled dialog = %v %v", saved, err)
	}
	destination := filepath.Join(t.TempDir(), "library.zip")
	saved, err = desktop.save(context.Background(), "/api/library/export", func(string) (string, error) { return destination, nil })
	if err != nil || !saved {
		t.Fatalf("export save = %v %v", saved, err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "archive" {
		t.Fatalf("saved bytes = %q %v", data, err)
	}
}
