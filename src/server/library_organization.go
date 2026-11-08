package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Library organization is deliberately separate from download jobs: removing
// a favorite or collection never touches the stored media or its history.
// Keeping it in SQLite makes it survive restarts and share across app windows.
type libraryCollection struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	ItemKeys []string `json:"itemKeys"`
}

type libraryOrganization struct {
	Revision    int64               `json:"revision"`
	Favorites   []string            `json:"favorites"`
	Collections []libraryCollection `json:"collections"`
}

const maxLibraryOrganizationBytes = 512 * 1024

var organizationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func (s *jobStore) migrateV32() error {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 32 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range []string{
		`CREATE TABLE library_organization (
            id INTEGER PRIMARY KEY CHECK (id=1),
            revision INTEGER NOT NULL DEFAULT 0,
            favorites_json TEXT NOT NULL DEFAULT '[]',
            collections_json TEXT NOT NULL DEFAULT '[]'
        )`,
		`INSERT INTO library_organization(id,revision,favorites_json,collections_json) VALUES (1,0,'[]','[]')`,
		`INSERT INTO schema_migrations(version) VALUES (32)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migrate library organization: %w", err)
		}
	}
	return tx.Commit()
}

func validLibraryItemKey(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func validateLibraryOrganization(org libraryOrganization) error {
	if org.Revision < 0 {
		return errors.New("invalid organization revision")
	}
	if len(org.Favorites) > 10000 || len(org.Collections) > 200 {
		return errors.New("library organization exceeds supported limits")
	}
	favorites := make(map[string]bool, len(org.Favorites))
	for _, key := range org.Favorites {
		if !validLibraryItemKey(key) || favorites[key] {
			return errors.New("invalid or duplicate favorite media key")
		}
		favorites[key] = true
	}
	ids, names := make(map[string]bool), make(map[string]bool)
	for _, collection := range org.Collections {
		name := strings.TrimSpace(collection.Name)
		normalized := strings.ToLower(name)
		if !organizationIDPattern.MatchString(collection.ID) || ids[collection.ID] ||
			name == "" || name != collection.Name || utf8.RuneCountInString(name) > 80 ||
			!utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") || names[normalized] {
			return errors.New("invalid or duplicate collection name or identifier")
		}
		ids[collection.ID], names[normalized] = true, true
		if len(collection.ItemKeys) > 10000 {
			return errors.New("collection contains too many media references")
		}
		keys := make(map[string]bool, len(collection.ItemKeys))
		for _, key := range collection.ItemKeys {
			if !validLibraryItemKey(key) || keys[key] {
				return errors.New("invalid or duplicate collection media key")
			}
			keys[key] = true
		}
	}
	return nil
}

func (s *jobStore) loadLibraryOrganization() (libraryOrganization, error) {
	return decodeLibraryOrganizationRow(s.db.QueryRow(
		`SELECT revision,favorites_json,collections_json FROM library_organization WHERE id=1`,
	))
}

func (s *jobStore) saveLibraryOrganization(org libraryOrganization) (libraryOrganization, bool, error) {
	if err := validateLibraryOrganization(org); err != nil {
		return libraryOrganization{}, false, err
	}
	favorites, err := json.Marshal(org.Favorites)
	if err != nil {
		return libraryOrganization{}, false, err
	}
	collections, err := json.Marshal(org.Collections)
	if err != nil {
		return libraryOrganization{}, false, err
	}
	res, err := s.db.Exec(`UPDATE library_organization SET favorites_json=?,collections_json=?,revision=revision+1 WHERE id=1 AND revision=?`,
		string(favorites), string(collections), org.Revision)
	if err != nil {
		return libraryOrganization{}, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return libraryOrganization{}, false, err
	}
	if affected == 0 {
		return libraryOrganization{}, true, nil
	}
	org.Revision++
	if org.Favorites == nil {
		org.Favorites = []string{}
	}
	if org.Collections == nil {
		org.Collections = []libraryCollection{}
	}
	return org, false, nil
}

func (s *server) handleLibraryOrganization(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		org, err := s.store.loadLibraryOrganization()
		if err != nil {
			fail(w, http.StatusInternalServerError, "Could not read saved library organization")
			return
		}
		reply(w, http.StatusOK, org)
	case http.MethodPut:
		var org libraryOrganization
		if !decodeWithLimit(w, r, &org, maxLibraryOrganizationBytes, "512 KiB") {
			return
		}
		if err := validateLibraryOrganization(org); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, conflict, err := s.store.saveLibraryOrganization(org)
		if err != nil {
			fail(w, http.StatusInternalServerError, "Could not save library organization")
			return
		}
		if conflict {
			fail(w, http.StatusConflict, "Library organization was changed in another window. Reload before editing.")
			return
		}
		reply(w, http.StatusOK, saved)
	default:
		fail(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
