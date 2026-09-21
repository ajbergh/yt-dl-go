package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const latestReleaseAPI = "https://api.github.com/repos/ajbergh/yt-dl-go/releases/latest"

type updateStatus struct {
	CurrentVersion   string `json:"currentVersion"`
	LatestVersion    string `json:"latestVersion,omitempty"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	DevelopmentBuild bool   `json:"developmentBuild,omitempty"`
	ReleaseURL       string `json:"releaseUrl,omitempty"`
	PublishedAt      string `json:"publishedAt,omitempty"`
	AutomaticUpdate bool   `json:"automaticUpdate"`
	Note             string `json:"note,omitempty"`
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

func releaseHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validGitHubReleaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return strings.HasPrefix(u.EscapedPath(), "/ajbergh/yt-dl-go/releases/")
}

func checkLatestRelease(ctx context.Context, client *http.Client, endpoint string, info buildInfo) (updateStatus, error) {
	status := updateStatus{
		CurrentVersion:  info.Version,
		AutomaticUpdate: false,
		Note:            "Automatic self-update is disabled until OS-native signing/notarization and rollback-safe replacement are implemented.",
	}
	if _, ok := parseSemanticVersion(info.Version); !ok {
		status.DevelopmentBuild = true
		status.Note = "Development build: external update checks are skipped."
		return status, nil
	}
	if client == nil {
		return status, errors.New("update HTTP client is unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return status, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "yt-dl-go/"+info.Version)
	resp, err := client.Do(req)
	if err != nil {
		return status, fmt.Errorf("check latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return status, fmt.Errorf("check latest release: GitHub returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return status, errors.New("latest release response could not be read")
	}
	return decodeGitHubReleaseBody(status, body)
}

func decodeGitHubReleaseBody(status updateStatus, body []byte) (updateStatus, error) {
	if len(body) == 0 || len(body) > 64<<10 {
		return status, errors.New("latest release response is empty or too large")
	}
	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return status, errors.New("latest release response is invalid JSON")
	}
	return finalizeUpdateStatus(status, release)
}

func finalizeUpdateStatus(status updateStatus, release githubRelease) (updateStatus, error) {
	if release.Draft || release.Prerelease {
		return status, errors.New("latest release endpoint returned a non-stable release")
	}
	if _, ok := parseSemanticVersion(release.TagName); !ok {
		return status, errors.New("latest release has an invalid semantic version")
	}
	if !validGitHubReleaseURL(release.HTMLURL) {
		return status, errors.New("latest release URL is not an approved project release URL")
	}
	comparison, ok := compareSemanticVersions(status.CurrentVersion, release.TagName)
	if !ok {
		return status, errors.New("cannot compare current and latest versions")
	}
	status.LatestVersion = release.TagName
	status.ReleaseURL = release.HTMLURL
	status.PublishedAt = release.PublishedAt
	status.UpdateAvailable = comparison < 0
	return status, nil
}
