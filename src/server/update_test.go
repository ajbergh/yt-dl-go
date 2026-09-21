package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSemanticVersionComparison(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
		ok          bool
	}{
		{"v1.2.3", "1.2.3", 0, true},
		{"1.2.3", "1.2.4", -1, true},
		{"2.0.0", "1.9.9", 1, true},
		{"1.2.3-rc.1", "1.2.3", -1, true},
		{"dev", "1.0.0", 0, false},
		{"01.2.3", "1.2.3", 0, false},
	}
	for _, test := range tests {
		got, ok := compareSemanticVersions(test.left, test.right)
		if ok != test.ok || (ok && got != test.want) {
			t.Fatalf("compareSemanticVersions(%q,%q) = %d,%v; want %d,%v", test.left, test.right, got, ok, test.want, test.ok)
		}
	}
}

func TestDevelopmentBuildSkipsExternalUpdateCheck(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	status, err := checkLatestRelease(context.Background(), server.Client(), server.URL, buildInfo{Version: "dev", Commit: "abc", Date: "now"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.DevelopmentBuild || status.UpdateAvailable || status.AutomaticUpdate || calls.Load() != 0 {
		t.Fatalf("unexpected development update status: %+v calls=%d", status, calls.Load())
	}
}

func TestLatestReleaseCheckUsesBoundedValidatedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "yt-dl-go/v1.2.3" {
			t.Fatalf("unexpected update request: method=%s accept=%q ua=%q", r.Method, r.Header.Get("Accept"), r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://github.com/ajbergh/yt-dl-go/releases/tag/v1.3.0","draft":false,"prerelease":false,"published_at":"2026-09-21T12:00:00Z","ignored":"allowed"}`))
	}))
	defer server.Close()

	status, err := checkLatestRelease(context.Background(), server.Client(), server.URL, buildInfo{Version: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.UpdateAvailable || status.LatestVersion != "v1.3.0" || status.ReleaseURL == "" || status.AutomaticUpdate {
		t.Fatalf("unexpected update status: %+v", status)
	}

	current, err := finalizeUpdateStatus(updateStatus{CurrentVersion: "v1.3.0"}, githubRelease{
		TagName: "v1.3.0", HTMLURL: "https://github.com/ajbergh/yt-dl-go/releases/tag/v1.3.0",
	})
	if err != nil || current.UpdateAvailable {
		t.Fatalf("current version incorrectly reported update: %+v %v", current, err)
	}
}

func TestUpdateMetadataRejectsUnsafeReleaseResponses(t *testing.T) {
	base := updateStatus{CurrentVersion: "v1.0.0"}
	for _, release := range []githubRelease{
		{TagName: "latest", HTMLURL: "https://github.com/ajbergh/yt-dl-go/releases/tag/latest"},
		{TagName: "v1.1.0", HTMLURL: "https://evil.invalid/ajbergh/yt-dl-go/releases/tag/v1.1.0"},
		{TagName: "v1.1.0", HTMLURL: "https://github.com/ajbergh/yt-dl-go/releases/tag/v1.1.0", Draft: true},
		{TagName: "v1.1.0", HTMLURL: "https://github.com/ajbergh/yt-dl-go/releases/tag/v1.1.0", Prerelease: true},
	} {
		if _, err := finalizeUpdateStatus(base, release); err == nil {
			t.Fatalf("accepted unsafe release metadata: %+v", release)
		}
	}
	if _, err := decodeGitHubReleaseBody(base, []byte(strings.Repeat("x", (64<<10)+1))); err == nil {
		t.Fatal("accepted oversized release response")
	}
}

func TestHealthAndUpdateExposeDevelopmentBuildMetadata(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)
	health := request(s, http.MethodGet, "/api/health", "", map[string]string{"Authorization": ""})
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"version":"dev"`) || !strings.Contains(health.Body.String(), `"commit":"unknown"`) {
		t.Fatalf("health build metadata: %d %s", health.Code, health.Body.String())
	}
	update := request(s, http.MethodGet, "/api/update", "", nil)
	if update.Code != http.StatusOK || !strings.Contains(update.Body.String(), `"developmentBuild":true`) || !strings.Contains(update.Body.String(), `"automaticUpdate":false`) {
		t.Fatalf("development update status: %d %s", update.Code, update.Body.String())
	}
}
