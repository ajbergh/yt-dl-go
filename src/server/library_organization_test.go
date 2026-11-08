package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryOrganizationPersistsThroughSQLiteRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := persistentTestConfig(t, root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	first := request(s, http.MethodGet, "/api/library/organization", "", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("initial GET: %d %s", first.Code, first.Body.String())
	}
	var initial libraryOrganization
	if err := json.Unmarshal(first.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 0 || len(initial.Collections) != 0 || len(initial.Favorites) != 0 {
		t.Fatalf("unexpected initial organization: %+v", initial)
	}
	payload := `{"revision":0,"favorites":["job-1:file-1"],"collections":[{"id":"c_example","name":"Watch Later","itemKeys":["job-1:file-1"]}]}`
	put := request(s, http.MethodPut, "/api/library/organization", payload, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("write organization: %d %s", put.Code, put.Body.String())
	}
	if !strings.Contains(put.Body.String(), `"revision":1`) {
		t.Fatalf("expected bumped revision: %s", put.Body.String())
	}
	s.stop()

	s, err = newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.stop()
	again := request(s, http.MethodGet, "/api/library/organization", "", nil)
	var loaded libraryOrganization
	if err := json.Unmarshal(again.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if again.Code != http.StatusOK || loaded.Revision != 1 || len(loaded.Favorites) != 1 ||
		len(loaded.Collections) != 1 || loaded.Collections[0].Name != "Watch Later" {
		t.Fatalf("organization did not survive restart: %d %+v", again.Code, loaded)
	}
	if _, err := os.Stat(filepath.Join(root, "state.db")); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryOrganizationRejectsStaleWritesAndInvalidData(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := persistentTestConfig(t, root)
	s, err := newServer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.stop()

	if r := request(s, http.MethodPut, "/api/library/organization",
		`{"revision":0,"favorites":["one:two"],"collections":[]}`, nil); r.Code != http.StatusOK {
		t.Fatalf("initial write: %d %s", r.Code, r.Body.String())
	}
	stale := request(s, http.MethodPut, "/api/library/organization",
		`{"revision":0,"favorites":["other:entry"],"collections":[]}`, nil)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale write = %d, want 409: %s", stale.Code, stale.Body.String())
	}
	for _, body := range []string{
		`{"revision":1,"favorites":["same:key","same:key"],"collections":[]}`,
		`{"revision":1,"favorites":[],"collections":[{"id":"invalid id","name":"Test","itemKeys":[]}]}`,
		`{"revision":1,"favorites":[],"collections":[{"id":"a","name":"Travel","itemKeys":[]},{"id":"b","name":"travel","itemKeys":[]}]}`,
		`{"revision":1,"favorites":[],"collections":[{"id":"a","name":"Test","itemKeys":["one:two","one:two"]}]}`,
		`{"revision":1,"favorites":[],"collections":[],"unexpected":true}`,
	} {
		if r := request(s, http.MethodPut, "/api/library/organization", body, nil); r.Code != http.StatusBadRequest {
			t.Fatalf("invalid write = %d, want 400: %s", r.Code, body)
		}
	}
	unchanged := request(s, http.MethodGet, "/api/library/organization", "", nil)
	var org libraryOrganization
	if err := json.Unmarshal(unchanged.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	if org.Revision != 1 || len(org.Favorites) != 1 || org.Favorites[0] != "one:two" {
		t.Fatalf("invalid request mutated organization: %+v", org)
	}
	if r := request(s, http.MethodPost, "/api/library/organization", `{}`, nil); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected write method status: %d", r.Code)
	}
	if r := request(s, http.MethodGet, "/api/library/organization", "", map[string]string{"Authorization": "Bearer wrong"}); r.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized request status = %d", r.Code)
	}
}
