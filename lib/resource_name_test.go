// Copyright (C) 2026 Pau Sanchez
package lib

import "testing"

// -----------------------------------------------------------------------------
// TestParseVersionedNames
// -----------------------------------------------------------------------------
func TestParseVersionedNames(t *testing.T) {
	cases := []struct {
		fileName    string
		version     string
		description string
	}{
		{"V1__Initial.sql", "1", "Initial"},
		{"V1.1__Add_users.sql", "1.1", "Add users"},
		{"V1_1__Add_users.sql", "1.1", "Add users"},
		{"V2.0.1__Fix_index.sql", "2.0.1", "Fix index"},
		{"V10__initial_version.sql", "10", "initial version"},
		{"V20260101120000__Snapshot.sql", "20260101120000", "Snapshot"},
		{"V1__Multi__Separator.sql", "1", "Multi  Separator"},
		{"V1__.sql", "1", ""},
	}

	naming := DefaultNaming()
	for _, c := range cases {
		name := ParseResourceName(c.fileName, naming)

		if !name.Valid {
			t.Errorf("%s should be valid: %s", c.fileName, name.InvalidCause)
			continue
		}
		if name.Type != ResourceTypeVersioned {
			t.Errorf("%s should be a versioned migration", c.fileName)
		}
		if name.Version.String() != c.version {
			t.Errorf("%s: version %q, want %q", c.fileName, name.Version, c.version)
		}
		if name.Description != c.description {
			t.Errorf("%s: description %q, want %q", c.fileName, name.Description, c.description)
		}
	}
}

// -----------------------------------------------------------------------------
// TestParseDashesAreNotTurnedIntoSpaces
//
// Flyway only replaces underscores in the description, dashes survive.
// -----------------------------------------------------------------------------
func TestParseDashesAreNotTurnedIntoSpaces(t *testing.T) {
	name := ParseResourceName("V1__add-users_table.sql", DefaultNaming())

	if name.Description != "add-users table" {
		t.Errorf("description %q, want %q", name.Description, "add-users table")
	}
}

// -----------------------------------------------------------------------------
// TestParseUndoNames
// -----------------------------------------------------------------------------
func TestParseUndoNames(t *testing.T) {
	name := ParseResourceName("U1.1__Add_users.sql", DefaultNaming())

	if !name.Valid || name.Type != ResourceTypeUndo {
		t.Fatalf("U1.1__Add_users.sql should be an undo migration (%s)", name.InvalidCause)
	}
	if name.Version.String() != "1.1" {
		t.Errorf("version %q, want 1.1", name.Version)
	}
	if !name.Type.IsVersioned() {
		t.Error("undo migrations are versioned")
	}
}

// -----------------------------------------------------------------------------
// TestParseRepeatableNames
// -----------------------------------------------------------------------------
func TestParseRepeatableNames(t *testing.T) {
	name := ParseResourceName("R__Refresh_views.sql", DefaultNaming())

	if !name.Valid || name.Type != ResourceTypeRepeatable {
		t.Fatalf("R__Refresh_views.sql should be repeatable (%s)", name.InvalidCause)
	}
	if name.Version != nil {
		t.Error("a repeatable migration must not carry a version")
	}
	if name.Description != "Refresh views" {
		t.Errorf("description %q", name.Description)
	}
}

// -----------------------------------------------------------------------------
// TestParseCallbackNames
// -----------------------------------------------------------------------------
func TestParseCallbackNames(t *testing.T) {
	for _, fileName := range []string{"afterMigrate.sql", "beforeEachMigrate__log.sql"} {
		name := ParseResourceName(fileName, DefaultNaming())
		if name.Type != ResourceTypeCallback {
			t.Errorf("%s should be recognised as a callback, got %v", fileName, name.Type)
		}
	}
}

// -----------------------------------------------------------------------------
// TestParseInvalidNames
// -----------------------------------------------------------------------------
func TestParseInvalidNames(t *testing.T) {
	cases := []string{
		"V__NoVersion.sql",      // versioned without a version
		"VABC__Bad_version.sql", // version is not numeric
		"R1__Versioned.sql",     // repeatable carrying a version
		"random_file.sql",       // no known prefix
		"U__NoVersion.sql",      // undo without a version
	}

	for _, fileName := range cases {
		if name := ParseResourceName(fileName, DefaultNaming()); name.Valid {
			t.Errorf("%s should have been rejected", fileName)
		}
	}
}

// -----------------------------------------------------------------------------
// TestParseHonoursCustomNaming
// -----------------------------------------------------------------------------
func TestParseHonoursCustomNaming(t *testing.T) {
	naming := Naming{
		SQLMigrationPrefix:           "V",
		UndoSQLMigrationPrefix:       "U",
		RepeatableSQLMigrationPrefix: "R",
		SQLMigrationSeparator:        "_",
		SQLMigrationSuffixes:         []string{".sql", ".pkg"},
	}

	// this is how artypist names its migrations: a single underscore separator
	name := ParseResourceName("V10_initial_version.sql", naming)
	if !name.Valid {
		t.Fatalf("V10_initial_version.sql should be valid: %s", name.InvalidCause)
	}
	if name.Version.String() != "10" {
		t.Errorf("version %q, want 10", name.Version)
	}
	if name.Description != "initial version" {
		t.Errorf("description %q, want %q", name.Description, "initial version")
	}

	if name := ParseResourceName("V1_Package.PKG", naming); !name.Valid {
		t.Errorf("a .PKG suffix should match case insensitively: %s", name.InvalidCause)
	}
}

// -----------------------------------------------------------------------------
// TestParseLongestPrefixWins
// -----------------------------------------------------------------------------
func TestParseLongestPrefixWins(t *testing.T) {
	naming := DefaultNaming()
	naming.SQLMigrationPrefix = "VV"

	// "VV" must be preferred over the repeatable "R" and the undo "U", and a
	// plain "V1__x.sql" is no longer a migration at all
	if name := ParseResourceName("VV1__x.sql", naming); !name.Valid || name.Type != ResourceTypeVersioned {
		t.Errorf("VV1__x.sql should be versioned: %s", name.InvalidCause)
	}
	if name := ParseResourceName("V1__x.sql", naming); name.Valid {
		t.Error("V1__x.sql should not match once the prefix is VV")
	}
}

// -----------------------------------------------------------------------------
// TestParseKeepsSuffix
// -----------------------------------------------------------------------------
func TestParseKeepsSuffix(t *testing.T) {
	name := ParseResourceName("V1__x.sql", DefaultNaming())

	if name.Suffix != ".sql" || name.Prefix != "V" {
		t.Errorf("prefix %q suffix %q", name.Prefix, name.Suffix)
	}
}
