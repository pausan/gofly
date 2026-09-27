// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// runsOutsideTransaction
// -----------------------------------------------------------------------------
func runsOutsideTransaction(dialect string, statement string) bool {
	return nontransactionalKeywords(dialect, flywayKeywords(statement, dialect))
}

// -----------------------------------------------------------------------------
// TestPostgresTransactionDetectionIgnoresQuotedKeywords
// -----------------------------------------------------------------------------
func TestPostgresTransactionDetectionIgnoresQuotedKeywords(t *testing.T) {
	for _, sql := range []string{
		"VACUUM events",
		"/* header */ CREATE UNIQUE INDEX CONCURRENTLY ix ON events(v)",
		"DROP INDEX CONCURRENTLY ix",
		"REINDEX INDEX CONCURRENTLY ix",
		"CREATE DATABASE other",
		"CREATE SUBSCRIPTION sub CONNECTION 'dbname=x' PUBLICATION pub",
		"DROP SUBSCRIPTION sub",
		"DISCARD ALL",
		"REINDEX VERBOSE SCHEMA app",
		"ALTER SYSTEM SET work_mem = '8MB'",
	} {
		if !runsOutsideTransaction(DialectPostgres, sql) {
			t.Errorf("not detected: %s", sql)
		}
	}
	for _, sql := range []string{
		`SELECT 'VACUUM'`,
		`CREATE INDEX "CONCURRENTLY" ON events(v)`,
		`DO $$ BEGIN RAISE NOTICE 'VACUUM'; END $$`,
		`CREATE TABLE events(v text DEFAULT 'CONCURRENTLY')`,
		`SELECT E'it\'s; VACUUM'`,
		"DISCARD PLANS",
		"REINDEX TABLE system",
		"ALTER TYPE mood ADD VALUE 'sad'",
	} {
		if runsOutsideTransaction(DialectPostgres, sql) {
			t.Errorf("false positive: %s", sql)
		}
	}
}

// -----------------------------------------------------------------------------
// TestPostgresAddingAnEnumValueIsRecognized
//
// Whether it may run in a transaction depends on the server version, which is
// only looked up for statements matching this.
// -----------------------------------------------------------------------------
func TestPostgresAddingAnEnumValueIsRecognized(t *testing.T) {
	for _, sql := range []string{"ALTER TYPE mood ADD VALUE 'sad'", "ALTER TYPE IF EXISTS mood ADD VALUE IF NOT EXISTS 'x'"} {
		if !matchesAnyPrefix(postgresAddEnumValue, flywayKeywords(sql, DialectPostgres)) {
			t.Errorf("not recognized: %s", sql)
		}
	}
	if matchesAnyPrefix(postgresAddEnumValue, flywayKeywords("ALTER TYPE mood RENAME VALUE 'a' TO 'b'", DialectPostgres)) {
		t.Error("RENAME VALUE is transactional on every version")
	}
}

// -----------------------------------------------------------------------------
// TestSqlServerTransactionDetectionFollowsFlyway
// -----------------------------------------------------------------------------
func TestSqlServerTransactionDetectionFollowsFlyway(t *testing.T) {
	for _, sql := range []string{
		"BACKUP DATABASE app TO DISK = 'x.bak'",
		"RESTORE DATABASE app FROM DISK = 'x.bak'",
		"RECONFIGURE",
		"ALTER DATABASE CURRENT SET ANSI_NULL_DEFAULT ON",
		"CREATE DATABASE other",
		"CREATE FULLTEXT CATALOG ftc",
		"DROP FULLTEXT INDEX ON t",
		"EXEC sp_addlinkedserver @server = 'x'",
		"exec SP_SERVEROPTION 'x', 'rpc', 'true'",
	} {
		if !runsOutsideTransaction(DialectMssql, sql) {
			t.Errorf("not detected: %s", sql)
		}
	}
	for _, sql := range []string{
		"CREATE TABLE backup_log(id int)",
		"SELECT 'BACKUP'",
		"SELECT [backup] FROM t",
		"EXEC dbo.sp_addlinkedserver 'x'",
		"EXEC my_procedure",
		"UPDATE t SET v = 1 -- RESTORE",
	} {
		if runsOutsideTransaction(DialectMssql, sql) {
			t.Errorf("false positive: %s", sql)
		}
	}
}

// -----------------------------------------------------------------------------
// TestSqliteForeignKeysPragmaRunsOutsideTransaction
// -----------------------------------------------------------------------------
func TestSqliteForeignKeysPragmaRunsOutsideTransaction(t *testing.T) {
	if !runsOutsideTransaction(DialectSqlite, "PRAGMA foreign_keys = OFF") {
		t.Error("PRAGMA foreign_keys not detected")
	}
	for _, sql := range []string{"PRAGMA journal_mode = WAL", "CREATE TABLE foreign_keys(v int)", "PRAGMA main.foreign_keys = OFF"} {
		if runsOutsideTransaction(DialectSqlite, sql) {
			t.Errorf("false positive: %s", sql)
		}
	}
}

// -----------------------------------------------------------------------------
// TestMysqlHasNoNontransactionalStatements
// -----------------------------------------------------------------------------
func TestMysqlHasNoNontransactionalStatements(t *testing.T) {
	for _, sql := range []string{"CREATE DATABASE other", "BACKUP", "PRAGMA foreign_keys = OFF", "VACUUM"} {
		if runsOutsideTransaction(DialectMysql, sql) {
			t.Errorf("MySQL has no such rule, yet detected: %s", sql)
		}
	}
}

// -----------------------------------------------------------------------------
// TestFlywayKeywordsStopsAtTheCutoffAndSkipsIdentifiers
// -----------------------------------------------------------------------------
func TestFlywayKeywordsStopsAtTheCutoffAndSkipsIdentifiers(t *testing.T) {
	got := flywayKeywords(`SELECT a1, b.c, "d", N'e', (f), g FROM h`, DialectMssql)
	if want := []string{"SELECT", "G", "FROM", "H"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	long := strings.Repeat("a ", 20) + "VACUUM"
	if len(flywayKeywords(long, DialectPostgres)) != 10 {
		t.Error("Flyway only looks at the first ten keywords")
	}
	if runsOutsideTransaction(DialectPostgres, "SELECT a, b, c, d, e, f, g, h, i, j; VACUUM") {
		t.Error("a keyword past the cutoff must not count")
	}
}

// -----------------------------------------------------------------------------
// TestScriptConfigurationOverridesDetection
// -----------------------------------------------------------------------------
func TestScriptConfigurationOverridesDetection(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) string {
		path := filepath.Join(dir, "V1__x.sql.conf")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	override, err := scriptTransactionOverride(filepath.Join(dir, "missing.conf"))
	if err != nil || override != nil {
		t.Fatalf("a missing file means no override, got %v, %v", override, err)
	}

	override, err = scriptTransactionOverride(write("\ufeff# comment\n! also a comment\n\n executeInTransaction = false \n"))
	if err != nil || override == nil || *override {
		t.Fatalf("expected false, got %v, %v", override, err)
	}

	override, err = scriptTransactionOverride(write("executeInTransaction=true\n"))
	if err != nil || override == nil || !*override {
		t.Fatalf("expected true, got %v, %v", override, err)
	}

	if _, err := scriptTransactionOverride(write("encoding=UTF-8\n")); err == nil {
		t.Error("an unsupported setting must be refused")
	}
	if _, err := scriptTransactionOverride(write("executeInTransaction=perhaps\n")); err == nil {
		t.Error("a malformed boolean must be refused")
	}
}

// -----------------------------------------------------------------------------
// TestSqlServerDetectsPerBatch
//
// Flyway parses a whole GO batch as one statement, so one nontransactional
// statement takes the rest of its batch out of the transaction with it.
// -----------------------------------------------------------------------------
func TestSqlServerDetectsPerBatch(t *testing.T) {
	oneBatch := detectionUnits(SplitStatements("CREATE TABLE a(id int);\nALTER DATABASE CURRENT SET ANSI_NULL_DEFAULT OFF;", DialectMssql), DialectMssql)
	if len(oneBatch) != 1 || !runsOutsideTransaction(DialectMssql, oneBatch[0]) {
		t.Errorf("one batch should be one nontransactional unit, got %q", oneBatch)
	}

	twoBatches := detectionUnits(SplitStatements("CREATE TABLE a(id int);\nGO\nALTER DATABASE CURRENT SET ANSI_NULL_DEFAULT OFF;", DialectMssql), DialectMssql)
	if len(twoBatches) != 2 || runsOutsideTransaction(DialectMssql, twoBatches[0]) || !runsOutsideTransaction(DialectMssql, twoBatches[1]) {
		t.Errorf("GO should separate the two, got %q", twoBatches)
	}

	if units := detectionUnits(SplitStatements("CREATE TABLE a(v int);\nVACUUM;", DialectPostgres), DialectPostgres); len(units) != 2 {
		t.Errorf("elsewhere every statement is its own unit, got %q", units)
	}
}
