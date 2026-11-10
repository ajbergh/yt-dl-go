package main

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

func foldLibraryText(value string) string {
	return norm.NFKC.String(cases.Fold().String(norm.NFKC.String(value)))
}
func fileSearchText(raw string) string {
	var file mediaFile
	if json.Unmarshal([]byte(raw), &file) != nil {
		return ""
	}
	values := []string{file.Title, file.Author, file.Name, file.OutputName, file.Category, file.ChapterTitle}
	for _, chapter := range file.Chapters {
		values = append(values, chapter.Title)
	}
	if file.Subtitle != nil {
		values = append(values, file.Subtitle.LanguageCode, file.Subtitle.Label)
	}
	return foldLibraryText(strings.Join(values, " "))
}
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("vault_fold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return "", nil
		}
		return foldLibraryText(fmt.Sprint(args[0])), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("vault_file_text", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return "", nil
		}
		return fileSearchText(fmt.Sprint(args[0])), nil
	})
}

func (s *jobStore) migrateV33() error {
	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 33 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := configureLibrarySearch(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES(33)`); err != nil {
		return err
	}
	return tx.Commit()
}

// Keep standalone FTS contents normalized. Triggers cover every write path,
// including import, direct reconciliation updates and source-history changes.
func configureLibrarySearch(tx *sql.Tx) error {
	for _, table := range []string{"library_sources", "library_items"} {
		for _, suffix := range []string{"ai", "ad", "au"} {
			if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + table + `_search_` + suffix); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"library_search_jobs", "library_search_files"} {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			return err
		}
	}
	for _, spec := range []struct{ table, index, column, expression string }{
		{"library_sources", "library_search_jobs", "search_text", "vault_fold(%s.search_text)"},
		{"library_items", "library_search_files", "file_json", "vault_file_text(%s.file_json)"},
	} {
		if _, err := tx.Exec(`CREATE VIRTUAL TABLE ` + spec.index + ` USING fts5(` + spec.column + `,tokenize='trigram')`); err != nil {
			return err
		}
		statements := []string{
			fmt.Sprintf(`INSERT INTO %s(rowid,%s) SELECT rowid,%s FROM %s`, spec.index, spec.column, fmt.Sprintf(spec.expression, spec.table), spec.table),
			fmt.Sprintf(`CREATE TRIGGER %s_search_ai AFTER INSERT ON %s BEGIN INSERT INTO %s(rowid,%s) VALUES(new.rowid,%s); END`, spec.table, spec.table, spec.index, spec.column, fmt.Sprintf(spec.expression, "new")),
			fmt.Sprintf(`CREATE TRIGGER %s_search_ad AFTER DELETE ON %s BEGIN DELETE FROM %s WHERE rowid=old.rowid; END`, spec.table, spec.table, spec.index),
			fmt.Sprintf(`CREATE TRIGGER %s_search_au AFTER UPDATE OF %s ON %s BEGIN DELETE FROM %s WHERE rowid=old.rowid; INSERT INTO %s(rowid,%s) VALUES(new.rowid,%s); END`, spec.table, spec.column, spec.table, spec.index, spec.index, spec.column, fmt.Sprintf(spec.expression, "new")),
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	}
	return nil
}
