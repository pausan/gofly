//go:build e2e

// Copyright (C) 2026 Pau Sanchez
//
// The compatibility suite.
//
// Every scenario runs the same migrations twice against the same engine, once
// with real Flyway in a container and once with gofly, and then compares the
// schema history tables the two produced. Anything that drifts apart shows up
// here as a diff, which is the whole point: it is a regression net for the one
// property this project sells, that gofly and Flyway agree.
//
// Scenarios that Flyway's community edition cannot do, undo above all, are
// asserted against gofly on its own and say so.
package e2e

import (
	"strings"
	"testing"

	"github.com/pausan/gofly/lib"
)

// -----------------------------------------------------------------------------
// eachTarget
//
// Runs a scenario against every configured database.
// -----------------------------------------------------------------------------
func eachTarget(t *testing.T, scenario func(*testing.T, Target)) {
	t.Helper()

	for _, target := range Targets(t) {
		t.Run(target.Name, func(t *testing.T) {
			scenario(t, target)
		})
	}
}

// -----------------------------------------------------------------------------
// eachComparableTarget
//
// Runs a scenario only where real Flyway can be reached, so that the two tools
// can be compared.
// -----------------------------------------------------------------------------
func eachComparableTarget(t *testing.T, scenario func(*testing.T, Target)) {
	t.Helper()

	eachTarget(t, func(t *testing.T, target Target) {
		if !target.CanRunFlyway() {
			t.Skipf("no flyway url configured for %s", target.Name)
		}
		scenario(t, target)
	})
}

// -----------------------------------------------------------------------------
// bothHistories
//
// Runs `flywayArgs` with real Flyway and `run` with gofly, from the same clean
// starting point, and returns the two schema histories.
// -----------------------------------------------------------------------------
func bothHistories(
	t *testing.T,
	target Target,
	fixtures []Fixture,
	flywayArgs []string,
	run func(*lib.Gofly),
) ([]HistoryRow, []HistoryRow) {
	t.Helper()

	// ---- real flyway -------------------------------------------------------
	flywayWorkspace := NewWorkspace(t, target)
	flywayWorkspace.WriteAll(fixtures)
	flywayWorkspace.MustRunFlyway(flywayArgs...)
	flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

	// ---- gofly, from the same clean slate ----------------------------------
	goflyWorkspace := NewWorkspace(t, target)
	goflyWorkspace.WriteAll(fixtures)
	run(goflyWorkspace.Gofly(nil))
	goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

	return flywayHistory, goflyHistory
}

// -----------------------------------------------------------------------------
// historySchemaFor
//
// Where gofly keeps its history on this engine.
// -----------------------------------------------------------------------------
func historySchemaFor(target Target) string {
	switch target.Dialect {
	case lib.DialectPostgres, lib.DialectMssql:
		return lib.DefaultGoflySchema
	default:
		return ""
	}
}

// -----------------------------------------------------------------------------
// TestCompatMigrateFromScratch
//
// The baseline: three migrations applied to an empty database must produce the
// same history rows and, crucially, the same checksums.
// -----------------------------------------------------------------------------
func TestCompatMigrateFromScratch(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		flyway, gofly := bothHistories(t, target, baseSchema(target.SQLDialect),
			[]string{"migrate"},
			func(g *lib.Gofly) {
				if _, err := g.Migrate(); err != nil {
					t.Fatalf("gofly migrate failed: %v", err)
				}
			})

		if len(gofly) != 3 {
			t.Fatalf("gofly recorded %d migrations, want 3", len(gofly))
		}
		AssertSameHistory(t, "migrate from scratch", flyway, gofly)
	})
}

// -----------------------------------------------------------------------------
// TestCompatIncrementalMigrate
//
// Migrating in two goes must land in the same place as migrating in one.
// -----------------------------------------------------------------------------
func TestCompatIncrementalMigrate(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		fixtures := baseSchema(target.SQLDialect)

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(fixtures[:2])
		flywayWorkspace.MustRunFlyway("migrate")
		flywayWorkspace.WriteAll(fixtures[2:])
		flywayWorkspace.MustRunFlyway("migrate")
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(fixtures[:2])
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyWorkspace.WriteAll(fixtures[2:])
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		AssertSameHistory(t, "incremental migrate", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatTarget
// -----------------------------------------------------------------------------
func TestCompatTarget(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		flyway, gofly := bothHistories(t, target, baseSchema(target.SQLDialect),
			[]string{"-target=2", "migrate"},
			func(g *lib.Gofly) {
				g.Config.Target = "2"
				if _, err := g.Migrate(); err != nil {
					t.Fatalf("gofly migrate failed: %v", err)
				}
			})

		if len(gofly) != 2 {
			t.Fatalf("gofly recorded %d migrations, want 2", len(gofly))
		}
		AssertSameHistory(t, "migrate up to a target", flyway, gofly)
	})
}

// -----------------------------------------------------------------------------
// TestCompatRepeatableMigrations
//
// A repeatable migration is applied once, skipped while unchanged, and applied
// again the moment its contents change.
// -----------------------------------------------------------------------------
func TestCompatRepeatableMigrations(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect
		fixtures := append(baseSchema(dialect)[:1],
			Fixture{"R__User_names.sql", repeatableView(dialect, "id")})

		runBoth := func(workspace *Workspace, migrate func()) {
			workspace.WriteAll(fixtures)
			migrate()
			migrate() // unchanged, must be a no-op
			workspace.Write("R__User_names.sql", repeatableView(dialect, "id, name"))
			migrate()
		}

		flywayWorkspace := NewWorkspace(t, target)
		runBoth(flywayWorkspace, func() { flywayWorkspace.MustRunFlyway("migrate") })
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		runBoth(goflyWorkspace, func() {
			if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
				t.Fatalf("gofly migrate failed: %v", err)
			}
		})
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		// V1, then R once, then R again after the edit
		if len(goflyHistory) != 3 {
			t.Errorf("gofly recorded %d rows, want 3", len(goflyHistory))
		}
		AssertSameHistory(t, "repeatable migrations", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatBaseline
// -----------------------------------------------------------------------------
func TestCompatBaseline(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		fixtures := baseSchema(target.SQLDialect)

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(fixtures)
		flywayWorkspace.MustRunFlyway("-baselineVersion=2", "baseline")
		flywayWorkspace.MustRunFlyway("migrate")
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(fixtures)
		config := goflyWorkspace.Config()
		config.BaselineVersion = "2"
		gofly := goflyWorkspace.Gofly(config)
		if err := gofly.Baseline(); err != nil {
			t.Fatalf("gofly baseline failed: %v", err)
		}
		if _, err := gofly.Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		// the baseline row, then only V3
		if len(goflyHistory) != 2 {
			t.Fatalf("gofly recorded %d rows, want the baseline plus V3", len(goflyHistory))
		}
		if goflyHistory[0].Type != string(lib.MigrationTypeBaseline) {
			t.Errorf("the first row is %q, want BASELINE", goflyHistory[0].Type)
		}
		AssertSameHistory(t, "baseline then migrate", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatChecksumMismatchIsRefused
//
// Both tools must refuse a migration that was edited after being applied, and
// must agree on what the new checksum is.
// -----------------------------------------------------------------------------
func TestCompatChecksumMismatchIsRefused(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect
		edited := createUsers(dialect) + "-- an innocent looking edit\n"

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(baseSchema(dialect)[:1])
		flywayWorkspace.MustRunFlyway("migrate")
		flywayWorkspace.Write("V1__Create_users.sql", edited)
		flywayOutput, flywayOK := flywayWorkspace.RunFlyway("validate")

		if flywayOK {
			t.Fatal("flyway should have refused the edited migration")
		}

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(baseSchema(dialect)[:1])
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyWorkspace.Write("V1__Create_users.sql", edited)

		result, err := goflyWorkspace.Gofly(nil).Validate()
		if err != nil {
			t.Fatalf("gofly validate failed: %v", err)
		}
		if result.Valid() {
			t.Fatal("gofly should have refused the edited migration")
		}
		if result.Errors[0].Code != lib.ErrorChecksumMismatch {
			t.Fatalf("gofly reported %s, want a checksum mismatch", result.Errors[0].Code)
		}

		// both tools must have arrived at the same pair of checksums
		for _, line := range strings.Split(result.Errors[0].Message, "\n") {
			if !strings.Contains(line, ":") {
				continue
			}
			_, value, _ := strings.Cut(line, ": ")
			value = strings.TrimSpace(value)
			if strings.HasPrefix(line, "->") && !strings.Contains(flywayOutput, value) {
				t.Errorf("flyway did not report the checksum %s that gofly did:\n%s", value, flywayOutput)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatMissingMigrationIsRefused
// -----------------------------------------------------------------------------
func TestCompatMissingMigrationIsRefused(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		fixtures := baseSchema(target.SQLDialect)

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(fixtures)
		flywayWorkspace.MustRunFlyway("migrate")
		flywayWorkspace.Remove("V2__Add_email.sql")
		if _, ok := flywayWorkspace.RunFlyway("validate"); ok {
			t.Error("flyway should have refused the missing migration")
		}

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(fixtures)
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyWorkspace.Remove("V2__Add_email.sql")

		result, err := goflyWorkspace.Gofly(nil).Validate()
		if err != nil {
			t.Fatalf("gofly validate failed: %v", err)
		}
		if result.Valid() {
			t.Fatal("gofly should have refused the missing migration")
		}
		if result.Errors[0].Code != lib.ErrorAppliedVersionedMigrationNotResolved {
			t.Errorf("gofly reported %s, want the applied-not-resolved code", result.Errors[0].Code)
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatOutOfOrderMigrations
// -----------------------------------------------------------------------------
func TestCompatOutOfOrderMigrations(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect
		first := Fixture{"V1__Create_users.sql", createUsers(dialect)}
		third := Fixture{"V3__Create_orders.sql", createOrders(dialect)}
		late := Fixture{"V2__Create_audit.sql", createAudit(dialect)}

		// without -outOfOrder both tools refuse to carry on
		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll([]Fixture{first, third})
		flywayWorkspace.MustRunFlyway("migrate")
		flywayWorkspace.WriteAll([]Fixture{late})
		if _, ok := flywayWorkspace.RunFlyway("validate"); ok {
			t.Error("flyway should have flagged the out of order migration")
		}
		flywayWorkspace.MustRunFlyway("-outOfOrder=true", "migrate")
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll([]Fixture{first, third})
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyWorkspace.WriteAll([]Fixture{late})

		result, err := goflyWorkspace.Gofly(nil).Validate()
		if err != nil {
			t.Fatalf("gofly validate failed: %v", err)
		}
		if result.Valid() {
			t.Error("gofly should have flagged the out of order migration")
		}

		outOfOrder := goflyWorkspace.Config()
		outOfOrder.OutOfOrder = true
		if _, err := goflyWorkspace.Gofly(outOfOrder).Migrate(); err != nil {
			t.Fatalf("gofly out of order migrate failed: %v", err)
		}
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		AssertSameHistory(t, "out of order migrations", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatPlaceholders
//
// The placeholder is replaced before the SQL runs, but the checksum is taken
// from the file as written, so both tools must record the same number whatever
// the placeholder expands to.
// -----------------------------------------------------------------------------
func TestCompatPlaceholders(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		fixtures := []Fixture{{"V1__Create_extra.sql", placeholderMigration(target.SQLDialect)}}

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(fixtures)
		flywayWorkspace.MustRunFlyway("-placeholders.table_name=e2e_extra", "migrate")
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(fixtures)
		config := goflyWorkspace.Config()
		config.Placeholders["table_name"] = "e2e_extra"
		if _, err := goflyWorkspace.Gofly(config).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		AssertSameHistory(t, "placeholders", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatFailedMigrationIsRecorded
//
// When a migration blows up, both tools must leave the database in the same
// state. Engines that roll DDL back record the failure; MySQL cannot, and
// Flyway does not record it there either.
// -----------------------------------------------------------------------------
func TestCompatFailedMigrationIsRecorded(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect
		fixtures := []Fixture{
			{"V1__Create_users.sql", createUsers(dialect)},
			{"V2__Broken.sql", brokenSQL()},
		}

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(fixtures)
		if _, ok := flywayWorkspace.RunFlyway("migrate"); ok {
			t.Fatal("flyway should have failed on the broken migration")
		}
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(fixtures)
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err == nil {
			t.Fatal("gofly should have failed on the broken migration")
		}
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		AssertSameHistory(t, "a failed migration", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatRepairAfterAFailure
// -----------------------------------------------------------------------------
func TestCompatRepairAfterAFailure(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect
		fixtures := []Fixture{
			{"V1__Create_users.sql", createUsers(dialect)},
			{"V2__Broken.sql", brokenSQL()},
		}
		fixed := Fixture{"V2__Broken.sql", createOrders(dialect)}

		flywayWorkspace := NewWorkspace(t, target)
		flywayWorkspace.WriteAll(fixtures)
		flywayWorkspace.RunFlyway("migrate")
		flywayWorkspace.MustRunFlyway("repair")
		flywayWorkspace.WriteAll([]Fixture{fixed})
		flywayWorkspace.MustRunFlyway("migrate")
		flywayHistory := flywayWorkspace.ReadHistory("", lib.FlywayTable)

		goflyWorkspace := NewWorkspace(t, target)
		goflyWorkspace.WriteAll(fixtures)
		goflyWorkspace.Gofly(nil).Migrate()
		if _, err := goflyWorkspace.Gofly(nil).Repair(); err != nil {
			t.Fatalf("gofly repair failed: %v", err)
		}
		goflyWorkspace.WriteAll([]Fixture{fixed})
		if _, err := goflyWorkspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate after repair failed: %v", err)
		}
		goflyHistory := goflyWorkspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		AssertSameHistory(t, "repair after a failure", flywayHistory, goflyHistory)
	})
}

// -----------------------------------------------------------------------------
// TestCompatHandoverFromFlyway
//
// The flagship: Flyway migrates part of the way, gofly takes over, and the
// imported history has to be indistinguishable from what Flyway would have
// produced on its own.
// -----------------------------------------------------------------------------
func TestCompatHandoverFromFlyway(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		fixtures := baseSchema(target.SQLDialect)

		// what Flyway alone would have ended up with
		reference := NewWorkspace(t, target)
		reference.WriteAll(fixtures)
		reference.MustRunFlyway("migrate")
		referenceHistory := reference.ReadHistory("", lib.FlywayTable)

		// Flyway up to V2, then gofly for the rest
		handover := NewWorkspace(t, target)
		handover.WriteAll(fixtures)
		handover.MustRunFlyway("-target=2", "migrate")

		gofly := handover.Gofly(nil)
		result, err := gofly.Migrate()
		if err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}
		if result.MigrationsExecuted != 1 {
			t.Fatalf("gofly applied %d migrations, want only V3: the import did not happen", result.MigrationsExecuted)
		}
		handoverHistory := handover.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		AssertSameHistory(t, "handover from flyway", referenceHistory, handoverHistory)

		// and the flyway table must have been left exactly as it was
		untouched := handover.ReadHistory("", lib.FlywayTable)
		if len(untouched) != 2 {
			t.Errorf("gofly wrote to the flyway history table, it now holds %d rows", len(untouched))
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatFlywayCanReadWhatGoflyWrote
//
// The handover has to work in both directions, so real Flyway is pointed at
// gofly's own table and must make sense of it.
// -----------------------------------------------------------------------------
func TestCompatFlywayCanReadWhatGoflyWrote(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		workspace := NewWorkspace(t, target)
		workspace.WriteAll(baseSchema(target.SQLDialect))

		if _, err := workspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}

		args := []string{"-table=" + lib.DefaultGoflyTable}
		if schema := historySchemaFor(target); schema != "" {
			args = append(args, "-defaultSchema="+schema)
		}
		args = append(args, "info")

		output, ok := workspace.RunFlyway(args...)
		if !ok {
			t.Fatalf("flyway could not read gofly's history table:\n%s", output)
		}

		for _, expected := range []string{"Create users", "Add email", "Create orders", "Success"} {
			if !strings.Contains(output, expected) {
				t.Errorf("flyway's info is missing %q:\n%s", expected, output)
			}
		}
		if strings.Contains(output, "Pending") {
			t.Errorf("flyway thinks migrations are still pending:\n%s", output)
		}
	})
}

// -----------------------------------------------------------------------------
// TestCompatValidateAgainstAnUntouchedFlywayDatabase
//
// Validating a database still managed by Flyway must read the Flyway history
// and must not create anything.
// -----------------------------------------------------------------------------
func TestCompatValidateAgainstAnUntouchedFlywayDatabase(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		workspace := NewWorkspace(t, target)
		workspace.WriteAll(baseSchema(target.SQLDialect))
		workspace.MustRunFlyway("migrate")

		gofly := workspace.Gofly(nil)

		info, source, err := gofly.InfoWithSource()
		if err != nil {
			t.Fatalf("gofly info failed: %v", err)
		}
		if source != lib.HistorySourceFlyway {
			t.Fatalf("gofly read source %v, want the flyway history", source)
		}
		if info.Current.String() != "3" {
			t.Errorf("gofly reports version %s, want 3", info.Current)
		}

		result, err := gofly.Validate()
		if err != nil {
			t.Fatalf("gofly validate failed: %v", err)
		}
		if !result.Valid() {
			t.Errorf("validation should pass against the flyway history: %v", result.Error())
		}

		exists, err := gofly.Connection.Dialect().TableExists(
			gofly.Connection.DB(), historySchemaFor(target), lib.DefaultGoflyTable)
		if err != nil {
			t.Fatalf("cannot look up the gofly table: %v", err)
		}
		if exists {
			t.Error("validate created the gofly history table, it must not")
		}
	})
}

// -----------------------------------------------------------------------------
// TestGoflyUndo
//
// Undo is a Flyway Teams feature, so the community edition cannot be compared
// against. This checks gofly on its own, on every engine.
// -----------------------------------------------------------------------------
func TestGoflyUndo(t *testing.T) {
	eachTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect

		workspace := NewWorkspace(t, target)
		workspace.WriteAll(baseSchema(dialect))
		workspace.Write("U3__Create_orders.sql", "DROP TABLE e2e_orders;\n")

		if _, err := workspace.Gofly(nil).Migrate(); err != nil {
			t.Fatalf("gofly migrate failed: %v", err)
		}

		undone, err := workspace.Gofly(nil).Undo()
		if err != nil {
			t.Fatalf("gofly undo failed: %v", err)
		}
		if undone.MigrationsUndone != 1 {
			t.Fatalf("undid %d migrations, want 1", undone.MigrationsUndone)
		}

		history := workspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)
		if len(history) != 4 {
			t.Fatalf("the history holds %d rows, want the three migrations plus the undo", len(history))
		}
		if history[3].Type != string(lib.MigrationTypeUndoSQL) || history[3].Version != "3" {
			t.Errorf("the undo row reads %s", history[3])
		}

		// and V3 can be applied again afterwards
		again, err := workspace.Gofly(nil).Migrate()
		if err != nil {
			t.Fatalf("gofly migrate after undo failed: %v", err)
		}
		if again.MigrationsExecuted != 1 {
			t.Errorf("re-applied %d migrations, want 1", again.MigrationsExecuted)
		}
	})
}

// -----------------------------------------------------------------------------
// TestGoflyGroupIsAllOrNothing
//
// Flyway's community edition has no -group, so this is gofly on its own. The
// assertion follows what each engine can actually deliver: MySQL commits
// implicitly on DDL and cannot take the whole batch back.
// -----------------------------------------------------------------------------
func TestGoflyGroupIsAllOrNothing(t *testing.T) {
	eachTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect

		workspace := NewWorkspace(t, target)
		workspace.WriteAll([]Fixture{
			{"V1__Create_users.sql", createUsers(dialect)},
			{"V2__Create_orders.sql", createOrders(dialect)},
			{"V3__Broken.sql", brokenSQL()},
		})

		config := workspace.Config()
		config.Group = true

		gofly := workspace.Gofly(config)
		if _, err := gofly.Migrate(); err == nil {
			t.Fatal("gofly should have failed on the broken migration")
		}

		history := workspace.ReadHistory(historySchemaFor(target), lib.DefaultGoflyTable)

		if !gofly.Connection.Dialect().SupportsDDLTransactions() {
			if len(history) != 2 {
				t.Errorf("on %s the two migrations before the failure are committed, got %d rows",
					target.Name, len(history))
			}
			return
		}

		if len(history) != 0 {
			t.Errorf("the whole batch should have been rolled back, got %d rows:\n%s",
				len(history), renderHistory(history))
		}

		exists, err := gofly.Connection.Dialect().TableExists(gofly.Connection.DB(), "", "e2e_users")
		if err != nil {
			t.Fatalf("cannot look up e2e_users: %v", err)
		}
		if exists {
			t.Error("e2e_users survived a rolled back batch")
		}
	})
}

// -----------------------------------------------------------------------------
// TestGoflyVersionSchemes
//
// Every version scheme Flyway documents has to resolve and apply in the right
// order. Flyway is asked the same question so that the ordering is compared,
// not just asserted.
// -----------------------------------------------------------------------------
func TestGoflyVersionSchemes(t *testing.T) {
	eachComparableTarget(t, func(t *testing.T, target Target) {
		dialect := target.SQLDialect

		// deliberately mixed: plain, padded, dotted, underscored and a stamp
		fixtures := []Fixture{
			{"V1__Create_users.sql", createUsers(dialect)},
			{"V1.1__Add_email.sql", addEmail(dialect)},
			{"V1_2__Create_orders.sql", createOrders(dialect)},
			{"V002__Create_audit.sql", createAudit(dialect)},
			{"V20260101120000__Create_extra.sql", createExtra(dialect)},
		}

		flyway, gofly := bothHistories(t, target, fixtures,
			[]string{"migrate"},
			func(g *lib.Gofly) {
				if _, err := g.Migrate(); err != nil {
					t.Fatalf("gofly migrate failed: %v", err)
				}
			})

		if len(gofly) != len(fixtures) {
			t.Fatalf("gofly applied %d migrations, want %d", len(gofly), len(fixtures))
		}

		want := []string{"1", "1.1", "1.2", "002", "20260101120000"}
		for index, version := range want {
			if gofly[index].Version != version {
				t.Errorf("row %d is version %q, want %q", index, gofly[index].Version, version)
			}
		}

		AssertSameHistory(t, "version schemes", flyway, gofly)
	})
}
