// Copyright (C) 2026 Pau Sanchez
//
// End to end tests running real migrations against a real SQLite database.
package lib

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testSetup is a throwaway database plus a directory of migrations
type testSetup struct {
	t         *testing.T
	dbPath    string
	locations string
	config    *Config
}

// -----------------------------------------------------------------------------
// newTestSetup
//
// Creates an empty SQLite database and an empty migrations directory.
// -----------------------------------------------------------------------------
func newTestSetup(t *testing.T) *testSetup {
	t.Helper()

	root := t.TempDir()
	locations := filepath.Join(root, "sql")
	if err := os.MkdirAll(locations, 0o755); err != nil {
		t.Fatalf("cannot create %s: %v", locations, err)
	}

	config := NewConfig()
	config.URL = "jdbc:sqlite:" + filepath.Join(root, "test.db")
	config.Locations = []string{"filesystem:" + locations}
	config.Quiet = true

	return &testSetup{
		t:         t,
		dbPath:    filepath.Join(root, "test.db"),
		locations: locations,
		config:    config,
	}
}

// -----------------------------------------------------------------------------
// write
//
// Adds a migration to the location.
// -----------------------------------------------------------------------------
func (s *testSetup) write(name string, content string) {
	s.t.Helper()

	path := filepath.Join(s.locations, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		s.t.Fatalf("cannot write %s: %v", path, err)
	}
}

// -----------------------------------------------------------------------------
// remove
// -----------------------------------------------------------------------------
func (s *testSetup) remove(name string) {
	s.t.Helper()

	if err := os.Remove(filepath.Join(s.locations, name)); err != nil {
		s.t.Fatalf("cannot remove %s: %v", name, err)
	}
}

// -----------------------------------------------------------------------------
// open
//
// Returns a Gofly bound to the test database. The caller closes it.
// -----------------------------------------------------------------------------
func (s *testSetup) open() *Gofly {
	s.t.Helper()

	gofly, err := New(s.config)
	if err != nil {
		s.t.Fatalf("cannot open gofly: %v", err)
	}
	gofly.Output = io.Discard

	return gofly
}

// -----------------------------------------------------------------------------
// migrate
//
// Runs migrate on a fresh connection and returns the result.
// -----------------------------------------------------------------------------
func (s *testSetup) migrate() (*MigrateResult, error) {
	s.t.Helper()

	gofly := s.open()
	defer gofly.Close()

	return gofly.Migrate()
}

// -----------------------------------------------------------------------------
// mustMigrate
// -----------------------------------------------------------------------------
func (s *testSetup) mustMigrate() *MigrateResult {
	s.t.Helper()

	result, err := s.migrate()
	if err != nil {
		s.t.Fatalf("migrate failed: %v", err)
	}

	return result
}

// -----------------------------------------------------------------------------
// query
//
// Runs a query against the test database with its own connection.
// -----------------------------------------------------------------------------
func (s *testSetup) query(statement string, args ...interface{}) *sql.Rows {
	s.t.Helper()

	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		s.t.Fatalf("cannot open %s: %v", s.dbPath, err)
	}
	s.t.Cleanup(func() { db.Close() })

	rows, err := db.Query(statement, args...)
	if err != nil {
		s.t.Fatalf("query %q failed: %v", statement, err)
	}

	return rows
}

// -----------------------------------------------------------------------------
// tableExists
// -----------------------------------------------------------------------------
func (s *testSetup) tableExists(name string) bool {
	s.t.Helper()

	rows := s.query(`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, name)
	defer rows.Close()

	return rows.Next()
}

// -----------------------------------------------------------------------------
// history
//
// Returns the gofly history rows.
// -----------------------------------------------------------------------------
func (s *testSetup) history() []*AppliedMigration {
	s.t.Helper()

	gofly := s.open()
	defer gofly.Close()

	exists, err := gofly.History.Exists()
	if err != nil {
		s.t.Fatalf("cannot check the history table: %v", err)
	}
	if !exists {
		return nil
	}

	rows, err := gofly.History.All()
	if err != nil {
		s.t.Fatalf("cannot read the history: %v", err)
	}

	return rows
}

// -----------------------------------------------------------------------------
// info
// -----------------------------------------------------------------------------
func (s *testSetup) info() *MigrationInfoService {
	s.t.Helper()

	gofly := s.open()
	defer gofly.Close()

	service, err := gofly.Info()
	if err != nil {
		s.t.Fatalf("cannot build the info: %v", err)
	}

	return service
}

// -----------------------------------------------------------------------------
// insertFailedRow
//
// Plants a failed migration in the history, the way an engine that cannot roll
// DDL back would leave one behind. SQLite can roll everything back, so this is
// the only way to reach that state in these tests.
// -----------------------------------------------------------------------------
func (s *testSetup) insertFailedRow(version string, description string, script string) {
	s.t.Helper()

	gofly := s.open()
	defer gofly.Close()

	rank, err := gofly.History.NextInstalledRank()
	if err != nil {
		s.t.Fatalf("cannot work out the next rank: %v", err)
	}

	parsed, parseErr := NewVersion(version)
	if parseErr != nil {
		s.t.Fatalf("cannot parse version %q: %v", version, parseErr)
	}

	checksum := int32(0)
	row := &AppliedMigration{
		InstalledRank: rank,
		Version:       parsed,
		Description:   description,
		Type:          MigrationTypeSQL,
		Script:        script,
		Checksum:      &checksum,
		InstalledBy:   "test",
		ExecutionTime: 1,
		Success:       false,
	}

	if err := gofly.History.Insert(gofly.Connection.DB(), row); err != nil {
		s.t.Fatalf("cannot plant the failed row: %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateAppliesPendingMigrations
// -----------------------------------------------------------------------------
func TestMigrateAppliesPendingMigrations(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__Create_users.sql", "CREATE TABLE users (id INTEGER PRIMARY KEY);\n")
	setup.write("V2__Create_orders.sql", "CREATE TABLE orders (id INTEGER PRIMARY KEY);\n")

	result := setup.mustMigrate()

	if result.MigrationsExecuted != 2 {
		t.Errorf("applied %d migrations, want 2", result.MigrationsExecuted)
	}
	if !setup.tableExists("users") || !setup.tableExists("orders") {
		t.Error("the migrations did not reach the database")
	}
	if !setup.tableExists(DefaultGoflyTable) {
		t.Errorf("%s was not created", DefaultGoflyTable)
	}

	rows := setup.history()
	if len(rows) != 2 {
		t.Fatalf("the history holds %d rows, want 2", len(rows))
	}
	if rows[0].Version.String() != "1" || rows[1].Version.String() != "2" {
		t.Errorf("versions recorded as %s and %s", rows[0].Version, rows[1].Version)
	}
	if rows[0].InstalledRank != 1 || rows[1].InstalledRank != 2 {
		t.Errorf("ranks recorded as %d and %d", rows[0].InstalledRank, rows[1].InstalledRank)
	}
	if rows[0].Description != "Create users" {
		t.Errorf("description recorded as %q", rows[0].Description)
	}
	if rows[0].Type != MigrationTypeSQL {
		t.Errorf("type recorded as %q", rows[0].Type)
	}
	if !rows[0].Success {
		t.Error("the migration was not recorded as successful")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRecordsFlywayCompatibleChecksums
// -----------------------------------------------------------------------------
func TestMigrateRecordsFlywayCompatibleChecksums(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__Create_users.sql", "CREATE TABLE a (id INT);\n")

	setup.mustMigrate()

	rows := setup.history()
	if rows[0].Checksum == nil {
		t.Fatal("no checksum was recorded")
	}
	if *rows[0].Checksum != -2090711421 {
		t.Errorf("checksum recorded as %d, want the flyway value -2090711421", *rows[0].Checksum)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateIsIdempotent
// -----------------------------------------------------------------------------
func TestMigrateIsIdempotent(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__Create_users.sql", "CREATE TABLE users (id INTEGER PRIMARY KEY);\n")

	setup.mustMigrate()
	second := setup.mustMigrate()

	if second.MigrationsExecuted != 0 {
		t.Errorf("the second run applied %d migrations, want 0", second.MigrationsExecuted)
	}
	if len(setup.history()) != 1 {
		t.Error("the second run wrote to the history")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRunsNewMigrationsOnly
// -----------------------------------------------------------------------------
func TestMigrateRunsNewMigrationsOnly(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	result := setup.mustMigrate()

	if result.MigrationsExecuted != 1 {
		t.Errorf("applied %d migrations, want 1", result.MigrationsExecuted)
	}
	if !setup.tableExists("b") {
		t.Error("V2 was not applied")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRefusesOnChecksumMismatch
//
// This is the check the whole tool hinges on: once a migration has run, its
// contents must never change.
// -----------------------------------------------------------------------------
func TestMigrateRefusesOnChecksumMismatch(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	// same file name, different contents
	setup.write("V1__a.sql", "CREATE TABLE a (id INT, name TEXT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")

	_, err := setup.migrate()
	if err == nil {
		t.Fatal("migrate should have refused to run")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
	if setup.tableExists("b") {
		t.Error("V2 ran even though validation failed")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRefusesWhenAnAppliedMigrationDisappears
// -----------------------------------------------------------------------------
func TestMigrateRefusesWhenAnAppliedMigrationDisappears(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	setup.remove("V1__a.sql")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")

	_, err := setup.migrate()
	if err == nil {
		t.Fatal("migrate should have refused to run")
	}
	if !strings.Contains(err.Error(), "not resolved locally") {
		t.Errorf("unexpected error: %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRefusesOnDescriptionMismatch
// -----------------------------------------------------------------------------
func TestMigrateRefusesOnDescriptionMismatch(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__Create_users.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	// a rename keeps the checksum but changes the description
	setup.remove("V1__Create_users.sql")
	setup.write("V1__Create_people.sql", "CREATE TABLE a (id INT);\n")

	_, err := setup.migrate()
	if err == nil || !strings.Contains(err.Error(), "description mismatch") {
		t.Errorf("expected a description mismatch, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateIgnoresMissingWhenAsked
// -----------------------------------------------------------------------------
func TestMigrateIgnoresMissingWhenAsked(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	setup.remove("V1__a.sql")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.config.IgnoreMissing = true

	if _, err := setup.migrate(); err != nil {
		t.Fatalf("migrate should have carried on: %v", err)
	}
	if !setup.tableExists("b") {
		t.Error("V2 was not applied")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateAppliesVersionsInOrder
// -----------------------------------------------------------------------------
func TestMigrateAppliesVersionsInOrder(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V10__c.sql", "CREATE TABLE c (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")

	setup.mustMigrate()

	rows := setup.history()
	got := []string{}
	for _, row := range rows {
		got = append(got, row.Version.String())
	}

	want := []string{"1", "2", "10"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("applied in order %v, want %v", got, want)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateIgnoresOutOfOrderMigrationsByDefault
// -----------------------------------------------------------------------------
func TestMigrateIgnoresOutOfOrderMigrationsByDefault(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V3__c.sql", "CREATE TABLE c (id INT);\n")
	setup.mustMigrate()

	// V2 shows up late
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")

	_, err := setup.migrate()
	if err == nil || !strings.Contains(err.Error(), "not applied to database") {
		t.Fatalf("expected validation to complain about the ignored migration, got %v", err)
	}
	if setup.tableExists("b") {
		t.Error("V2 must not have been applied")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateAppliesOutOfOrderWhenAllowed
// -----------------------------------------------------------------------------
func TestMigrateAppliesOutOfOrderWhenAllowed(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V3__c.sql", "CREATE TABLE c (id INT);\n")
	setup.mustMigrate()

	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.config.OutOfOrder = true

	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Fatalf("applied %d migrations, want 1", result.MigrationsExecuted)
	}
	if !setup.tableExists("b") {
		t.Error("V2 was not applied")
	}

	// and the row is flagged as having gone in out of order
	for _, info := range setup.info().Infos {
		if info.Version() != nil && info.Version().String() == "2" && info.State != StateOutOfOrder {
			t.Errorf("V2 is in state %q, want %q", info.State, StateOutOfOrder)
		}
	}
}

// -----------------------------------------------------------------------------
// TestMigrateStopsAtTarget
// -----------------------------------------------------------------------------
func TestMigrateStopsAtTarget(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("V3__c.sql", "CREATE TABLE c (id INT);\n")
	setup.config.Target = "2"

	result := setup.mustMigrate()

	if result.MigrationsExecuted != 2 {
		t.Errorf("applied %d migrations, want 2", result.MigrationsExecuted)
	}
	if setup.tableExists("c") {
		t.Error("V3 is above the target and must not have run")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateAppliesPlaceholders
// -----------------------------------------------------------------------------
func TestMigrateAppliesPlaceholders(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE ${table_name} (id INT);\n")
	setup.config.Placeholders["table_name"] = "placeholders_worked"

	setup.mustMigrate()

	if !setup.tableExists("placeholders_worked") {
		t.Error("the placeholder was not replaced")
	}

	// the checksum is computed before the replacement, so it stays stable no
	// matter which values are passed in
	rows := setup.history()
	if *rows[0].Checksum != ChecksumString("CREATE TABLE ${table_name} (id INT);\n") {
		t.Error("the checksum must be computed on the raw file, before replacement")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRunsRepeatableMigrationsAfterVersionedOnes
// -----------------------------------------------------------------------------
func TestMigrateRunsRepeatableMigrationsAfterVersionedOnes(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("R__view.sql", "CREATE VIEW v AS SELECT * FROM a;\n")

	setup.mustMigrate()

	rows := setup.history()
	if len(rows) != 2 {
		t.Fatalf("the history holds %d rows, want 2", len(rows))
	}
	if rows[0].Version == nil || rows[1].Version != nil {
		t.Error("the repeatable migration must be recorded after the versioned one")
	}
}

// -----------------------------------------------------------------------------
// TestRepeatableMigrationIsReappliedWhenItChanges
// -----------------------------------------------------------------------------
func TestRepeatableMigrationIsReappliedWhenItChanges(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT, name TEXT);\n")
	setup.write("R__view.sql", "DROP VIEW IF EXISTS v;\nCREATE VIEW v AS SELECT id FROM a;\n")
	setup.mustMigrate()

	// unchanged, so nothing to do
	if result := setup.mustMigrate(); result.MigrationsExecuted != 0 {
		t.Errorf("an unchanged repeatable migration was run again")
	}

	setup.write("R__view.sql", "DROP VIEW IF EXISTS v;\nCREATE VIEW v AS SELECT id, name FROM a;\n")

	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Fatalf("applied %d migrations, want the repeatable one", result.MigrationsExecuted)
	}
	if len(setup.history()) != 3 {
		t.Error("the re-run must add a row rather than replace one")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateWithoutGroupKeepsWhatAlreadySucceeded
//
// The default is one transaction per migration, so a failure leaves everything
// before it applied and rolls back only the migration that broke.
//
// On an engine that can roll DDL back there is deliberately no failed row: the
// changes are gone, so there is nothing to repair, and Flyway behaves the same
// way (DbMigrate only records the failure when supportsDdlTransactions() is
// false). The migration simply stays pending and is retried.
// -----------------------------------------------------------------------------
func TestMigrateWithoutGroupKeepsWhatAlreadySucceeded(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "THIS IS NOT SQL;\n")

	if _, err := setup.migrate(); err == nil {
		t.Fatal("migrate should have failed")
	}

	if !setup.tableExists("a") {
		t.Error("V1 ran before the failure and must have been kept")
	}

	rows := setup.history()
	if len(rows) != 1 {
		t.Fatalf("the history holds %d rows, want only the successful V1", len(rows))
	}
	if !rows[0].Success || rows[0].Version.String() != "1" {
		t.Errorf("the recorded row is %+v", rows[0])
	}

	// V2 is still pending, so fixing it and running again just works
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Errorf("applied %d migrations, want the fixed V2", result.MigrationsExecuted)
	}
	if !setup.tableExists("b") {
		t.Error("the fixed V2 was not applied")
	}
}

// -----------------------------------------------------------------------------
// TestAFailedRowBlocksTheNextRun
//
// Where the DDL could not be rolled back, the failed row stays behind and has
// to stop everything until someone repairs it.
// -----------------------------------------------------------------------------
func TestAFailedRowBlocksTheNextRun(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	// V2 arrives and fails on an engine that cannot roll the DDL back
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.insertFailedRow("2", "b", "V2__b.sql")

	_, err := setup.migrate()
	if err == nil || !strings.Contains(err.Error(), "Detected failed migration") {
		t.Errorf("expected the failed migration to block the next run, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateWithGroupIsAllOrNothing
//
// With -group everything runs in one transaction: a failure at the end must
// leave the database exactly as it was.
// -----------------------------------------------------------------------------
func TestMigrateWithGroupIsAllOrNothing(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("V3__c.sql", "THIS IS NOT SQL;\n")
	setup.config.Group = true

	_, err := setup.migrate()
	if err == nil {
		t.Fatal("migrate should have failed")
	}

	if setup.tableExists("a") || setup.tableExists("b") {
		t.Error("the whole batch must have been rolled back")
	}
	if rows := setup.history(); len(rows) != 0 {
		t.Errorf("the history holds %d rows, want none", len(rows))
	}
}

// -----------------------------------------------------------------------------
// TestMigrateWithGroupCommitsEverythingOnSuccess
// -----------------------------------------------------------------------------
func TestMigrateWithGroupCommitsEverythingOnSuccess(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.config.Group = true

	result := setup.mustMigrate()

	if result.MigrationsExecuted != 2 {
		t.Errorf("applied %d migrations, want 2", result.MigrationsExecuted)
	}
	if !setup.tableExists("a") || !setup.tableExists("b") {
		t.Error("the batch was not committed")
	}
	if len(setup.history()) != 2 {
		t.Error("the history was not written")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRollsBackAFailingStatementWithinOneMigration
// -----------------------------------------------------------------------------
func TestMigrateRollsBackAFailingStatementWithinOneMigration(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\nTHIS IS NOT SQL;\n")

	if _, err := setup.migrate(); err == nil {
		t.Fatal("migrate should have failed")
	}

	if setup.tableExists("a") {
		t.Error("the first statement must have been rolled back with the rest")
	}
}
