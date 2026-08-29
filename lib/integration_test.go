//go:build integration

// Copyright (C) 2026 Pau Sanchez
//
// Tests that need a real PostgreSQL, MySQL or SQL Server. They are behind the
// `integration` build tag and only run for the databases whose url is exported:
//
//	GOFLY_TEST_PG_URL="jdbc:postgresql://127.0.0.1:5432/test"
//	GOFLY_TEST_PG_USER=... GOFLY_TEST_PG_PASSWORD=...
//	GOFLY_TEST_MYSQL_URL=... GOFLY_TEST_MYSQL_USER=... GOFLY_TEST_MYSQL_PASSWORD=...
//	GOFLY_TEST_MSSQL_URL=... GOFLY_TEST_MSSQL_USER=... GOFLY_TEST_MSSQL_PASSWORD=...
//
//	go test -tags integration ./lib/
//
// See `make test-integration`, which brings the databases up for you.
package lib

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// integrationTarget is one database to run the suite against
type integrationTarget struct {
	name     string
	url      string
	user     string
	password string
}

// -----------------------------------------------------------------------------
// integrationTargets
//
// Returns the databases configured through the environment.
// -----------------------------------------------------------------------------
func integrationTargets(t *testing.T) []integrationTarget {
	targets := []integrationTarget{}

	for _, prefix := range []string{"PG", "MYSQL", "MSSQL"} {
		url := os.Getenv("GOFLY_TEST_" + prefix + "_URL")
		if url == "" {
			continue
		}
		targets = append(targets, integrationTarget{
			name:     strings.ToLower(prefix),
			url:      url,
			user:     os.Getenv("GOFLY_TEST_" + prefix + "_USER"),
			password: os.Getenv("GOFLY_TEST_" + prefix + "_PASSWORD"),
		})
	}

	if len(targets) == 0 {
		t.Skip("no GOFLY_TEST_*_URL exported, skipping the integration tests")
	}

	return targets
}

// -----------------------------------------------------------------------------
// integrationConfig
//
// Builds a configuration pointing at the target, with a migrations directory of
// its own so that runs never collide.
// -----------------------------------------------------------------------------
func integrationConfig(t *testing.T, target integrationTarget, files map[string]string) *Config {
	t.Helper()

	config := NewConfig()
	config.URL = target.url
	config.User = target.user
	config.Password = target.password
	config.Locations = []string{"filesystem:" + writeFilesInDir(t, files)}
	config.Quiet = true

	// the target database may well hold an unrelated flyway_schema_history, so
	// the import is turned off here and exercised on its own below
	config.ImportFromFlyway = false

	return config
}

// -----------------------------------------------------------------------------
// openIntegration
//
// Connects, and registers the teardown in the right order: the tables are
// dropped first, the connection is closed last. A plain `defer Close()` would
// run before t.Cleanup and leave the drops with a closed handle.
// -----------------------------------------------------------------------------
func openIntegration(t *testing.T, config *Config, tables []string) *Gofly {
	t.Helper()

	gofly, err := New(config)
	if err != nil {
		t.Fatalf("cannot connect: %v", err)
	}
	gofly.Output = io.Discard

	t.Cleanup(func() { gofly.Close() })
	t.Cleanup(func() { dropEverything(t, gofly, tables) })

	dropEverything(t, gofly, tables)

	return gofly
}

// -----------------------------------------------------------------------------
// dropEverything
//
// Leaves the target database as empty as we found it.
// -----------------------------------------------------------------------------
func dropEverything(t *testing.T, gofly *Gofly, tables []string) {
	t.Helper()

	dialect := gofly.Connection.Dialect()
	db := gofly.Connection.DB()

	for _, table := range tables {
		db.Exec("DROP TABLE IF EXISTS " + dialect.QuoteIdentifier(table))
	}

	db.Exec("DROP TABLE IF EXISTS " + gofly.History.QualifiedName())
	if gofly.History.schema != "" && dialect.SupportsSchemas() {
		db.Exec("DROP SCHEMA IF EXISTS " + dialect.QuoteIdentifier(gofly.History.schema))
	}
}

// -----------------------------------------------------------------------------
// TestIntegrationMigrateAndUndo
// -----------------------------------------------------------------------------
func TestIntegrationMigrateAndUndo(t *testing.T) {
	for _, target := range integrationTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			config := integrationConfig(t, target, map[string]string{
				"V1__Create_a.sql": "CREATE TABLE gofly_a (id INT);\n",
				"V2__Create_b.sql": "CREATE TABLE gofly_b (id INT);\n",
				"U2__Create_b.sql": "DROP TABLE gofly_b;\n",
			})

			gofly := openIntegration(t, config, []string{"gofly_a", "gofly_b"})

			result, err := gofly.Migrate()
			if err != nil {
				t.Fatalf("migrate failed: %v", err)
			}
			if result.MigrationsExecuted != 2 {
				t.Fatalf("applied %d migrations, want 2", result.MigrationsExecuted)
			}

			// the migrations must land in the schema the connection points at,
			// not in the one gofly keeps its history in
			exists, err := gofly.Connection.Dialect().TableExists(gofly.Connection.DB(), "", "gofly_a")
			if err != nil {
				t.Fatalf("cannot look up gofly_a: %v", err)
			}
			if !exists {
				t.Error("gofly_a did not land in the default schema")
			}

			undone, err := gofly.Undo()
			if err != nil {
				t.Fatalf("undo failed: %v", err)
			}
			if undone.MigrationsUndone != 1 {
				t.Errorf("undid %d migrations, want 1", undone.MigrationsUndone)
			}

			again, err := gofly.Migrate()
			if err != nil {
				t.Fatalf("migrate after undo failed: %v", err)
			}
			if again.MigrationsExecuted != 1 {
				t.Errorf("re-applied %d migrations, want 1", again.MigrationsExecuted)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestIntegrationChecksumMismatchBlocksMigrate
// -----------------------------------------------------------------------------
func TestIntegrationChecksumMismatchBlocksMigrate(t *testing.T) {
	for _, target := range integrationTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			config := integrationConfig(t, target, map[string]string{
				"V1__Create_a.sql": "CREATE TABLE gofly_a (id INT);\n",
			})

			gofly := openIntegration(t, config, []string{"gofly_a"})

			if _, err := gofly.Migrate(); err != nil {
				t.Fatalf("migrate failed: %v", err)
			}

			// edit the migration behind gofly's back
			location := strings.TrimPrefix(config.Locations[0], "filesystem:")
			path := filepath.Join(location, "V1__Create_a.sql")
			if err := os.WriteFile(path, []byte("CREATE TABLE gofly_a (id INT, x INT);\n"), 0o644); err != nil {
				t.Fatalf("cannot rewrite the migration: %v", err)
			}

			if _, err := gofly.Migrate(); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
				t.Errorf("expected a checksum mismatch, got %v", err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestIntegrationGroupRollsBackEverything
//
// MySQL commits implicitly on DDL, so it is the one database where a grouped
// migration cannot be undone. The test asserts what each engine can actually
// deliver.
// -----------------------------------------------------------------------------
func TestIntegrationGroupRollsBackEverything(t *testing.T) {
	for _, target := range integrationTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			config := integrationConfig(t, target, map[string]string{
				"V1__Create_a.sql": "CREATE TABLE gofly_a (id INT);\n",
				"V2__Broken.sql":   "THIS IS NOT SQL;\n",
			})
			config.Group = true

			gofly := openIntegration(t, config, []string{"gofly_a"})

			if _, err := gofly.Migrate(); err == nil {
				t.Fatal("migrate should have failed")
			}

			dialect := gofly.Connection.Dialect()

			applied, err := gofly.History.All()
			if err != nil {
				t.Fatalf("cannot read the history: %v", err)
			}

			exists, err := dialect.TableExists(gofly.Connection.DB(), "", "gofly_a")
			if err != nil {
				t.Fatalf("cannot look up gofly_a: %v", err)
			}

			if !dialect.SupportsDDLTransactions() {
				// MySQL commits implicitly on every DDL statement, so neither
				// the table nor the row recording it can be taken back. What
				// still has to hold is that the two agree with each other: V1
				// is applied and recorded, V2 is neither.
				if !exists {
					t.Error("the table created before the failure should still be there")
				}
				if len(applied) != 1 || applied[0].Version.String() != "1" {
					t.Errorf("the history should record V1 and nothing else, got %d rows", len(applied))
				}
				return
			}

			if len(applied) != 0 {
				t.Errorf("the history holds %d rows, want none", len(applied))
			}
			if exists {
				t.Error("the whole batch should have been rolled back")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestIntegrationHistorySchemaDoesNotCaptureMigrations
//
// PostgreSQL resolves the stock search_path of "$user", public against the
// login name, so a schema called after the user silently becomes the current
// one. gofly creates exactly such a schema, and the migrations must keep going
// where they were going before.
// -----------------------------------------------------------------------------
func TestIntegrationHistorySchemaDoesNotCaptureMigrations(t *testing.T) {
	for _, target := range integrationTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			config := integrationConfig(t, target, map[string]string{
				"V1__Create_a.sql": "CREATE TABLE gofly_a (id INT);\n",
			})
			// name the history schema after the connecting user, which is the
			// case that used to go wrong
			if config.User != "" {
				config.GoflySchema = config.User
			}

			gofly := openIntegration(t, config, []string{"gofly_a"})

			if !gofly.Connection.Dialect().SupportsSchemas() || config.GoflySchema == "" {
				t.Skip("this engine has no schemas")
			}

			if _, err := gofly.Migrate(); err != nil {
				t.Fatalf("migrate failed: %v", err)
			}

			if gofly.defaultSchema == config.GoflySchema {
				t.Errorf("the migrations were redirected into the history schema %q", gofly.defaultSchema)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// TestIntegrationImportsAFlywayHistory
//
// Builds a flyway_schema_history with Flyway's own DDL, fills it in, and checks
// that gofly picks up from there instead of re-running what is already applied.
// -----------------------------------------------------------------------------
func TestIntegrationImportsAFlywayHistory(t *testing.T) {
	for _, target := range integrationTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			firstSQL := "CREATE TABLE gofly_a (id INT);\n"

			config := integrationConfig(t, target, map[string]string{
				"V1__Create_a.sql": firstSQL,
				"V2__Create_b.sql": "CREATE TABLE gofly_b (id INT);\n",
			})
			config.ImportFromFlyway = true
			config.FlywayTable = "gofly_fake_flyway_history"

			gofly := openIntegration(t, config, []string{"gofly_a", "gofly_b"})

			dialect := gofly.Connection.Dialect()
			db := gofly.Connection.DB()

			dropFakeFlyway := func() {
				db.Exec("DROP TABLE IF EXISTS " + dialect.QuoteIdentifier(config.FlywayTable))
			}
			dropFakeFlyway()
			t.Cleanup(dropFakeFlyway)

			// pretend Flyway already applied V1 here
			for _, statement := range dialect.CreateHistoryTableSQL(gofly.defaultSchema, config.FlywayTable) {
				if _, err := db.Exec(statement); err != nil {
					t.Fatalf("cannot create the fake flyway table: %v", err)
				}
			}
			source := NewSchemaHistory(gofly.Connection, gofly.defaultSchema, config.FlywayTable, "flyway")
			checksum := ChecksumString(firstSQL)
			row := &AppliedMigration{
				InstalledRank: 1,
				Version:       mustVersion(t, "1"),
				Description:   "Create a",
				Type:          MigrationTypeSQL,
				Script:        "V1__Create_a.sql",
				Checksum:      &checksum,
				InstalledBy:   "flyway",
				ExecutionTime: 42,
				Success:       true,
			}
			if err := source.Insert(db, row); err != nil {
				t.Fatalf("cannot seed the fake flyway table: %v", err)
			}
			if _, err := db.Exec("CREATE TABLE " + dialect.QuoteIdentifier("gofly_a") + " (id INT)"); err != nil {
				t.Fatalf("cannot create gofly_a: %v", err)
			}

			result, err := gofly.Migrate()
			if err != nil {
				t.Fatalf("migrate failed: %v", err)
			}
			if result.MigrationsExecuted != 1 {
				t.Fatalf("applied %d migrations, want only V2", result.MigrationsExecuted)
			}

			applied, err := gofly.History.All()
			if err != nil {
				t.Fatalf("cannot read the history: %v", err)
			}
			if len(applied) != 2 {
				t.Fatalf("the history holds %d rows, want 2", len(applied))
			}
			if applied[0].InstalledBy != "flyway" || applied[0].ExecutionTime != 42 {
				t.Errorf("the imported row lost data: %+v", applied[0])
			}
			if applied[0].Checksum == nil || *applied[0].Checksum != checksum {
				t.Error("the imported checksum does not match")
			}
		})
	}
}
