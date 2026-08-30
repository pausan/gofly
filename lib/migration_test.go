// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// resolveIn
// -----------------------------------------------------------------------------
func resolveIn(t *testing.T, files map[string]string) (*ResolvedMigrations, error) {
	t.Helper()

	config := NewConfig()
	config.Locations = []string{"filesystem:" + writeFilesInDir(t, files)}

	return ResolveMigrations(config)
}

// -----------------------------------------------------------------------------
// TestResolveFindsEveryKindOfMigration
// -----------------------------------------------------------------------------
func TestResolveFindsEveryKindOfMigration(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V1__a.sql":     "SELECT 1;\n",
		"V2__b.sql":     "SELECT 2;\n",
		"U2__b.sql":     "SELECT -2;\n",
		"R__report.sql": "SELECT 3;\n",
		"README.md":     "not a migration",
		"notes.txt":     "not a migration either",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resolved.Versioned) != 2 {
		t.Errorf("found %d versioned migrations, want 2", len(resolved.Versioned))
	}
	if len(resolved.Undo) != 1 {
		t.Errorf("found %d undo migrations, want 1", len(resolved.Undo))
	}
	if len(resolved.Repeatable) != 1 {
		t.Errorf("found %d repeatable migrations, want 1", len(resolved.Repeatable))
	}
}

// -----------------------------------------------------------------------------
// TestResolveSortsByVersion
// -----------------------------------------------------------------------------
func TestResolveSortsByVersion(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V10__c.sql":  "SELECT 1;\n",
		"V2__b.sql":   "SELECT 2;\n",
		"V1.1__a.sql": "SELECT 3;\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := []string{}
	for _, migration := range resolved.Versioned {
		got = append(got, migration.Version.String())
	}

	if strings.Join(got, ",") != "1.1,2,10" {
		t.Errorf("sorted as %v, want [1.1 2 10]", got)
	}
}

// -----------------------------------------------------------------------------
// TestResolveScansSubdirectories
// -----------------------------------------------------------------------------
func TestResolveScansSubdirectories(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V1__a.sql":            "SELECT 1;\n",
		"tables/V2__b.sql":     "SELECT 2;\n",
		"tables/sub/V3__c.sql": "SELECT 3;\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resolved.Versioned) != 3 {
		t.Fatalf("found %d migrations, want 3", len(resolved.Versioned))
	}

	// the script column keeps the path relative to the location
	if resolved.Versioned[1].Script != "tables/V2__b.sql" {
		t.Errorf("script recorded as %q", resolved.Versioned[1].Script)
	}
	if resolved.Versioned[2].Script != "tables/sub/V3__c.sql" {
		t.Errorf("script recorded as %q", resolved.Versioned[2].Script)
	}
}

// -----------------------------------------------------------------------------
// TestResolveMergesSeveralLocations
//
// This is how artypist layers its test data on top of the schema migrations.
// -----------------------------------------------------------------------------
func TestResolveMergesSeveralLocations(t *testing.T) {
	schema := writeFilesInDir(t, map[string]string{
		"V10__structure.sql": "SELECT 1;\n",
		"V20__more.sql":      "SELECT 2;\n",
	})
	testData := writeFilesInDir(t, map[string]string{
		"V11__data.sql": "SELECT 3;\n",
	})

	config := NewConfig()
	config.Locations = []string{"filesystem:" + schema, "filesystem:" + testData}

	resolved, err := ResolveMigrations(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := []string{}
	for _, migration := range resolved.Versioned {
		got = append(got, migration.Version.String())
	}

	if strings.Join(got, ",") != "10,11,20" {
		t.Errorf("resolved as %v, want the two locations interleaved by version", got)
	}
}

// -----------------------------------------------------------------------------
// TestResolveRejectsDuplicateVersions
// -----------------------------------------------------------------------------
func TestResolveRejectsDuplicateVersions(t *testing.T) {
	_, err := resolveIn(t, map[string]string{
		"V1__a.sql":     "SELECT 1;\n",
		"sub/V1__b.sql": "SELECT 2;\n",
	})

	if err == nil || !strings.Contains(err.Error(), "more than one migration with version 1") {
		t.Errorf("expected a duplicate version error, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestResolveRejectsDuplicateRepeatableDescriptions
// -----------------------------------------------------------------------------
func TestResolveRejectsDuplicateRepeatableDescriptions(t *testing.T) {
	_, err := resolveIn(t, map[string]string{
		"R__report.sql":     "SELECT 1;\n",
		"sub/R__report.sql": "SELECT 2;\n",
	})

	if err == nil || !strings.Contains(err.Error(), "more than one repeatable migration") {
		t.Errorf("expected a duplicate description error, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestResolveTreatsOneAsTwoWhenTrailingZeroesDiffer
//
// V1 and V1.0 are the same version to Flyway, so having both is a duplicate.
// -----------------------------------------------------------------------------
func TestResolveTreatsOneAsTwoWhenTrailingZeroesDiffer(t *testing.T) {
	_, err := resolveIn(t, map[string]string{
		"V1__a.sql":   "SELECT 1;\n",
		"V1.0__b.sql": "SELECT 2;\n",
	})

	if err == nil || !strings.Contains(err.Error(), "more than one migration") {
		t.Errorf("V1 and V1.0 are the same version, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestResolveRejectsMalformedMigrationNames
// -----------------------------------------------------------------------------
func TestResolveRejectsMalformedMigrationNames(t *testing.T) {
	_, err := resolveIn(t, map[string]string{
		"V__no_version.sql": "SELECT 1;\n",
	})

	if err == nil || !strings.Contains(err.Error(), "Invalid versioned migration name format") {
		t.Errorf("expected a name format error, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestResolveSkipsCallbacks
// -----------------------------------------------------------------------------
func TestResolveSkipsCallbacks(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V1__a.sql":        "SELECT 1;\n",
		"afterMigrate.sql": "SELECT 2;\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resolved.Versioned) != 1 {
		t.Errorf("found %d migrations, want only V1", len(resolved.Versioned))
	}
}

// -----------------------------------------------------------------------------
// TestResolveIgnoresMissingLocations
// -----------------------------------------------------------------------------
func TestResolveIgnoresMissingLocations(t *testing.T) {
	config := NewConfig()
	config.Locations = []string{"filesystem:/definitely/not/there"}

	resolved, err := ResolveMigrations(config)
	if err != nil {
		t.Fatalf("a missing location should just be empty: %v", err)
	}
	if len(resolved.Versioned) != 0 {
		t.Error("nothing should have been resolved")
	}
}

// -----------------------------------------------------------------------------
// TestResolveRejectsClasspathLocations
// -----------------------------------------------------------------------------
func TestResolveRejectsClasspathLocations(t *testing.T) {
	config := NewConfig()
	config.Locations = []string{"classpath:db/migration"}

	if _, err := ResolveMigrations(config); err == nil {
		t.Error("classpath locations only exist for the Java edition and should be rejected")
	}
}

// -----------------------------------------------------------------------------
// TestResolveComputesChecksums
// -----------------------------------------------------------------------------
func TestResolveComputesChecksums(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V1__a.sql": "CREATE TABLE a (id INT);\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolved.Versioned[0].Checksum != -2090711421 {
		t.Errorf("checksum computed as %d", resolved.Versioned[0].Checksum)
	}
}

// -----------------------------------------------------------------------------
// TestUndoForFindsTheMatchingScript
// -----------------------------------------------------------------------------
func TestUndoForFindsTheMatchingScript(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V1__a.sql": "SELECT 1;\n",
		"V2__b.sql": "SELECT 2;\n",
		"U2__b.sql": "SELECT -2;\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	two := mustVersion(t, "2")
	if undo := resolved.UndoFor(two); undo == nil || undo.Script != "U2__b.sql" {
		t.Errorf("the undo script for version 2 was not found: %v", undo)
	}

	one := mustVersion(t, "1")
	if undo := resolved.UndoFor(one); undo != nil {
		t.Errorf("version 1 has no undo script, got %v", undo.Script)
	}
}

// -----------------------------------------------------------------------------
// TestLoadSQLStripsTheBom
// -----------------------------------------------------------------------------
func TestLoadSQLStripsTheBom(t *testing.T) {
	resolved, err := resolveIn(t, map[string]string{
		"V1__a.sql": "\ufeffSELECT 1;\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sql, err := resolved.Versioned[0].LoadSQL(NewPlaceholderReplacer(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.HasPrefix(sql, "\ufeff") {
		t.Error("the BOM should have been stripped before executing")
	}
}

// -----------------------------------------------------------------------------
// TestLocationPathPrefixIsOptional
//
// The filesystem: prefix is what Flyway writes, but a bare directory is far
// more natural on a command line and has to mean the same thing.
// -----------------------------------------------------------------------------
func TestLocationPathPrefixIsOptional(t *testing.T) {
	cases := []struct {
		location string
		expected string
	}{
		{"filesystem:./sql", "./sql"},
		{"./sql", "./sql"},
		{"sql", "sql"},
		{"setup/sql/schema", "setup/sql/schema"},
		{"/srv/app/sql", "/srv/app/sql"},
		{"  sql  ", "sql"},
		{"", ""},
	}

	for _, testCase := range cases {
		path, err := locationPath(testCase.location)
		if err != nil {
			t.Fatalf("locationPath(%q) returned %v", testCase.location, err)
		}
		if path != testCase.expected {
			t.Errorf("locationPath(%q) = %q, want %q", testCase.location, path, testCase.expected)
		}
	}
}

// -----------------------------------------------------------------------------
// TestLocationPathRejectsUnsupportedPrefixes
// -----------------------------------------------------------------------------
func TestLocationPathRejectsUnsupportedPrefixes(t *testing.T) {
	for _, location := range []string{"classpath:db/migration", "s3:bucket/sql"} {
		if _, err := locationPath(location); err == nil {
			t.Errorf("locationPath(%q) should have failed", location)
		}
	}
}

// -----------------------------------------------------------------------------
// TestResolveSuggestsTheSeparatorInUse
//
// The bare "could not recognise version number" error points at the version,
// which is the one part of the name that is not actually wrong.
// -----------------------------------------------------------------------------
func TestResolveSuggestsTheSeparatorInUse(t *testing.T) {
	_, err := resolveIn(t, map[string]string{
		"V10_initial.sql":        "select 1;",
		"V100_user_activity.sql": "select 2;",
		"V320_public_name.sql":   "select 3;",
	})
	if err == nil {
		t.Fatal("expected the resolution to fail")
	}

	if !strings.Contains(err.Error(), `sqlMigrationSeparator="_"`) {
		t.Errorf("error does not name the separator in use: %v", err)
	}
	if !strings.Contains(err.Error(), "3 of the 3 migration file(s)") {
		t.Errorf("error does not count the matching files: %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestResolveWithDetectedSeparatorSucceeds
//
// The suggestion has to be the setting that actually resolves the migrations.
// -----------------------------------------------------------------------------
func TestResolveWithDetectedSeparatorSucceeds(t *testing.T) {
	config := NewConfig()
	config.Locations = []string{writeFilesInDir(t, map[string]string{
		"V10_initial.sql":        "select 1;",
		"V100_user_activity.sql": "select 2;",
	})}
	config.SQLMigrationSeparator = "_"

	resolved, err := ResolveMigrations(config)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}
	if len(resolved.Versioned) != 2 {
		t.Fatalf("resolved %d migrations, want 2", len(resolved.Versioned))
	}
	if got := resolved.Versioned[1].Description; got != "user activity" {
		t.Errorf("description %q, want %q", got, "user activity")
	}
}
