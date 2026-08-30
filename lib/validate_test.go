// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// TestVerboseValidateShowsTheLocationsAndTheFileOfEveryMigration
//
// -verbose is what somebody staring at a checksum mismatch reaches for, so the
// report has to name the directories that were scanned and, for every row of
// the history, the file on disk it was compared against.
// -----------------------------------------------------------------------------
func TestVerboseValidateShowsTheLocationsAndTheFileOfEveryMigration(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	setup.write("V2__b.sql", "CREATE TABLE b (id INT);\n")

	setup.config.Quiet = false
	setup.config.Verbose = true

	gofly := setup.open()
	defer gofly.Close()

	output := &bytes.Buffer{}
	gofly.Output = output

	if _, err := gofly.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	report := output.String()

	if !strings.Contains(report, setup.locations) {
		t.Errorf("the scanned location %s is not mentioned:\n%s", setup.locations, report)
	}
	for _, name := range []string{"V1__a.sql", "V2__b.sql"} {
		if !strings.Contains(report, filepath.Join(setup.locations, name)) {
			t.Errorf("the file of %s is not mentioned:\n%s", name, report)
		}
	}
	if !strings.Contains(report, "Checksum (local/db)") {
		t.Errorf("the checksums being compared are not shown:\n%s", report)
	}
	if !strings.Contains(report, "<not in history>") {
		t.Errorf("V2 has no history row, that should be visible:\n%s", report)
	}
}

// -----------------------------------------------------------------------------
// TestValidateStaysSilentWithoutVerbose
// -----------------------------------------------------------------------------
func TestValidateStaysSilentWithoutVerbose(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	setup.config.Quiet = false

	gofly := setup.open()
	defer gofly.Close()

	output := &bytes.Buffer{}
	gofly.Output = output

	if _, err := gofly.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if output.Len() != 0 {
		t.Errorf("validate should print nothing of its own without -verbose, got:\n%s", output.String())
	}
}

// -----------------------------------------------------------------------------
// TestVerboseValidateReportsAMissingLocation
// -----------------------------------------------------------------------------
func TestVerboseValidateReportsAMissingLocation(t *testing.T) {
	setup := newTestSetup(t)
	setup.write("V1__a.sql", "CREATE TABLE a (id INT);\n")
	setup.mustMigrate()

	gone := filepath.Join(t.TempDir(), "not-there")
	setup.config.Locations = append(setup.config.Locations, "filesystem:"+gone)
	setup.config.Quiet = false
	setup.config.Verbose = true

	gofly := setup.open()
	defer gofly.Close()

	output := &bytes.Buffer{}
	gofly.Output = output

	if _, err := gofly.Validate(); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	report := output.String()
	if !strings.Contains(report, gone) || !strings.Contains(report, "(not found)") {
		t.Errorf("a location that is not there should be called out:\n%s", report)
	}
}
