// Copyright (C) 2026 Pau Sanchez
package lib

import "testing"

// -----------------------------------------------------------------------------
// mustVersion
// -----------------------------------------------------------------------------
func mustVersion(t *testing.T, text string) *Version {
	t.Helper()

	version, err := NewVersion(text)
	if err != nil {
		t.Fatalf("cannot parse version %q: %v", text, err)
	}

	return version
}

// -----------------------------------------------------------------------------
// TestVersionParsing
// -----------------------------------------------------------------------------
func TestVersionParsing(t *testing.T) {
	cases := []struct {
		input   string
		display string
	}{
		{"1", "1"},
		{"6.0", "6.0"},
		{"005", "005"},
		{"1.2.3.4", "1.2.3.4"},
		{"201004200021", "201004200021"},
		{"1_2", "1.2"},
		{"1_2_3", "1.2.3"},
		{"20260101120000", "20260101120000"},
	}

	for _, c := range cases {
		version := mustVersion(t, c.input)
		if version.String() != c.display {
			t.Errorf("%q displayed as %q, want %q", c.input, version.String(), c.display)
		}
		if version.RawVersion() != c.input {
			t.Errorf("%q raw version is %q", c.input, version.RawVersion())
		}
	}
}

// -----------------------------------------------------------------------------
// TestVersionRejectsInvalidInput
// -----------------------------------------------------------------------------
func TestVersionRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "abc", "1.a", "1..2", "-1", "1.2.x", "+3", "1 2"} {
		if _, err := NewVersion(input); err == nil {
			t.Errorf("version %q should have been rejected", input)
		}
	}
}

// -----------------------------------------------------------------------------
// TestVersionTrailingZeroesAreIgnored
//
// Flyway trims trailing zero parts, so 1, 1.0 and 1.0.0 are the same version.
// -----------------------------------------------------------------------------
func TestVersionTrailingZeroesAreIgnored(t *testing.T) {
	one := mustVersion(t, "1")

	for _, equivalent := range []string{"1.0", "1.0.0", "1_0", "1.0.0.0"} {
		if !one.Equals(mustVersion(t, equivalent)) {
			t.Errorf("1 and %s should compare as equal", equivalent)
		}
	}

	if one.Equals(mustVersion(t, "1.0.1")) {
		t.Error("1 and 1.0.1 must not be equal")
	}
}

// -----------------------------------------------------------------------------
// TestVersionComparison
// -----------------------------------------------------------------------------
func TestVersionComparison(t *testing.T) {
	ordered := []string{"1", "1.1", "1.2", "1.2.1", "2", "10", "10.1", "2026.1.1"}

	for i := 0; i < len(ordered)-1; i++ {
		lower := mustVersion(t, ordered[i])
		higher := mustVersion(t, ordered[i+1])

		if lower.Compare(higher) >= 0 {
			t.Errorf("%s should be lower than %s", ordered[i], ordered[i+1])
		}
		if !higher.IsNewerThan(lower) {
			t.Errorf("%s should be newer than %s", ordered[i+1], ordered[i])
		}
		if !higher.IsAtLeast(higher) {
			t.Errorf("%s should be at least itself", ordered[i+1])
		}
	}
}

// -----------------------------------------------------------------------------
// TestVersionComparesNumericallyNotLexicographically
// -----------------------------------------------------------------------------
func TestVersionComparesNumericallyNotLexicographically(t *testing.T) {
	if !mustVersion(t, "10").IsNewerThan(mustVersion(t, "9")) {
		t.Error("10 must be newer than 9")
	}
	if !mustVersion(t, "005").Equals(mustVersion(t, "5")) {
		t.Error("005 must equal 5")
	}
}

// -----------------------------------------------------------------------------
// TestVersionHandlesHugeNumbers
//
// Flyway stores the parts as BigInteger, so values beyond int64 must work.
// -----------------------------------------------------------------------------
func TestVersionHandlesHugeNumbers(t *testing.T) {
	huge := mustVersion(t, "99999999999999999999999999.1")
	small := mustVersion(t, "99999999999999999999999998.9")

	if !huge.IsNewerThan(small) {
		t.Error("big integer version parts are not compared correctly")
	}
}

// -----------------------------------------------------------------------------
// TestVersionPredefinedMarkers
// -----------------------------------------------------------------------------
func TestVersionPredefinedMarkers(t *testing.T) {
	cases := map[string]*Version{
		"current": VersionCurrent,
		"CURRENT": VersionCurrent,
		"next":    VersionNext,
		"latest":  VersionLatest,
		"LATEST":  VersionLatest,
		"":        VersionEmpty,
	}

	for input, expected := range cases {
		version, err := VersionFromString(input)
		if err != nil {
			t.Fatalf("cannot parse %q: %v", input, err)
		}
		if version != expected {
			t.Errorf("%q resolved to %v, want %v", input, version, expected)
		}
		if !version.IsPredefined() {
			t.Errorf("%q should be a predefined marker", input)
		}
	}

	one := mustVersion(t, "1")
	if one.IsPredefined() {
		t.Error("a real version must not be predefined")
	}

	// latest is above everything, empty below everything
	if !VersionLatest.IsNewerThan(one) {
		t.Error("latest must be newer than any real version")
	}
	if !one.IsNewerThan(VersionEmpty) {
		t.Error("any real version must be newer than the empty schema")
	}
}

// -----------------------------------------------------------------------------
// TestVersionNilHandling
// -----------------------------------------------------------------------------
func TestVersionNilHandling(t *testing.T) {
	var missing *Version

	if missing.String() != "" {
		t.Error("a nil version must render as the empty string")
	}
	if mustVersion(t, "1").Compare(missing) <= 0 {
		t.Error("a real version must sort above a nil one")
	}
}
