// Copyright (C) 2026 Pau Sanchez
//
// Choosing between gofly's own history and an existing Flyway one.
package lib

import (
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// flywayRanks
//
// The installed ranks in flyway_schema_history, in order.
// -----------------------------------------------------------------------------
func (s *testSetup) flywayRanks() []int {
	s.t.Helper()

	rows := s.query(`SELECT installed_rank FROM "` + FlywayTable + `" ORDER BY installed_rank`)
	defer rows.Close()

	ranks := []int{}
	for rows.Next() {
		var rank int
		if err := rows.Scan(&rank); err != nil {
			s.t.Fatal(err)
		}
		ranks = append(ranks, rank)
	}
	return ranks
}

// -----------------------------------------------------------------------------
// takeOverWithAnImport
//
// Leaves the database the way gofly did before sharing became the default:
// Flyway applied V1, gofly imported that into its own table and applied V2.
// -----------------------------------------------------------------------------
func takeOverWithAnImport(t *testing.T) *testSetup {
	t.Helper()

	setup := newTestSetup(t)
	setup.write("V1__first.sql", "CREATE TABLE first(v int);")
	setup.write("V2__second.sql", "CREATE TABLE second(v int);")
	mustExec(t, setup.dbPath, "CREATE TABLE first(v int)")
	setup.createFlywayHistory([]string{
		flywayRow(1, "1", "first", "V1__first.sql", ChecksumString("CREATE TABLE first(v int);")),
	})
	setup.mustMigrate()

	setup.config.ReuseFlywayHistory = true
	return setup
}

// -----------------------------------------------------------------------------
// TestMigrateWritesToAnExistingFlywayHistoryByDefault
// -----------------------------------------------------------------------------
func TestMigrateWritesToAnExistingFlywayHistoryByDefault(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__first.sql", "CREATE TABLE first(v int);")
	setup.write("V2__second.sql", "CREATE TABLE second(v int);")
	mustExec(t, setup.dbPath, "CREATE TABLE first(v int)")
	setup.seedFlywayHistory([]string{
		flywayRow(1, "1", "first", "V1__first.sql", ChecksumString("CREATE TABLE first(v int);")),
	})

	result := setup.mustMigrate()

	if result.MigrationsExecuted != 1 || !setup.tableExists("second") {
		t.Fatalf("expected only V2 to run, ran %d", result.MigrationsExecuted)
	}
	if setup.tableExists(DefaultGoflyTable) {
		t.Error("sharing the Flyway history must not create gofly's own table")
	}
	if ranks := setup.flywayRanks(); len(ranks) != 2 {
		t.Errorf("the Flyway history should hold both migrations, has ranks %v", ranks)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateKeepsItsOwnHistoryWhenFlywayHoldsAnOlderCopy
//
// Every database an earlier gofly took over has both tables. Upgrading gofly
// must not stop it from migrating them.
// -----------------------------------------------------------------------------
func TestMigrateKeepsItsOwnHistoryWhenFlywayHoldsAnOlderCopy(t *testing.T) {
	setup := takeOverWithAnImport(t)
	setup.write("V3__third.sql", "CREATE TABLE third(v int);")

	result := setup.mustMigrate()

	if result.MigrationsExecuted != 1 || !setup.tableExists("third") {
		t.Fatalf("expected V3 to run, ran %d", result.MigrationsExecuted)
	}
	if rows := setup.history(); len(rows) != 3 {
		t.Errorf("gofly's history should hold three rows, has %d", len(rows))
	}
	if ranks := setup.flywayRanks(); len(ranks) != 1 {
		t.Errorf("the stale Flyway copy must be left alone, has ranks %v", ranks)
	}

	info := setup.info()
	if info.Current == nil || info.Current.String() != "3" {
		t.Errorf("info should read gofly's history, current is %v", info.Current)
	}
}

// -----------------------------------------------------------------------------
// TestMigrateRefusesHistoriesThatDiverged
//
// Flyway ran again after the import, so each table holds something the other
// lacks. Neither migrate nor info may pick one.
// -----------------------------------------------------------------------------
func TestMigrateRefusesHistoriesThatDiverged(t *testing.T) {
	setup := takeOverWithAnImport(t)
	mustExec(t, setup.dbPath, flywayRow(2, "3", "third", "V3__third.sql", 0))
	setup.write("V4__fourth.sql", "CREATE TABLE fourth(v int);")

	_, err := setup.migrate()
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("diverged histories must be refused, got %v", err)
	}
	if setup.tableExists("fourth") {
		t.Error("nothing may run while the histories disagree")
	}

	gofly := setup.open()
	defer gofly.Close()
	if _, err := gofly.Info(); err == nil {
		t.Error("info must refuse the same ambiguity migrate refuses")
	}

	setup.config.ReuseFlywayHistory = false
	if _, err := setup.migrate(); err != nil {
		t.Errorf("reuseFlywayHistory=false keeps using gofly's own table: %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestStaleCopyDetection
// -----------------------------------------------------------------------------
func TestStaleCopyDetection(t *testing.T) {
	version := func(text string) *Version {
		parsed, err := NewVersion(text)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	row := func(rank int, v string, script string, success bool) *AppliedMigration {
		return &AppliedMigration{InstalledRank: rank, Version: version(v), Script: script, Success: success}
	}
	imported := []*AppliedMigration{row(1, "1", "V1__a.sql", true), row(2, "2", "V2__b.sql", true), row(3, "3", "V3__c.sql", true)}

	cases := []struct {
		name   string
		flyway []*AppliedMigration
		stale  bool
	}{
		{"empty flyway table", nil, true},
		{"prefix of gofly's history", imported[:2], true},
		{"same version spelled differently", []*AppliedMigration{row(1, "1.0", "V1__a.sql", true)}, true},
		{"failed row at a rank gofly used for another migration", []*AppliedMigration{row(1, "1", "V1__a.sql", true), row(2, "9", "V9__x.sql", false)}, false},
		{"failed row after gofly's last one", []*AppliedMigration{row(1, "1", "V1__a.sql", true), {InstalledRank: 4, Version: version("4"), Script: "V4__d.sql"}}, false},
		{"flyway applied more", append(append([]*AppliedMigration{}, imported...), row(4, "4", "V4__d.sql", true)), false},
		{"another migration at the same rank", []*AppliedMigration{row(1, "1", "V1__a.sql", true), row(2, "5", "V5__e.sql", true)}, false},
	}
	for _, c := range cases {
		if got := isStaleCopy(c.flyway, imported); got != c.stale {
			t.Errorf("%s: got %t, want %t", c.name, got, c.stale)
		}
	}

	repaired := []*AppliedMigration{row(1, "1", "V1__a.sql", true), row(3, "3", "V3__c.sql", true)}
	failed := []*AppliedMigration{row(1, "1", "V1__a.sql", true), row(2, "2", "V2__b.sql", false), row(3, "3", "V3__c.sql", true)}
	if !isStaleCopy(failed, repaired) {
		t.Error("a failed row removed by gofly's repair still leaves a stale copy")
	}
}
