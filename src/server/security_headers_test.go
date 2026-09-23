package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertSecurityHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if got := response.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, contentSecurityPolicy)
	}
	if got := response.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
}

func TestSecurityHeadersCoverStaticAPIAndTicketResponses(t *testing.T) {
	s := testServer(t, fixtureClient(1), nil)

	static := request(s, http.MethodGet, "/", "", nil)
	assertSecurityHeaders(t, static)

	api := request(s, http.MethodGet, "/api/health", "", nil)
	if api.Code != http.StatusOK {
		t.Fatalf("health response status = %d, want %d", api.Code, http.StatusOK)
	}
	assertSecurityHeaders(t, api)

	job := waitTerminal(t, s, createJob(t, s, testVideo).ID)
	if job.Status != "completed" || len(job.Files) != 1 {
		t.Fatalf("ticket fixture failed: %+v", job)
	}
	issued := request(s, http.MethodPost, "/api/jobs/"+job.ID+"/ticket", `{"fileId":"`+job.Files[0].ID+`"}`, nil)
	if issued.Code != http.StatusOK {
		t.Fatalf("ticket issue response status = %d, want %d", issued.Code, http.StatusOK)
	}
	assertSecurityHeaders(t, issued)
	var ticket struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(issued.Body.Bytes(), &ticket); err != nil || ticket.Path == "" {
		t.Fatalf("invalid ticket response %q: %v", issued.Body.String(), err)
	}

	download := request(s, http.MethodGet, ticket.Path, "", map[string]string{"Authorization": ""})
	if download.Code != http.StatusOK {
		t.Fatalf("ticket download response status = %d, want %d", download.Code, http.StatusOK)
	}
	assertSecurityHeaders(t, download)
}

func TestContentSecurityPolicyCoversRequiredAppSources(t *testing.T) {
	for _, directive := range []string{
		"default-src 'self'", "script-src 'self'", "style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https://i.ytimg.com", "media-src 'self' blob:",
		"frame-ancestors 'none'", "base-uri 'none'", "form-action 'self'",
	} {
		if !strings.Contains(contentSecurityPolicy, directive) {
			t.Errorf("Content-Security-Policy does not contain %q", directive)
		}
	}
}
