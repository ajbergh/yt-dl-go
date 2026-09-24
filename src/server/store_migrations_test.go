package main

import (
	"database/sql"
	"testing"
)

func TestTransactionalMigrationsCoverVersionsTwoThroughFifteen(t *testing.T) {
	if len(transactionalMigrations) != 14 {
		t.Fatalf("transactional migration count = %d, want 14", len(transactionalMigrations))
	}
	for index, migration := range transactionalMigrations {
		if want := index + 2; migration.version != want {
			t.Errorf("transactionalMigrations[%d].version = %d, want %d", index, migration.version, want)
		}
		if len(migration.statements) == 0 {
			t.Errorf("migration v%d has no statements", migration.version)
		}
	}
}

func TestApplyMigrationStatementsRollsBackSchemaAndVersionOnFailure(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`,
		`INSERT INTO schema_migrations(version) VALUES (1)`,
		`CREATE TABLE jobs(id TEXT PRIMARY KEY)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	err = (&jobStore{db: db}).applyMigration(migration{version: 2, statements: []string{
		`ALTER TABLE jobs ADD COLUMN first_change TEXT NOT NULL DEFAULT ''`,
		`THIS IS NOT VALID SQL`,
	}})
	if err == nil {
		t.Fatal("migration succeeded despite an invalid statement")
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("migration version after rollback = %d, want 1", version)
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name='first_change'`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 0 {
		t.Fatal("migration left its first schema change behind after rollback")
	}
}
