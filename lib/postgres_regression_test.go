//go:build integration

// Copyright (C) 2026 Pau Sanchez
//
// PostgreSQL regressions use a fresh database for each test, including history.
package lib

import (
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// newPostgresRegression
// -----------------------------------------------------------------------------
func newPostgresRegression(t *testing.T, files map[string]string) *Gofly {
	t.Helper()
	raw := os.Getenv("GOFLY_TEST_PG_URL")
	if raw == "" {
		t.Skip("GOFLY_TEST_PG_URL is not set")
	}
	user, password := os.Getenv("GOFLY_TEST_PG_USER"), os.Getenv("GOFLY_TEST_PG_PASSWORD")
	admin, err := Connect(raw, user, password, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("gofly_reg_%d", time.Now().UnixNano())
	if _, err := admin.DB().Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.DB().Exec("DROP DATABASE " + name); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(StripJDBCPrefix(raw))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	config := NewConfig()
	config.URL, config.User, config.Password = u.String(), user, password
	config.Quiet = true
	config.Locations = []string{writeFilesInDir(t, files)}
	g, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

// -----------------------------------------------------------------------------
// regressionExec
// -----------------------------------------------------------------------------
func regressionExec(t *testing.T, g *Gofly, statement string) {
	t.Helper()
	if _, err := g.Connection.DB().Exec(statement); err != nil {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// seedRegressionFlyway
// -----------------------------------------------------------------------------
func seedRegressionFlyway(t *testing.T, g *Gofly, script string) {
	t.Helper()
	source := NewSchemaHistory(g.Connection, "public", FlywayTable, "flyway")
	if err := source.Create(); err != nil {
		t.Fatal(err)
	}
	sum := ChecksumString(script)
	if err := source.Insert(g.Connection.DB(), &AppliedMigration{InstalledRank: 1, Version: mustVersion(t, "1"), Description: "base", Type: MigrationTypeSQL, Script: "V1__base.sql", Checksum: &sum, InstalledBy: "flyway", Success: true}); err != nil {
		t.Fatal(err)
	}
	regressionExec(t, g, script)
}

// -----------------------------------------------------------------------------
// TestPostgresImportFailureIsAtomicAndRetryDoesNotReplay
// -----------------------------------------------------------------------------
func TestPostgresImportFailureIsAtomicAndRetryDoesNotReplay(t *testing.T) {
	first := "CREATE TABLE IF NOT EXISTS events(v int); INSERT INTO events VALUES(1);"
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": first, "V2__next.sql": "INSERT INTO events VALUES(2);"})
	seedRegressionFlyway(t, g, first)
	blocker, err := Connect(g.Config.URL, g.Config.User, g.Config.Password, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	tx, err := blocker.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("LOCK public.flyway_schema_history IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	regressionExec(t, g, "SET statement_timeout='100ms'")
	if _, err := g.Migrate(); err == nil {
		t.Fatal("import should time out")
	}
	exists, err := g.History.Exists()
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("failed import left a history table that masks the Flyway history")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	regressionExec(t, g, "SET statement_timeout=0")
	result, err := g.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if result.MigrationsExecuted != 1 {
		t.Errorf("retry executed %d migrations, want only V2", result.MigrationsExecuted)
	}
	var rows int
	if err := g.Connection.DB().QueryRow("SELECT count(*) FROM events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("retry replayed data: got %d rows, want 2", rows)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresRefusesNonemptySchemaWithoutHistory
// -----------------------------------------------------------------------------
func TestPostgresRefusesNonemptySchemaWithoutHistory(t *testing.T) {
	for _, object := range []string{
		"CREATE TABLE existing(v int); INSERT INTO existing VALUES(1)",
		"CREATE VIEW existing AS SELECT 1 AS v",
		"CREATE TYPE existing AS ENUM ('one')",
		"CREATE FUNCTION existing() RETURNS int LANGUAGE SQL AS 'SELECT 1'",
	} {
		t.Run(object, func(t *testing.T) {
			g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE unexpected(v int);"})
			regressionExec(t, g, object)
			if _, err := g.Migrate(); err == nil {
				t.Fatal("nonempty unmanaged schema must require a baseline")
			}
			exists, err := g.History.Exists()
			if err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Error("refused migration created history")
			}
		})
	}
}
