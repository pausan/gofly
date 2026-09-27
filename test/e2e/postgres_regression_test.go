//go:build e2e

// Copyright (C) 2026 Pau Sanchez
//
// PostgreSQL SQL compatibility and shared-history deployment regressions.
package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/pausan/gofly/lib"
)

// -----------------------------------------------------------------------------
// TestCompatPostgresSQLForms
// -----------------------------------------------------------------------------
func TestCompatPostgresSQLForms(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		if target.Dialect != lib.DialectPostgres {
			t.Skip("PostgreSQL syntax")
		}
		cases := map[string][]Fixture{
			"escape_string":    {{"V1__base.sql", `CREATE TABLE e2e_users(name text); INSERT INTO e2e_users VALUES(E'it\'s; valid');`}},
			"copy":             {{"V1__base.sql", "CREATE TABLE e2e_users(name text);\nCOPY e2e_users FROM STDIN;\nhello;world\n--data\n\\.\nINSERT INTO e2e_users VALUES('after');"}},
			"concurrent_index": {{"V1__base.sql", "CREATE TABLE e2e_users(id int);"}, {"V2__online.sql", "CREATE INDEX CONCURRENTLY e2e_idx ON e2e_users(id);"}},
			"vacuum":           {{"V1__base.sql", "CREATE TABLE e2e_users(id int);"}, {"V2__vacuum.sql", "VACUUM e2e_users;"}},
			"script_override":  {{"V1__base.sql", "CREATE TABLE e2e_users(id int); CREATE INDEX CONCURRENTLY e2e_idx ON e2e_users(id);"}, {"V1__base.sql.conf", "executeInTransaction=false\n"}},
		}
		for name, fixtures := range cases {
			t.Run(name, func(t *testing.T) {
				flyway, gofly := bothHistories(t, target, fixtures, postgresFlywayArgs(), func(g *lib.Gofly) {
					if _, err := g.Migrate(); err != nil {
						t.Fatal(err)
					}
				})
				AssertSameHistory(t, name, flyway, gofly)
			})
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatEmptySchemaIsNotBaselined
// -----------------------------------------------------------------------------
func TestCompatEmptySchemaIsNotBaselined(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		flyway, gofly := bothHistories(t, target, baseSchema(target.SQLDialect), []string{"-baselineOnMigrate=true", "migrate"}, func(g *lib.Gofly) {
			g.Config.BaselineOnMigrate = true
			if _, err := g.Migrate(); err != nil {
				t.Fatal(err)
			}
		})
		AssertSameHistory(t, "empty schema with baselineOnMigrate", flyway, gofly)
	})
}

// -----------------------------------------------------------------------------
// TestCompatSharedHistoryRoundTrip
// -----------------------------------------------------------------------------
func TestCompatSharedHistoryRoundTrip(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		w := NewWorkspace(t, target)
		w.WriteAll(baseSchema(target.SQLDialect))
		w.MustRunFlyway("-target=1", "migrate")
		g := w.Gofly(nil)
		g.Config.Target = "2"
		if _, err := g.Migrate(); err != nil {
			t.Fatal(err)
		}
		if rows := w.ReadHistory("", lib.FlywayTable); len(rows) != 2 {
			t.Fatalf("shared history has %d rows, want 2", len(rows))
		}
		w.MustRunFlyway("migrate")
		g.Config.Target = "latest"
		result, err := g.Migrate()
		if err != nil {
			t.Fatal(err)
		}
		if result.MigrationsExecuted != 0 {
			t.Fatal("round trip replayed migrations")
		}
		if rows := w.ReadHistory("", lib.FlywayTable); len(rows) != 3 {
			t.Fatalf("shared history has %d rows, want 3", len(rows))
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatConcurrentFlywayAndGoflyExecuteOnce
// -----------------------------------------------------------------------------
func TestCompatConcurrentFlywayAndGoflyExecuteOnce(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		if target.Dialect != lib.DialectPostgres {
			t.Skip("PostgreSQL advisory locking")
		}
		for _, first := range []string{"flyway", "gofly"} {
			t.Run(first, func(t *testing.T) { runConcurrentHistory(t, target, first, false) })
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatConcurrentFirstMigrationWithSharedHistory
// -----------------------------------------------------------------------------
func TestCompatConcurrentFirstMigrationWithSharedHistory(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		if target.Dialect != lib.DialectPostgres {
			t.Skip("PostgreSQL advisory locking")
		}
		for _, first := range []string{"flyway", "gofly"} {
			t.Run(first, func(t *testing.T) { runConcurrentHistory(t, target, first, true) })
		}
	})
}

// -----------------------------------------------------------------------------
// runConcurrentHistory
// -----------------------------------------------------------------------------
func runConcurrentHistory(t *testing.T, target Target, first string, fresh bool) {
	t.Helper()
	w := NewWorkspace(t, target)
	w.Write("V1__base.sql", "CREATE TABLE e2e_users(id int); CREATE SEQUENCE e2e_attempts;")
	config := w.Config()
	if fresh {
		config.GoflySchema = "public"
		config.Table = lib.FlywayTable
		w.Write("V1__base.sql", "CREATE TABLE e2e_users(id int); CREATE SEQUENCE e2e_attempts; SELECT nextval('e2e_attempts'); SELECT pg_sleep(4); INSERT INTO e2e_users VALUES(2);")
	} else {
		w.MustRunFlyway("migrate")
		w.Write("V2__slow.sql", "SELECT nextval('e2e_attempts'); SELECT pg_sleep(4); INSERT INTO e2e_users VALUES(2);")
	}
	g := w.Gofly(config)
	observer, err := lib.Connect(target.URL, target.User, target.Password, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	runFlyway := func() error {
		output, ok := w.RunFlyway(postgresFlywayArgs()...)
		if !ok {
			return &flywayRunError{output}
		}
		return nil
	}
	runGofly := func() error { _, err := g.Migrate(); return err }
	a, b := runFlyway, runGofly
	if first == "gofly" {
		a, b = b, a
	}
	done := make(chan error, 1)
	go func() { done <- a() }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var started bool
		if err := observer.DB().QueryRow("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND state='active' AND query LIKE '%pg_sleep(4)%')").Scan(&started); err != nil {
			t.Fatal(err)
		}
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first migration did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	secondErr := b()
	firstErr := <-done
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var count, attempts int
	if err := observer.DB().QueryRow("SELECT count(*) FROM e2e_users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := observer.DB().QueryRow("SELECT last_value FROM e2e_attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if count != 1 || attempts != 1 {
		t.Errorf("migration ran more than once: rows=%d, attempts=%d", count, attempts)
	}
}

type flywayRunError struct{ output string }

// -----------------------------------------------------------------------------
// Error
// -----------------------------------------------------------------------------
func (e *flywayRunError) Error() string { return e.output }

// -----------------------------------------------------------------------------
// postgresFlywayArgs
//
// Flyway 6 predates the transactional-lock option. Set the legacy flag when
// exercising that baseline; newer Flyway needs session locks for online indexes.
// -----------------------------------------------------------------------------
func postgresFlywayArgs() []string {
	if os.Getenv("GOFLY_E2E_FLYWAY_LEGACY") == "1" {
		return []string{"migrate"}
	}
	return []string{"-postgresql.transactional.lock=false", "migrate"}
}
