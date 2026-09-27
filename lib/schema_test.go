// Copyright (C) 2026 Pau Sanchez
//
// Unmanaged schemas and migrations that run outside a transaction, on SQLite.
package lib

import (
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// TestMigrateRefusesANonEmptySchemaWithFlywaysMessage
// -----------------------------------------------------------------------------
func TestMigrateRefusesANonEmptySchemaWithFlywaysMessage(t *testing.T) {
	setup := newTestSetup(t)
	mustExec(t, setup.dbPath, "CREATE TABLE existing(v int)")
	setup.write("V1__first.sql", "CREATE TABLE first(v int);")

	_, err := setup.migrate()

	want := `Found non-empty schema(s) "main" but no schema history table. Use baseline() or set baselineOnMigrate to true to initialize the schema history table.`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
}

// -----------------------------------------------------------------------------
// TestSqliteSchemaWithoutTablesIsEmpty
//
// Flyway's SQLiteSchema.doEmpty only lists tables, so a view on its own does
// not make the schema non-empty.
// -----------------------------------------------------------------------------
func TestSqliteSchemaWithoutTablesIsEmpty(t *testing.T) {
	setup := newTestSetup(t)
	mustExec(t, setup.dbPath, "CREATE VIEW constant AS SELECT 1 AS v")
	setup.write("V1__first.sql", "CREATE TABLE first(v int);")

	if result := setup.mustMigrate(); result.MigrationsExecuted != 1 {
		t.Errorf("expected V1 to run, ran %d", result.MigrationsExecuted)
	}
}

// -----------------------------------------------------------------------------
// TestSkipExecutingMigrationsAcceptsANonEmptySchema
//
// Flyway skips the non-empty check when it is only recording migrations.
// -----------------------------------------------------------------------------
func TestSkipExecutingMigrationsAcceptsANonEmptySchema(t *testing.T) {
	setup := newTestSetup(t)
	mustExec(t, setup.dbPath, "CREATE TABLE first(v int)")
	setup.write("V1__first.sql", "CREATE TABLE first(v int);")
	setup.config.SkipExecutingMigrations = true

	setup.mustMigrate()

	if rows := setup.history(); len(rows) != 1 || !rows[0].Success {
		t.Errorf("expected V1 to be recorded, got %d row(s)", len(rows))
	}
}

// -----------------------------------------------------------------------------
// TestSqliteForeignKeysPragmaCannotShareAMigrationWithDDL
//
// The pragma is a no-op inside a transaction, so Flyway runs it outside one,
// and a script mixing it with anything else needs mixed=true.
// -----------------------------------------------------------------------------
func TestSqliteForeignKeysPragmaCannotShareAMigrationWithDDL(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__first.sql", "PRAGMA foreign_keys = OFF;\nCREATE TABLE first(v int);")

	_, err := setup.migrate()
	if err == nil || !strings.Contains(err.Error(), "mixes transactional and non-transactional") {
		t.Fatalf("expected the mixed script to be refused, got %v", err)
	}

	setup.config.Mixed = true
	setup.mustMigrate()
	if !setup.tableExists("first") {
		t.Error("with mixed=true the script runs")
	}
}

// -----------------------------------------------------------------------------
// TestNontransactionalFailureIsRecordedOnSqlite
//
// Outside a transaction nothing is rolled back, so the failure is recorded even
// though SQLite could have rolled the DDL back.
// -----------------------------------------------------------------------------
func TestNontransactionalFailureIsRecordedOnSqlite(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__first.sql", "CREATE TABLE first(v int);\nINSERT INTO missing VALUES(1);")
	setup.write("V1__first.sql.conf", "executeInTransaction=false\n")

	if _, err := setup.migrate(); err == nil {
		t.Fatal("the broken migration must fail")
	}

	if !setup.tableExists("first") {
		t.Error("the first statement ran outside a transaction and must remain")
	}
	rows := setup.history()
	if len(rows) != 1 || rows[0].Success || rows[0].Version.String() != "1" {
		t.Fatalf("expected one failed row for V1, got %d row(s)", len(rows))
	}
}
