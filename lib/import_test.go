// Copyright (C) 2026 Pau Sanchez
//
// Taking over a database that was previously migrated with Flyway.
package lib

import (
	"database/sql"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// createFlywayHistory
//
// Builds a flyway_schema_history table exactly as Flyway would leave it, and
// fills it with the given rows.
// -----------------------------------------------------------------------------
func (s *testSetup) createFlywayHistory(rows []string) {
	s.t.Helper()

	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		s.t.Fatalf("cannot open %s: %v", s.dbPath, err)
	}
	defer db.Close()

	dialect := &sqliteDialect{}
	for _, statement := range dialect.CreateHistoryTableSQL("", FlywayTable) {
		if _, err := db.Exec(statement); err != nil {
			s.t.Fatalf("cannot create the flyway history table: %v", err)
		}
	}

	for _, row := range rows {
		if _, err := db.Exec(row); err != nil {
			s.t.Fatalf("cannot insert into the flyway history table: %v", err)
		}
	}
}

// -----------------------------------------------------------------------------
// flywayRow
// -----------------------------------------------------------------------------
func flywayRow(rank int, version string, description string, script string, checksum int32) string {
	versionValue := "'" + version + "'"
	if version == "" {
		versionValue = "NULL"
	}

	return `INSERT INTO "` + FlywayTable + `"
	  ("installed_rank", "version", "description", "type", "script", "checksum",
	   "installed_by", "installed_on", "execution_time", "success")
	  VALUES (` + itoa(rank) + `, ` + versionValue + `, '` + description + `', 'SQL', '` +
		script + `', ` + itoa(int(checksum)) + `, 'flyway', '2026-01-01 10:00:00.000', 12, 1)`
}

// -----------------------------------------------------------------------------
// itoa
// -----------------------------------------------------------------------------
func itoa(value int) string {
	if value < 0 {
		return "-" + itoa(-value)
	}
	if value < 10 {
		return string(rune('0' + value))
	}

	return itoa(value/10) + string(rune('0'+value%10))
}

// -----------------------------------------------------------------------------
// TestFirstRunImportsAnExistingFlywayHistory
//
// This is what makes gofly a drop in replacement: point it at a database Flyway
// has been migrating and it picks up exactly where Flyway left off.
// -----------------------------------------------------------------------------
func TestFirstRunImportsAnExistingFlywayHistory(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	secondSQL := "CREATE TABLE b (id INT);\n"

	setup.write("V1__a.sql", firstSQL)
	setup.write("V2__b.sql", secondSQL)
	setup.write("V3__c.sql", "CREATE TABLE c (id INT);\n")

	// Flyway already applied V1 and V2 against this database
	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
		flywayRow(2, "2", "b", "V2__b.sql", ChecksumString(secondSQL)),
	})
	mustExec(t, setup.dbPath, "CREATE TABLE a (id INT)")
	mustExec(t, setup.dbPath, "CREATE TABLE b (id INT)")

	result := setup.mustMigrate()

	if result.MigrationsExecuted != 1 {
		t.Fatalf("applied %d migrations, want only V3", result.MigrationsExecuted)
	}

	rows := setup.history()
	if len(rows) != 3 {
		t.Fatalf("the gofly history holds %d rows, want 3", len(rows))
	}
	if rows[0].Version.String() != "1" || rows[1].Version.String() != "2" || rows[2].Version.String() != "3" {
		t.Errorf("the imported history is out of order: %+v", rows)
	}
	if rows[0].InstalledBy != "flyway" {
		t.Errorf("the imported row lost its installed_by: %q", rows[0].InstalledBy)
	}
	if rows[0].ExecutionTime != 12 {
		t.Errorf("the imported row lost its execution_time: %d", rows[0].ExecutionTime)
	}
	if *rows[0].Checksum != ChecksumString(firstSQL) {
		t.Error("the imported checksum does not match")
	}
	if rows[0].InstalledOn.IsZero() {
		t.Error("the imported row lost its installed_on")
	}
}

// -----------------------------------------------------------------------------
// TestImportLeavesTheFlywayTableUntouched
//
// gofly only ever reads flyway_schema_history, so rolling back to Flyway stays
// possible after the switch.
// -----------------------------------------------------------------------------
func TestImportLeavesTheFlywayTableUntouched(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	setup.write("V1__a.sql", firstSQL)
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")

	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
	})
	mustExec(t, setup.dbPath, "CREATE TABLE a (id INT)")

	setup.mustMigrate()

	rows := setup.query(`SELECT COUNT(*) FROM "` + FlywayTable + `"`)
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("the flyway history table is gone")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatalf("cannot count the flyway rows: %v", err)
	}
	if count != 1 {
		t.Errorf("the flyway history now holds %d rows, want the original 1", count)
	}
}

// -----------------------------------------------------------------------------
// TestImportedChecksumMismatchStillBlocksMigrate
//
// A migration edited while Flyway was in charge has to keep failing after the
// switch, otherwise the import would quietly paper over a real problem.
// -----------------------------------------------------------------------------
func TestImportedChecksumMismatchStillBlocksMigrate(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")

	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", 12345),
	})

	_, err := setup.migrate()
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected the imported checksum to be validated, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestImportHappensOnlyOnce
// -----------------------------------------------------------------------------
func TestImportHappensOnlyOnce(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	setup.write("V1__a.sql", firstSQL)
	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
	})
	mustExec(t, setup.dbPath, "CREATE TABLE a (id INT)")

	setup.mustMigrate()
	setup.mustMigrate()
	setup.mustMigrate()

	if rows := setup.history(); len(rows) != 1 {
		t.Errorf("the history holds %d rows, want 1: the import must not repeat", len(rows))
	}
}

// -----------------------------------------------------------------------------
// TestImportCanBeTurnedOff
// -----------------------------------------------------------------------------
func TestImportCanBeTurnedOff(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	setup.write("V1__a.sql", firstSQL)
	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
	})
	setup.config.ImportFromFlyway = false

	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Errorf("applied %d migrations, want 1: without the import V1 is pending", result.MigrationsExecuted)
	}
}

// -----------------------------------------------------------------------------
// TestImportCarriesRepeatableAndBaselineRows
// -----------------------------------------------------------------------------
func TestImportCarriesRepeatableAndBaselineRows(t *testing.T) {
	setup := newTestSetup(t)

	repeatableSQL := "SELECT 1;\n"
	setup.write("R__report.sql", repeatableSQL)

	setup.createFlywayHistory([]string{
		`INSERT INTO "` + FlywayTable + `"
		  ("installed_rank", "version", "description", "type", "script", "checksum",
		   "installed_by", "installed_on", "execution_time", "success")
		  VALUES (1, '1', '<< Flyway Baseline >>', 'BASELINE', '<< Flyway Baseline >>', NULL,
		          'flyway', '2026-01-01 10:00:00.000', 0, 1)`,
		flywayRow(2, "", "report", "R__report.sql", ChecksumString(repeatableSQL)),
	})

	result := setup.mustMigrate()
	if result.MigrationsExecuted != 0 {
		t.Errorf("applied %d migrations, want none", result.MigrationsExecuted)
	}

	rows := setup.history()
	if len(rows) != 2 {
		t.Fatalf("the history holds %d rows, want 2", len(rows))
	}
	if rows[0].Type != MigrationTypeBaseline || rows[0].Checksum != nil {
		t.Errorf("the baseline row was not imported faithfully: %+v", rows[0])
	}
	if rows[1].Version != nil {
		t.Error("the repeatable row must keep its null version")
	}
}

// -----------------------------------------------------------------------------
// mustExec
// -----------------------------------------------------------------------------
func mustExec(t *testing.T, dbPath string, statement string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("cannot open %s: %v", dbPath, err)
	}
	defer db.Close()

	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("cannot run %q: %v", statement, err)
	}
}

// -----------------------------------------------------------------------------
// TestValidateReadsTheFlywayHistoryWhenGoflyHasNoneYet
//
// Running validate against a database still managed by Flyway must check the
// files against the history that is actually there, and must not create the
// gofly table or import anything: validating is a read-only question.
// -----------------------------------------------------------------------------
func TestValidateReadsTheFlywayHistoryWhenGoflyHasNoneYet(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	setup.write("V1__a.sql", firstSQL)

	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
	})

	gofly := setup.open()
	defer gofly.Close()

	result, err := gofly.Validate()
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if !result.Valid() {
		t.Errorf("V1 is applied according to the flyway history, so it should validate: %v", result.Error())
	}

	if setup.tableExists(DefaultGoflyTable) {
		t.Error("validate must not create the gofly history table")
	}
}

// -----------------------------------------------------------------------------
// TestValidateAgainstTheFlywayHistoryStillCatchesAnEditedMigration
// -----------------------------------------------------------------------------
func TestValidateAgainstTheFlywayHistoryStillCatchesAnEditedMigration(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")

	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", 999999),
	})

	gofly := setup.open()
	defer gofly.Close()

	result, err := gofly.Validate()
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if result.Valid() {
		t.Fatal("the checksum does not match, validation should have failed")
	}
	if result.Errors[0].Code != ErrorChecksumMismatch {
		t.Errorf("got %s, want a checksum mismatch", result.Errors[0].Code)
	}
	if setup.tableExists(DefaultGoflyTable) {
		t.Error("validate must not create the gofly history table")
	}
}

// -----------------------------------------------------------------------------
// TestInfoReportsTheFlywayHistoryWhenGoflyHasNoneYet
// -----------------------------------------------------------------------------
func TestInfoReportsTheFlywayHistoryWhenGoflyHasNoneYet(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	setup.write("V1__a.sql", firstSQL)
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")

	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
	})

	gofly := setup.open()
	defer gofly.Close()

	info, source, err := gofly.InfoWithSource()
	if err != nil {
		t.Fatalf("info failed: %v", err)
	}
	if source != HistorySourceFlyway {
		t.Errorf("the info came from source %v, want the flyway table", source)
	}
	if info.Current.String() != "1" {
		t.Errorf("the current version is %s, want 1", info.Current)
	}
	if len(info.Pending()) != 1 {
		t.Errorf("got %d pending migrations, want only V2", len(info.Pending()))
	}
	if setup.tableExists(DefaultGoflyTable) {
		t.Error("info must not create the gofly history table")
	}
}

// -----------------------------------------------------------------------------
// TestValidateOnAVirginDatabaseReportsPendingMigrations
// -----------------------------------------------------------------------------
func TestValidateOnAVirginDatabaseReportsPendingMigrations(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")

	gofly := setup.open()
	defer gofly.Close()

	result, err := gofly.Validate()
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if result.Valid() {
		t.Error("nothing is applied, so V1 should be reported as not applied")
	}
	if setup.tableExists(DefaultGoflyTable) {
		t.Error("validate must not create the gofly history table")
	}
}

// -----------------------------------------------------------------------------
// TestMigrateStillTakesOverAfterAReadOnlyValidate
// -----------------------------------------------------------------------------
func TestMigrateStillTakesOverAfterAReadOnlyValidate(t *testing.T) {
	setup := newTestSetup(t)

	firstSQL := "CREATE TABLE a (id INT);\n"
	setup.write("V1__a.sql", firstSQL)
	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")
	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "a", "V1__a.sql", ChecksumString(firstSQL)),
	})
	mustExec(t, setup.dbPath, "CREATE TABLE a (id INT)")

	gofly := setup.open()
	if _, err := gofly.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	gofly.Close()

	result := setup.mustMigrate()
	if result.MigrationsExecuted != 1 {
		t.Errorf("applied %d migrations, want only V2", result.MigrationsExecuted)
	}
	if len(setup.history()) != 2 {
		t.Error("migrate should have imported the flyway row and added V2")
	}
}
