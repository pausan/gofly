//go:build integration

// Copyright (C) 2026 Pau Sanchez
//
// PostgreSQL regressions use a fresh database for each test, including history.
package lib

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// newPostgresRegression
// -----------------------------------------------------------------------------
func newPostgresRegression(t *testing.T, files map[string]string, configure ...func(*Config)) *Gofly {
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
	for _, change := range configure {
		change(config)
	}
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
	if _, err := g.Connection.sessionDB().Exec(statement); err != nil {
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
	g.Config.ReuseFlywayHistory = false
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

// -----------------------------------------------------------------------------
// TestPostgresBaselineOnMigrateAppliesV1ToEmptyDatabase
// -----------------------------------------------------------------------------
func TestPostgresBaselineOnMigrateAppliesV1ToEmptyDatabase(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int);", "V2__next.sql": "INSERT INTO events VALUES(2);"})
	g.Config.BaselineOnMigrate = true
	result, err := g.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if result.MigrationsExecuted != 2 {
		t.Errorf("executed %d migrations, want 2", result.MigrationsExecuted)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresCreatesEveryConfiguredSchemaBeforeMigrating
// -----------------------------------------------------------------------------
func TestPostgresCreatesEveryConfiguredSchemaBeforeMigrating(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int); CREATE TABLE extra.other(v int);"}, func(c *Config) { c.Schemas = []string{"tenant", "extra"} })
	if _, err := g.Info(); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := g.Connection.DB().QueryRow("SELECT to_regnamespace('tenant') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("read-only preflight created application schemas")
	}
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tenant.events", "extra.other"} {
		if err := g.Connection.DB().QueryRow("SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing %s", name)
		}
	}
	if err := g.Connection.DB().QueryRow("SELECT to_regclass('public.events') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("migration fell back to public")
	}
}

// -----------------------------------------------------------------------------
// TestPostgresMigratesEscapedStringWithSemicolon
// -----------------------------------------------------------------------------
func TestPostgresMigratesEscapedStringWithSemicolon(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": `CREATE TABLE events(v text); INSERT INTO events VALUES(E'it\'s; valid'); INSERT INTO events VALUES('after');`})
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := g.Connection.DB().QueryRow("SELECT string_agg(v,',' ORDER BY v) FROM events").Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "after,it's; valid" {
		t.Errorf("unexpected data %q", text)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresMigratesNestedComments
// -----------------------------------------------------------------------------
func TestPostgresMigratesNestedComments(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int); /* outer /* inner */ ; SELECT 'ignored'; */ INSERT INTO events VALUES(42);"})
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	var value int
	if err := g.Connection.DB().QueryRow("SELECT v FROM events").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Errorf("got %d", value)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresCopyFromStdinLoadsDataAndContinues
// -----------------------------------------------------------------------------
func TestPostgresCopyFromStdinLoadsDataAndContinues(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v text);\nCOPY events FROM STDIN;\nhello;world\n--data\n\\.\nINSERT INTO events VALUES('after');"})
	result := make(chan error, 1)
	go func() { _, err := g.Migrate(); result <- err }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		// Kill only sessions in this test's private database so the regression
		// fails promptly even when the driver waits indefinitely for COPY input.
		killer, err := Connect(g.Config.URL, g.Config.User, g.Config.Password, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = killer.DB().Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()")
		killer.Close()
		if err != nil {
			t.Fatal(err)
		}
		<-result
		t.Fatal("COPY FROM STDIN hung instead of consuming its payload")
	}
	var rows int
	if err := g.Connection.DB().QueryRow("SELECT count(*) FROM events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Errorf("copied %d rows, want 3", rows)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresCopyFailureRollsBackDDLAndData
// -----------------------------------------------------------------------------
func TestPostgresCopyFailureRollsBackDDLAndData(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprint(group), func(t *testing.T) {
			g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int PRIMARY KEY);\nCOPY events FROM STDIN;\n1\n1\n\\.\n"})
			g.Config.Group = group
			if _, err := g.Migrate(); err == nil {
				t.Fatal("duplicate COPY input should fail")
			}
			var exists bool
			if err := g.Connection.DB().QueryRow("SELECT to_regclass('events') IS NOT NULL").Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Fatal("failed COPY left application DDL behind")
			}
			rows, err := g.History.All()
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Fatal("failed transactional COPY was recorded")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestPostgresRunsNontransactionalMigrations
// -----------------------------------------------------------------------------
func TestPostgresRunsNontransactionalMigrations(t *testing.T) {
	for _, statement := range []string{"CREATE INDEX CONCURRENTLY events_idx ON events(v);", "VACUUM events;"} {
		t.Run(statement, func(t *testing.T) {
			g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int);", "V2__online.sql": statement})
			if _, err := g.Migrate(); err != nil {
				t.Fatal(err)
			}
			if _, err := g.Migrate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestPostgresHonorsNontransactionalScriptConfiguration
// -----------------------------------------------------------------------------
func TestPostgresHonorsNontransactionalScriptConfiguration(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int); CREATE INDEX CONCURRENTLY events_idx ON events(v);", "V1__base.sql.conf": "executeInTransaction=false\n"})
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresMixedMigrationsRequireExplicitConsent
// -----------------------------------------------------------------------------
func TestPostgresMixedMigrationsRequireExplicitConsent(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprint(group), func(t *testing.T) {
			files := map[string]string{"V1__base.sql": "CREATE TABLE events(v int); CREATE INDEX CONCURRENTLY events_idx ON events(v);"}
			if group {
				files = map[string]string{"V1__base.sql": "CREATE TABLE events(v int);", "V2__online.sql": "CREATE INDEX CONCURRENTLY events_idx ON events(v);"}
			}
			g := newPostgresRegression(t, files)
			g.Config.Group = group
			if _, err := g.Migrate(); err == nil {
				t.Fatal("mixed transaction modes were not rejected")
			}
			var exists bool
			if err := g.Connection.DB().QueryRow("SELECT to_regclass('events') IS NOT NULL").Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Fatal("mixed-mode rejection changed the database")
			}
			g.Config.Mixed = true
			if _, err := g.Migrate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestPostgresRecordsFailuresOnlyWhenChangesCanRemain
// -----------------------------------------------------------------------------
func TestPostgresRecordsFailuresOnlyWhenChangesCanRemain(t *testing.T) {
	for _, transactional := range []bool{false, true} {
		t.Run(fmt.Sprint(transactional), func(t *testing.T) {
			g := newPostgresRegression(t, map[string]string{
				"V1__base.sql":      "CREATE TABLE events(v int PRIMARY KEY); INSERT INTO events VALUES(1); INSERT INTO events VALUES(1);",
				"V1__base.sql.conf": fmt.Sprintf("executeInTransaction=%t\n", transactional),
			})
			if _, err := g.Migrate(); err == nil {
				t.Fatal("duplicate data should fail")
			}
			rows, err := g.History.All()
			if err != nil {
				t.Fatal(err)
			}
			if transactional && len(rows) != 0 {
				t.Fatal("rolled back failure was recorded")
			}
			if !transactional && (len(rows) != 1 || rows[0].Success) {
				t.Fatal("nontransactional failure was not recorded")
			}
			var exists bool
			if err := g.Connection.DB().QueryRow("SELECT to_regclass('events') IS NOT NULL").Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists == transactional {
				t.Fatalf("transactional=%v, remaining table=%v", transactional, exists)
			}
			if !transactional {
				if _, err := g.Migrate(); err == nil {
					t.Fatal("failed migration did not block retry")
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestPostgresConcurrentMigratorsExecuteSQLOnce
// -----------------------------------------------------------------------------
func TestPostgresConcurrentMigratorsExecuteSQLOnce(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprint(fresh), func(t *testing.T) {
			files := map[string]string{"V1__base.sql": "CREATE TABLE events(v int); CREATE SEQUENCE attempts;"}
			g := newPostgresRegression(t, files)
			if !fresh {
				if _, err := g.Migrate(); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(g.Config.Locations[0]+"/V2__slow.sql", []byte("SELECT nextval('attempts'); SELECT pg_sleep(0.3); INSERT INTO events VALUES(2);"), 0600); err != nil {
				t.Fatal(err)
			}
			peer, err := New(g.Config)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			start := make(chan struct{})
			results := make(chan error, 2)
			for _, runner := range []*Gofly{g, peer} {
				go func(r *Gofly) { <-start; _, err := r.Migrate(); results <- err }(runner)
			}
			close(start)
			for i := 0; i < 2; i++ {
				if err := <-results; err != nil {
					t.Errorf("concurrent migrate: %v", err)
				}
			}
			var attempts int
			if err := g.Connection.DB().QueryRow("SELECT last_value FROM attempts").Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts != 1 {
				t.Errorf("migration SQL executed %d times", attempts)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestPostgresMutatingCommandsWaitForAndReleaseTheHistoryLock
// -----------------------------------------------------------------------------
func TestPostgresMutatingCommandsWaitForAndReleaseTheHistoryLock(t *testing.T) {
	for _, command := range []string{"migrate", "undo", "repair", "baseline", "history"} {
		t.Run(command, func(t *testing.T) {
			g := newPostgresRegression(t, map[string]string{})
			owner, err := Connect(g.Config.URL, g.Config.User, g.Config.Password, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			key := postgresHistoryLockKey(g.History.QualifiedName())
			if _, err := owner.DB().Exec("SELECT pg_advisory_lock($1)", key); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch command {
				case "migrate":
					_, err = g.Migrate()
				case "undo":
					_, err = g.Undo()
				case "repair":
					_, err = g.Repair()
				case "baseline":
					err = g.Baseline()
				case "history":
					err = g.EnsureHistory()
				}
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("command bypassed lock: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := owner.DB().Exec("SELECT pg_advisory_unlock($1)", key); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var available bool
			if err := owner.DB().QueryRow("SELECT pg_try_advisory_lock($1)", key).Scan(&available); err != nil {
				t.Fatal(err)
			}
			if !available {
				t.Error("command leaked its lock")
			}
			if _, err := owner.DB().Exec("SELECT pg_advisory_unlock($1)", key); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestPostgresWaitingMigratorRevalidatesConflictingFiles
// -----------------------------------------------------------------------------
func TestPostgresWaitingMigratorRevalidatesConflictingFiles(t *testing.T) {
	first := "CREATE TABLE events(v int); CREATE SEQUENCE attempts;"
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": first})
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.Config.Locations[0]+"/V2__slow.sql", []byte("SELECT nextval('attempts'); SELECT pg_sleep(0.5); INSERT INTO events VALUES(2);"), 0600); err != nil {
		t.Fatal(err)
	}
	config := *g.Config
	config.Locations = []string{writeFilesInDir(t, map[string]string{"V1__base.sql": first, "V2__slow.sql": "SELECT nextval('attempts'); INSERT INTO events VALUES(200);"})}
	peer, err := New(&config)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	done := make(chan error, 1)
	go func() { _, err := g.Migrate(); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var started bool
		if err := g.Connection.DB().QueryRow("SELECT is_called FROM attempts").Scan(&started); err != nil {
			t.Fatal(err)
		}
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first runner did not start")
		}
		time.Sleep(time.Millisecond)
	}
	_, peerErr := peer.Migrate()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if peerErr == nil {
		t.Fatal("waiting migration did not revalidate its conflicting checksum")
	}
	var attempts int
	if err := g.Connection.DB().QueryRow("SELECT last_value FROM attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Errorf("conflicting SQL executed: attempts=%d", attempts)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresReusesExistingFlywayHistoryForFutureRuns
// -----------------------------------------------------------------------------
func TestPostgresReusesExistingFlywayHistoryForFutureRuns(t *testing.T) {
	first := "CREATE TABLE events(v int); INSERT INTO events VALUES(1);"
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": first, "V2__next.sql": "INSERT INTO events VALUES(2);"})
	seedRegressionFlyway(t, g, first)
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	source := NewSchemaHistory(g.Connection, "public", FlywayTable, "flyway")
	rows, err := source.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("Flyway history is stale: got %d rows, want 2", len(rows))
	}
	peer, err := New(g.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	result, err := peer.Migrate()
	if err != nil {
		t.Fatal(err)
	}
	if result.MigrationsExecuted != 0 {
		t.Fatal("subsequent process replayed a migration")
	}
}

// -----------------------------------------------------------------------------
// TestPostgresRefusesToGuessBetweenExistingHistories
// -----------------------------------------------------------------------------
func TestPostgresRefusesToGuessBetweenExistingHistories(t *testing.T) {
	first := "CREATE TABLE events(v int); INSERT INTO events VALUES(1);"
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": first, "V2__next.sql": "INSERT INTO events VALUES(2);"})
	seedRegressionFlyway(t, g, first)
	if err := g.History.Create(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Migrate(); err == nil {
		t.Fatal("two existing histories were silently accepted")
	}
	var count int
	if err := g.Connection.DB().QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("ambiguous history caused data changes")
	}
}

// -----------------------------------------------------------------------------
// TestPostgresSessionLossAbortsRatherThanReconnectingWithoutALock
// -----------------------------------------------------------------------------
func TestPostgresSessionLossAbortsRatherThanReconnectingWithoutALock(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int);"})
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.Config.Locations[0]+"/V2__slow.sql", []byte("INSERT INTO events VALUES(2); SELECT pg_sleep(0.5);"), 0600); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := g.Connection.sessionDB().QueryRow("SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := g.Migrate(); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var sleeping bool
		if err := g.Connection.DB().QueryRow("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state='active' AND query LIKE '%pg_sleep%')", pid).Scan(&sleeping); err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := g.Connection.DB().Exec("SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("terminated migration reported success")
	}
	var rows int
	if err := g.Connection.DB().QueryRow("SELECT count(*) FROM public.events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatal("terminated transaction committed data")
	}
	if _, err := g.Migrate(); err == nil {
		t.Fatal("broken pinned session silently reconnected")
	}
	peer, err := New(g.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err := peer.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := peer.Connection.DB().QueryRow("SELECT count(*) FROM public.events").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("retry left %d rows", rows)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresObjectsOwnedByAnExtensionLeaveTheSchemaEmpty
//
// Flyway's PostgreSQLSchema.doEmpty skips extension objects, so a database
// that only has pgcrypto or PostGIS installed is migrated, not refused.
// -----------------------------------------------------------------------------
func TestPostgresObjectsOwnedByAnExtensionLeaveTheSchemaEmpty(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int);"})
	regressionExec(t, g, "CREATE EXTENSION pgcrypto")
	if _, err := g.Migrate(); err != nil {
		t.Fatalf("an extension alone must not make the schema non-empty: %v", err)
	}

	peer := newPostgresRegression(t, map[string]string{"V1__base.sql": "CREATE TABLE events(v int);"})
	regressionExec(t, peer, "CREATE TYPE mood AS ENUM ('ok')")
	if _, err := peer.Migrate(); err == nil || !strings.Contains(err.Error(), `Found non-empty schema(s) "public"`) {
		t.Fatalf("a user type makes the schema non-empty, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestPostgresRecordsTheSchemaCreationMarker
//
// Flyway writes a SCHEMA row at rank 0 for the schemas it created, which also
// must not stop a later baseline.
// -----------------------------------------------------------------------------
func TestPostgresRecordsTheSchemaCreationMarker(t *testing.T) {
	g := newPostgresRegression(t, map[string]string{"V2__next.sql": "CREATE TABLE events(v int);"}, func(c *Config) {
		c.Schemas = []string{"tenant", "extra"}
		c.BaselineVersion = "1"
	})
	if err := g.Baseline(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Migrate(); err != nil {
		t.Fatal(err)
	}
	rows, err := g.History.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected marker, baseline and V2, got %d row(s)", len(rows))
	}
	marker := rows[0]
	if marker.InstalledRank != 0 || marker.Type != MigrationTypeSchema || marker.Version != nil ||
		marker.Description != "<< Flyway Schema Creation >>" || marker.Script != `"tenant","extra"` {
		t.Errorf("unexpected marker %+v", marker)
	}
	if rows[1].Type != MigrationTypeBaseline || rows[1].InstalledRank != 1 {
		t.Errorf("the baseline must follow the marker at rank 1, got %+v", rows[1])
	}
}
