// Copyright (C) 2026 Pau Sanchez
//
// Undo, baseline and repair, end to end.
package lib

import (
	"io"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// undo
// -----------------------------------------------------------------------------
func (s *testSetup) undo() (*UndoResult, error) {
	s.t.Helper()

	gofly := s.open()
	defer gofly.Close()

	return gofly.Undo()
}

// -----------------------------------------------------------------------------
// TestUndoRevertsTheLastMigration
// -----------------------------------------------------------------------------
func TestUndoRevertsTheLastMigration(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("U2__b.sql", "DROP TABLE b;\n")
	setup.mustMigrate()

	result, err := setup.undo()
	if err != nil {
		t.Fatalf("undo failed: %v", err)
	}

	if result.MigrationsUndone != 1 {
		t.Errorf("undid %d migrations, want 1", result.MigrationsUndone)
	}
	if setup.tableExists("b") {
		t.Error("the undo script did not run")
	}
	if !setup.tableExists("a") {
		t.Error("undo went one migration too far")
	}
}

// -----------------------------------------------------------------------------
// TestUndoRecordsItsOwnRow
//
// Flyway records the undo as a new row of type UNDO_SQL and leaves the original
// one in place, marked as undone.
// -----------------------------------------------------------------------------
func TestUndoRecordsItsOwnRow(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("U1__a.sql", "DROP TABLE a;\n")
	setup.mustMigrate()

	if _, err := setup.undo(); err != nil {
		t.Fatalf("undo failed: %v", err)
	}

	rows := setup.history()
	if len(rows) != 2 {
		t.Fatalf("the history holds %d rows, want 2", len(rows))
	}
	if rows[1].Type != MigrationTypeUndoSQL {
		t.Errorf("the undo row has type %q, want %q", rows[1].Type, MigrationTypeUndoSQL)
	}
	if rows[1].Version.String() != "1" {
		t.Errorf("the undo row records version %q, want 1", rows[1].Version)
	}
	if rows[1].Script != "U1__a.sql" {
		t.Errorf("the undo row records script %q", rows[1].Script)
	}

	// the history row for V1 now reads as undone, while the file itself is
	// pending again so that migrate can re-apply it
	info := setup.info()
	sawUndone, sawPending := false, false
	for _, migration := range info.Infos {
		if migration.Type().IsUndo() {
			continue
		}
		if migration.Applied != nil && migration.State == StateUndone {
			sawUndone = true
		}
		if migration.Applied == nil && migration.State == StatePending {
			sawPending = true
		}
	}
	if !sawUndone {
		t.Error("the V1 history row should read as undone")
	}
	if !sawPending {
		t.Error("V1 should be pending again once it has been undone")
	}
	if info.Current.Kind() != VersionKindEmpty {
		t.Errorf("the current version is %s, want the empty schema", info.Current)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateAfterUndoReappliesTheMigration
// -----------------------------------------------------------------------------
func TestMigrateAfterUndoReappliesTheMigration(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("U1__a.sql", "DROP TABLE a;\n")
	setup.mustMigrate()

	if _, err := setup.undo(); err != nil {
		t.Fatalf("undo failed: %v", err)
	}

	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Fatalf("re-applied %d migrations, want 1", result.MigrationsExecuted)
	}
	if !setup.tableExists("a") {
		t.Error("V1 was not re-applied")
	}
	if setup.info().Current.String() != "1" {
		t.Errorf("the current version is %s, want 1", setup.info().Current)
	}
}

// -----------------------------------------------------------------------------
// TestUndoStopsWhenThereIsNoUndoScript
// -----------------------------------------------------------------------------
func TestUndoStopsWhenThereIsNoUndoScript(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.mustMigrate()

	result, err := setup.undo()
	if err != nil {
		t.Fatalf("undo failed: %v", err)
	}
	if result.MigrationsUndone != 0 {
		t.Errorf("undid %d migrations, want none", result.MigrationsUndone)
	}
	if !setup.tableExists("b") {
		t.Error("nothing should have been undone")
	}
}

// -----------------------------------------------------------------------------
// TestUndoWithTargetUndoesSeveralMigrations
// -----------------------------------------------------------------------------
func TestUndoWithTargetUndoesSeveralMigrations(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("V3__c.sql", "CREATE TABLE c (id INT);\n")
	setup.write("U2__b.sql", "DROP TABLE b;\n")
	setup.write("U3__c.sql", "DROP TABLE c;\n")
	setup.mustMigrate()

	setup.config.Target = "2"

	result, err := setup.undo()
	if err != nil {
		t.Fatalf("undo failed: %v", err)
	}
	if result.MigrationsUndone != 2 {
		t.Fatalf("undid %d migrations, want 2", result.MigrationsUndone)
	}
	if setup.tableExists("b") || setup.tableExists("c") {
		t.Error("V2 and V3 should both have been undone")
	}
	if !setup.tableExists("a") {
		t.Error("V1 has no undo script and must have been left alone")
	}
}

// -----------------------------------------------------------------------------
// TestUndoWithGroupIsAllOrNothing
// -----------------------------------------------------------------------------
func TestUndoWithGroupIsAllOrNothing(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("U1__a.sql", "DROP TABLE a;\n")
	setup.write("U2__b.sql", "THIS IS NOT SQL;\n")
	setup.mustMigrate()

	setup.config.Target = "1"
	setup.config.Group = true

	if _, err := setup.undo(); err == nil {
		t.Fatal("undo should have failed")
	}

	if !setup.tableExists("a") || !setup.tableExists("b") {
		t.Error("the whole undo batch must have been rolled back")
	}
	if len(setup.history()) != 2 {
		t.Error("no undo row should have been recorded")
	}
}

// -----------------------------------------------------------------------------
// TestInfoReportsUndoableMigrations
// -----------------------------------------------------------------------------
func TestInfoReportsUndoableMigrations(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.write("U1__a.sql", "DROP TABLE a;\n")
	setup.mustMigrate()

	table := DumpInfoTable(setup.info())

	lines := strings.Split(table, "\n")
	for _, line := range lines {
		if strings.Contains(line, "| 1 ") && !strings.Contains(line, "Yes") {
			t.Errorf("V1 has an undo script and should be listed as undoable:\n%s", line)
		}
		if strings.Contains(line, "| 2 ") && !strings.Contains(line, "No") {
			t.Errorf("V2 has no undo script:\n%s", line)
		}
	}
	if strings.Contains(table, "UNDO_SQL") {
		t.Error("undo migrations must not get a row of their own in the info table")
	}
}

// -----------------------------------------------------------------------------
// TestBaselineMarksTheDatabaseAsMigrated
// -----------------------------------------------------------------------------
func TestBaselineMarksTheDatabaseAsMigrated(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.config.BaselineVersion = "1"

	gofly := setup.open()
	gofly.Output = io.Discard
	if err := gofly.Baseline(); err != nil {
		t.Fatalf("baseline failed: %v", err)
	}
	gofly.Close()

	rows := setup.history()
	if len(rows) != 1 || rows[0].Type != MigrationTypeBaseline {
		t.Fatalf("the baseline row was not written: %+v", rows)
	}
	if rows[0].Checksum != nil {
		t.Error("a baseline row carries no checksum")
	}

	// only what sits above the baseline is applied afterwards
	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Errorf("applied %d migrations, want only V2", result.MigrationsExecuted)
	}
	if setup.tableExists("a") {
		t.Error("V1 is at the baseline and must not have run")
	}
	if !setup.tableExists("b") {
		t.Error("V2 is above the baseline and should have run")
	}
}

// -----------------------------------------------------------------------------
// TestBaselineRefusesOnAnAlreadyMigratedDatabase
// -----------------------------------------------------------------------------
func TestBaselineRefusesOnAnAlreadyMigratedDatabase(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	gofly := setup.open()
	defer gofly.Close()
	gofly.Output = io.Discard

	if err := gofly.Baseline(); err == nil {
		t.Error("baseline should refuse to run once migrations have been applied")
	}
}

// -----------------------------------------------------------------------------
// TestBaselineOnMigrate
// -----------------------------------------------------------------------------
func TestBaselineOnMigrate(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.config.BaselineOnMigrate = true
	setup.config.BaselineVersion = "1"

	setup.mustMigrate()

	if setup.tableExists("a") {
		t.Error("V1 sits at the baseline and must not have run")
	}
	if !setup.tableExists("b") {
		t.Error("V2 should have run")
	}
}

// -----------------------------------------------------------------------------
// TestRepairRemovesFailedMigrations
// -----------------------------------------------------------------------------
func TestRepairRemovesFailedMigrations(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	// a failure left behind by an engine that cannot roll DDL back
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.insertFailedRow("2", "b", "V2__b.sql")

	if _, err := setup.migrate(); err == nil {
		t.Fatal("the failed row should have blocked migrate")
	}

	gofly := setup.open()
	gofly.Output = io.Discard
	result, err := gofly.Repair()
	gofly.Close()
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if result.RemovedFailed != 1 {
		t.Errorf("removed %d failed rows, want 1", result.RemovedFailed)
	}

	// with the failure gone, V2 goes in cleanly
	if _, err := setup.migrate(); err != nil {
		t.Fatalf("migrate after repair failed: %v", err)
	}
	if !setup.tableExists("b") {
		t.Error("the fixed V2 was not applied")
	}
}

// -----------------------------------------------------------------------------
// TestRepairRealignsChecksums
// -----------------------------------------------------------------------------
func TestRepairRealignsChecksums(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	// the file is edited after the fact, which normally blocks everything
	setup.write("V1__a.sql", "CREATE TABLE a (id INT); -- reformatted\n")
	if _, err := setup.migrate(); err == nil {
		t.Fatal("the edited migration should have blocked migrate")
	}

	gofly := setup.open()
	gofly.Output = io.Discard
	result, err := gofly.Repair()
	gofly.Close()
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if result.AlignedChecksums != 1 {
		t.Errorf("aligned %d checksums, want 1", result.AlignedChecksums)
	}

	if _, err := setup.migrate(); err != nil {
		t.Fatalf("migrate after repair failed: %v", err)
	}

	rows := setup.history()
	if *rows[0].Checksum != ChecksumString("CREATE TABLE a (id INT); -- reformatted\n") {
		t.Error("the checksum was not realigned with the file")
	}
}

// -----------------------------------------------------------------------------
// TestRepairMarksMissingMigrationsAsDeleted
// -----------------------------------------------------------------------------
func TestRepairMarksMissingMigrationsAsDeleted(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.mustMigrate()

	// the migration removed is not the most recent one: Flyway cannot tell a
	// removed latest migration from one applied by a newer deployment, so that
	// case is reported as Future rather than Missing
	setup.write("V3__c.sql", "CREATE TABLE c (id INT);\n")
	setup.mustMigrate()
	setup.remove("V2__b.sql")

	gofly := setup.open()
	gofly.Output = io.Discard
	result, err := gofly.Repair()
	gofly.Close()
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if result.MarkedAsDeleted != 1 {
		t.Errorf("marked %d migrations as deleted, want 1", result.MarkedAsDeleted)
	}

	// and validation is happy again
	gofly = setup.open()
	defer gofly.Close()
	validation, err := gofly.Validate()
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if !validation.Valid() {
		t.Errorf("validation still complains: %v", validation.Error())
	}
}
