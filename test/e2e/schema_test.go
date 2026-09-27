//go:build e2e

// Copyright (C) 2026 Pau Sanchez
//
// Schema checks, schema creation and transaction detection on every database.
package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/pausan/gofly/lib"
)

// -----------------------------------------------------------------------------
// flywayMigrateArgs
//
// Newer Flyway takes its PostgreSQL lock inside a transaction unless told not
// to, which rules out statements that must run outside one.
// -----------------------------------------------------------------------------
func flywayMigrateArgs(target Target, extra ...string) []string {
	args := append([]string{}, extra...)
	if target.Dialect == lib.DialectPostgres && os.Getenv("GOFLY_E2E_FLYWAY_LEGACY") != "1" {
		args = append(args, "-postgresql.transactional.lock=false")
	}
	return append(args, "migrate")
}

// -----------------------------------------------------------------------------
// nonTableObject
//
// Something that makes a schema non-empty to Flyway without being a table,
// which is where each database's check differs. SQLite only counts tables.
// -----------------------------------------------------------------------------
func nonTableObject(dialect string) string {
	switch dialect {
	case lib.DialectPostgres:
		return "CREATE TYPE e2e_mood AS ENUM ('ok')"
	case lib.DialectMysql:
		return "CREATE PROCEDURE e2e_proc() SELECT 1"
	case lib.DialectMssql:
		return "CREATE TYPE e2e_code FROM int"
	default:
		return "CREATE TABLE e2e_extra(id int)"
	}
}

// -----------------------------------------------------------------------------
// nontransactionalStatement
//
// A statement Flyway's parser for the database runs outside a transaction.
// MySQL has none.
// -----------------------------------------------------------------------------
func nontransactionalStatement(dialect string) string {
	switch dialect {
	case lib.DialectPostgres:
		return "CREATE INDEX CONCURRENTLY e2e_idx ON e2e_users(id);"
	case lib.DialectMssql:
		return "ALTER DATABASE CURRENT SET ANSI_NULL_DEFAULT OFF;"
	case lib.DialectSqlite:
		return "PRAGMA foreign_keys = OFF;"
	}
	return ""
}

// -----------------------------------------------------------------------------
// TestCompatNonEmptySchemaIsRefused
// -----------------------------------------------------------------------------
func TestCompatNonEmptySchemaIsRefused(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		fixtures := []Fixture{{"V1__base.sql", "CREATE TABLE e2e_users(id int);"}}

		flyway := NewWorkspace(t, target)
		flyway.WriteAll(fixtures)
		flyway.Exec(nonTableObject(target.Dialect))
		output, ok := flyway.RunFlyway(flywayMigrateArgs(target)...)
		if ok {
			t.Fatalf("flyway should have refused the non-empty schema:\n%s", output)
		}

		gofly := NewWorkspace(t, target)
		gofly.WriteAll(fixtures)
		gofly.Exec(nonTableObject(target.Dialect))
		_, err := gofly.Gofly(nil).Migrate()
		if err == nil {
			t.Fatal("gofly should have refused the non-empty schema")
		}
		if !strings.Contains(output, err.Error()) {
			t.Errorf("messages differ\nflyway:\n%s\ngofly:\n%s", output, err)
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatExtensionObjectsLeaveTheSchemaEmpty
// -----------------------------------------------------------------------------
func TestCompatExtensionObjectsLeaveTheSchemaEmpty(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		if target.Dialect != lib.DialectPostgres {
			t.Skip("PostgreSQL extensions")
		}
		fixtures := []Fixture{{"V1__base.sql", "CREATE TABLE e2e_users(id int);"}}

		flyway := NewWorkspace(t, target)
		flyway.WriteAll(fixtures)
		flyway.Exec("CREATE EXTENSION pgcrypto")
		flyway.MustRunFlyway(flywayMigrateArgs(target)...)
		flywayHistory := flyway.ReadHistory("", lib.FlywayTable)

		gofly := NewWorkspace(t, target)
		gofly.WriteAll(fixtures)
		gofly.Exec("CREATE EXTENSION pgcrypto")
		if _, err := gofly.Gofly(nil).Migrate(); err != nil {
			t.Fatal(err)
		}
		AssertSameHistory(t, "schema holding only an extension", flywayHistory,
			gofly.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable))
	})
}

// -----------------------------------------------------------------------------
// TestCompatSchemaCreationIsRecorded
// -----------------------------------------------------------------------------
func TestCompatSchemaCreationIsRecorded(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		if target.IsSQLite() {
			t.Skip("SQLite has no schemas to create")
		}
		fixtures := []Fixture{{"V1__base.sql", "CREATE TABLE e2e_users(id int);"}}

		flyway := NewWorkspace(t, target)
		flyway.WriteAll(fixtures)
		flyway.MustRunFlyway(flywayMigrateArgs(target, "-schemas="+appSchema)...)
		flywayHistory := flyway.ReadHistory(appSchema, lib.FlywayTable)

		gofly := NewWorkspace(t, target)
		gofly.WriteAll(fixtures)
		config := gofly.Config()
		config.Schemas = []string{appSchema}
		if _, err := gofly.Gofly(config).Migrate(); err != nil {
			t.Fatal(err)
		}

		// MySQL keeps gofly's history in the default database, the new one
		historySchema := historySchemaFor(target)
		if historySchema == "" {
			historySchema = appSchema
		}
		AssertSameHistory(t, "schema created on the first migrate", flywayHistory,
			gofly.ReadHistory(historySchema, lib.DefaultGoflyTable))
	})
}

// -----------------------------------------------------------------------------
// TestCompatNontransactionalStatements
//
// A script mixing both kinds is refused before anything runs. With mixed=true
// it runs outside a transaction, so its failure is recorded even where DDL
// could have been rolled back.
// -----------------------------------------------------------------------------
func TestCompatNontransactionalStatements(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		statement := nontransactionalStatement(target.Dialect)
		if statement == "" {
			t.Skip("Flyway runs every MySQL statement the same way")
		}

		// SQL Server statements only count separately in separate GO batches
		separator := "\n"
		if target.Dialect == lib.DialectMssql {
			separator = "\nGO\n"
		}
		type scriptCase struct {
			name   string
			script string
			mixed  bool
			fails  bool
		}
		cases := []scriptCase{
			{"mixed script refused", "CREATE TABLE e2e_extra(id int);" + separator + statement, false, true},
			{"failure outside a transaction recorded", statement + separator + "INSERT INTO e2e_missing VALUES(1);", true, true},
		}
		if target.Dialect == lib.DialectMssql {
			cases = append(cases, scriptCase{"one batch runs outside a transaction", "CREATE TABLE e2e_extra(id int);\n" + statement, false, false})
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				fixtures := []Fixture{
					{"V1__base.sql", "CREATE TABLE e2e_users(id int);"},
					{"V2__special.sql", c.script},
				}
				extra := []string{}
				if c.mixed {
					extra = append(extra, "-mixed=true")
				}

				flyway := NewWorkspace(t, target)
				flyway.WriteAll(fixtures)
				if output, ok := flyway.RunFlyway(flywayMigrateArgs(target, extra...)...); ok == c.fails {
					t.Fatalf("flyway succeeded=%t, want %t:\n%s", ok, !c.fails, output)
				}
				flywayHistory := flyway.ReadHistory("", lib.FlywayTable)

				gofly := NewWorkspace(t, target)
				gofly.WriteAll(fixtures)
				config := gofly.Config()
				config.Mixed = c.mixed
				if _, err := gofly.Gofly(config).Migrate(); (err != nil) != c.fails {
					t.Fatalf("gofly returned %v, want failure=%t", err, c.fails)
				}
				AssertSameHistory(t, c.name, flywayHistory,
					gofly.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable))
			})
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatUpgradeKeepsAnEarlierSeparateHistory
//
// A database taken over before sharing became the default has both tables.
// The newer default must carry on with gofly's own one.
// -----------------------------------------------------------------------------
func TestCompatUpgradeKeepsAnEarlierSeparateHistory(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		w := NewWorkspace(t, target)
		w.WriteAll(baseSchema(target.SQLDialect))
		w.MustRunFlyway("-target=1", "migrate")

		config := w.Config()
		config.ReuseFlywayHistory = false
		config.Target = "2"
		if _, err := w.Gofly(config).Migrate(); err != nil {
			t.Fatal(err)
		}

		result, err := w.Gofly(nil).Migrate()
		if err != nil {
			t.Fatalf("the upgraded default refused an earlier takeover: %v", err)
		}
		if result.MigrationsExecuted == 0 {
			t.Fatal("expected the remaining migrations to run")
		}
		if rows := w.ReadHistory("", lib.FlywayTable); len(rows) != 1 {
			t.Errorf("the stale Flyway table must be left alone, has %d rows", len(rows))
		}
	})
}
