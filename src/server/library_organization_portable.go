package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// portableLibraryOrganization exports only references represented by
// library.json. Deleted/stale Library entries must not reappear as favorites
// or collection memberships when the archive is imported elsewhere.
func portableLibraryOrganization(org libraryOrganization, records []libraryExportRecord) *libraryExportOrganization {
	known := make(map[string]bool, len(records))
	for _, record := range records {
		known[record.JobID+":"+record.File.ID] = true
	}
	portable := &libraryExportOrganization{
		Favorites:   make([]string, 0),
		Collections: make([]libraryCollection, 0, len(org.Collections)),
	}
	for _, key := range org.Favorites {
		if known[key] {
			portable.Favorites = append(portable.Favorites, key)
		}
	}
	for _, collection := range org.Collections {
		copy := libraryCollection{
			ID: collection.ID, Name: collection.Name, ItemKeys: make([]string, 0),
		}
		for _, key := range collection.ItemKeys {
			if known[key] {
				copy.ItemKeys = append(copy.ItemKeys, key)
			}
		}
		portable.Collections = append(portable.Collections, copy)
	}
	return portable
}

func decodeLibraryOrganizationRow(row *sql.Row) (libraryOrganization, error) {
	var result libraryOrganization
	var favorites, collections string
	err := row.Scan(&result.Revision, &favorites, &collections)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(favorites), &result.Favorites); err != nil {
		return result, fmt.Errorf("decode stored favorites: %w", err)
	}
	if err := json.Unmarshal([]byte(collections), &result.Collections); err != nil {
		return result, fmt.Errorf("decode stored collections: %w", err)
	}
	if result.Favorites == nil {
		result.Favorites = []string{}
	}
	if result.Collections == nil {
		result.Collections = []libraryCollection{}
	}
	return result, validateLibraryOrganization(result)
}

// importLibraryDocument applies both media metadata and organization data
// inside one SQLite transaction. A conflict in either half rolls back both.
func (s *jobStore) importLibraryDocument(manifest libraryExportManifest, document libraryExportDocument) (int, bool, error) {
	if s == nil || s.db == nil {
		return 0, false, errors.New("state database is unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	count, err := s.importLibraryRecordsTx(tx, manifest, document.Records)
	if err != nil {
		return 0, false, err
	}
	organizationChanged := false
	if document.Organization != nil {
		organizationChanged, err = mergePortableLibraryOrganization(tx, document.Organization, document.Records)
		if err != nil {
			return 0, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return count, organizationChanged, nil
}

func importedCollectionID(collection libraryCollection, reserved map[string]bool) string {
	if !reserved[collection.ID] {
		return collection.ID
	}
	for salt := 0; ; salt++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("collection:%s:%s:%d", collection.ID, collection.Name, salt)))
		id := "import-" + hex.EncodeToString(sum[:])
		if !reserved[id] {
			return id
		}
	}
}

// mergePortableLibraryOrganization maps exported job:file references to the
// deterministic imported identities. Existing favorites and collections are
// never removed. Collections with the same name (case-insensitive) merge their
// memberships; otherwise an ID collision gets a deterministic new ID.
func mergePortableLibraryOrganization(tx *sql.Tx, incoming *libraryExportOrganization, records []libraryExportRecord) (bool, error) {
	source := libraryOrganization{Favorites: incoming.Favorites, Collections: incoming.Collections}
	if err := validateLibraryOrganization(source); err != nil {
		return false, badLibraryImport("Invalid portable Library organization: %v", err)
	}
	encodedSource, err := json.Marshal(incoming)
	if err != nil {
		return false, badLibraryImport("Invalid portable Library organization")
	}
	if len(encodedSource) > maxLibraryOrganizationBytes {
		return false, badLibraryImport("Portable Library organization exceeds 512 KiB")
	}
	mapped := make(map[string]string, len(records))
	for _, record := range records {
		sourceKey := record.JobID + ":" + record.File.ID
		mapped[sourceKey] = importedSourceID(record.JobID) + ":" + importedFileID(record.JobID, record.File.ID)
	}

	current, err := decodeLibraryOrganizationRow(tx.QueryRow(
		`SELECT revision,favorites_json,collections_json FROM library_organization WHERE id=1`,
	))
	if err != nil {
		return false, err
	}
	changed := false
	favorites := make(map[string]bool, len(current.Favorites))
	for _, key := range current.Favorites {
		favorites[key] = true
	}
	for _, key := range incoming.Favorites {
		dest, found := mapped[key]
		if !found || favorites[dest] {
			continue // Orphaned references are ignored, never restored.
		}
		current.Favorites = append(current.Favorites, dest)
		favorites[dest] = true
		changed = true
	}

	names := make(map[string]int, len(current.Collections))
	ids := make(map[string]bool, len(current.Collections))
	for i, collection := range current.Collections {
		names[strings.ToLower(collection.Name)] = i
		ids[collection.ID] = true
	}
	for _, collection := range incoming.Collections {
		members := make([]string, 0, len(collection.ItemKeys))
		for _, key := range collection.ItemKeys {
			if mappedKey, found := mapped[key]; found {
				members = append(members, mappedKey)
			}
		}
		nameKey := strings.ToLower(collection.Name)
		index, exists := names[nameKey]
		if !exists {
			copy := libraryCollection{
				ID: importedCollectionID(collection, ids), Name: collection.Name, ItemKeys: members,
			}
			current.Collections = append(current.Collections, copy)
			index = len(current.Collections) - 1
			names[nameKey] = index
			ids[copy.ID] = true
			changed = true
			continue
		}
		known := make(map[string]bool, len(current.Collections[index].ItemKeys))
		for _, key := range current.Collections[index].ItemKeys {
			known[key] = true
		}
		for _, key := range members {
			if known[key] {
				continue
			}
			current.Collections[index].ItemKeys = append(current.Collections[index].ItemKeys, key)
			known[key] = true
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := validateLibraryOrganization(current); err != nil {
		return false, &libraryImportConflictError{detail: "Merged Library organization exceeds supported limits"}
	}
	newFavorites, err := json.Marshal(current.Favorites)
	if err != nil {
		return false, err
	}
	newCollections, err := json.Marshal(current.Collections)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE library_organization SET revision=revision+1,favorites_json=?,collections_json=?
		WHERE id=1 AND revision=?`, string(newFavorites), string(newCollections), current.Revision)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, &libraryImportConflictError{detail: "Library organization changed during import"}
	}
	return true, nil
}
