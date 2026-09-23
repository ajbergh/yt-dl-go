package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServeStaticFSExplainsMissingBuild(t *testing.T) {
	assets := fstest.MapFS{"dist/.gitkeep": &fstest.MapFile{}}
	request := httptest.NewRequest(http.MethodGet, "/library", nil)
	response := httptest.NewRecorder()

	if !serveStaticFS(response, request, assets) {
		t.Fatal("serveStaticFS returned false for the missing UI build page")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if body := response.Body.String(); !strings.Contains(body, "UI not built") || !strings.Contains(body, "npm run build") {
		t.Fatalf("missing clear build instructions in response: %q", body)
	}
}

func TestServeStaticFSBuildHintSupportsHead(t *testing.T) {
	assets := fstest.MapFS{"dist/.gitkeep": &fstest.MapFile{}}
	request := httptest.NewRequest(http.MethodHead, "/", nil)
	response := httptest.NewRecorder()

	if !serveStaticFS(response, request, assets) {
		t.Fatal("serveStaticFS returned false for HEAD")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("HEAD response body length = %d, want 0", response.Body.Len())
	}
}

func TestServeStaticFSUsesBuiltIndex(t *testing.T) {
	assets := fstest.MapFS{
		"dist/index.html": &fstest.MapFile{Data: []byte("built UI")},
	}
	request := httptest.NewRequest(http.MethodGet, "/library", nil)
	response := httptest.NewRecorder()

	if !serveStaticFS(response, request, assets) {
		t.Fatal("serveStaticFS returned false for the built SPA")
	}
	if response.Code != http.StatusOK || response.Body.String() != "built UI" {
		t.Fatalf("built SPA response = %d %q", response.Code, response.Body.String())
	}
}
