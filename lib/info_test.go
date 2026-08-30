// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"testing"
)

// -----------------------------------------------------------------------------
// TestPendingStateSeparatesBaselineFromBelowBaseline
//
// Flyway reports the migration sitting exactly at the baseline as
// "Ignored (Baseline)" and only the older ones as "Below Baseline", see
// org.flywaydb.core.api.resolver.ResolvedMigration.getState.
// -----------------------------------------------------------------------------
func TestPendingStateSeparatesBaselineFromBelowBaseline(t *testing.T) {
	config := NewConfig()
	baseline := mustVersion(t, "10")

	cases := []struct {
		version  string
		expected MigrationState
	}{
		{"5", StateBelowBaseline},
		{"9.9", StateBelowBaseline},
		{"10", StateBaselineIgnored},
		{"10.0", StateBaselineIgnored},
		{"20", StatePending},
	}

	for _, testCase := range cases {
		version := mustVersion(t, testCase.version)
		state := pendingState(config, version, baseline, baseline, VersionEmpty)
		if state != testCase.expected {
			t.Errorf("version %s is %q, want %q", testCase.version, state, testCase.expected)
		}
	}
}

// -----------------------------------------------------------------------------
// TestSortInfosLeadsWithTheMigrationsTheBaselineSkipped
//
// Flyway puts the migrations the baseline skipped ahead of the applied ones, so
// that the file sitting at the baseline version reads next to the baseline row
// instead of trailing the whole history. See MigrationInfoImpl.compareTo.
// -----------------------------------------------------------------------------
func TestSortInfosLeadsWithTheMigrationsTheBaselineSkipped(t *testing.T) {
	applied := func(rank int, version string, state MigrationState) *MigrationInfo {
		return &MigrationInfo{
			Applied: &AppliedMigration{InstalledRank: rank, Version: mustVersion(t, version)},
			State:   state,
		}
	}
	resolved := func(version string, state MigrationState) *MigrationInfo {
		return &MigrationInfo{
			Resolved: &ResolvedMigration{Version: mustVersion(t, version)},
			State:    state,
		}
	}

	infos := []*MigrationInfo{
		applied(1, "10", StateBaseline),
		applied(2, "20", StateSuccess),
		resolved("320", StatePending),
		resolved("10", StateBaselineIgnored),
		resolved("5", StateBelowBaseline),
	}

	sortInfos(infos)

	expected := []MigrationState{
		StateBelowBaseline,
		StateBaselineIgnored,
		StateBaseline,
		StateSuccess,
		StatePending,
	}

	for index, want := range expected {
		if infos[index].State != want {
			t.Errorf("row %d is %q, want %q", index, infos[index].State, want)
		}
	}
}
