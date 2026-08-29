// Copyright (C) 2026 Pau Sanchez
package lib

import "testing"

// -----------------------------------------------------------------------------
// TestChecksumIsLineEndingIndependent
//
// The reference values were produced with the very same CRC-32 that
// java.util.zip.CRC32 implements, feeding it one line at a time.
// -----------------------------------------------------------------------------
func TestChecksumIsLineEndingIndependent(t *testing.T) {
	const expected int32 = -2090711421

	variants := map[string]string{
		"unix":            "CREATE TABLE a (id INT);\n",
		"dos":             "CREATE TABLE a (id INT);\r\n",
		"classic mac":     "CREATE TABLE a (id INT);\r",
		"no line ending":  "CREATE TABLE a (id INT);",
		"bom prefixed":    "\ufeffCREATE TABLE a (id INT);\n",
		"bom and dos eol": "\ufeffCREATE TABLE a (id INT);\r\n",
	}

	for name, content := range variants {
		if got := ChecksumString(content); got != expected {
			t.Errorf("%s: got %d, want %d", name, got, expected)
		}
	}
}

// -----------------------------------------------------------------------------
// TestChecksumKnownValues
// -----------------------------------------------------------------------------
func TestChecksumKnownValues(t *testing.T) {
	cases := []struct {
		content  string
		expected int32
	}{
		{"", 0},
		{"line1\nline2\nline3\n", -836085548},
		{"line1\r\nline2\r\nline3\r\n", -836085548},
		{"line1\nline2\nline3", -836085548},
		{"a\n\nb\n", -1635563411},
		{"SELECT 'ñá€';\n", -1069709678},
	}

	for _, c := range cases {
		if got := ChecksumString(c.content); got != c.expected {
			t.Errorf("checksum(%q) = %d, want %d", c.content, got, c.expected)
		}
	}
}

// -----------------------------------------------------------------------------
// TestChecksumIgnoresTrailingNewline
// -----------------------------------------------------------------------------
func TestChecksumIgnoresTrailingNewline(t *testing.T) {
	if ChecksumString("SELECT 1;") != ChecksumString("SELECT 1;\n") {
		t.Error("a trailing newline must not change the checksum")
	}

	// blank lines contribute no bytes at all to the CRC, so Flyway considers
	// these two migrations identical. Reproducing this quirk is what makes the
	// checksums interchangeable with Flyway's.
	if ChecksumString("SELECT 1;\n\nSELECT 2;") != ChecksumString("SELECT 1;\nSELECT 2;") {
		t.Error("blank lines must not change the checksum")
	}

	// leading whitespace does count, though
	if ChecksumString("  SELECT 1;") == ChecksumString("SELECT 1;") {
		t.Error("indentation must change the checksum")
	}
}

// -----------------------------------------------------------------------------
// TestChecksumFile
// -----------------------------------------------------------------------------
func TestChecksumFile(t *testing.T) {
	path := writeTempFile(t, "V1__x.sql", "CREATE TABLE a (id INT);\n")

	checksum, err := ChecksumFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checksum != -2090711421 {
		t.Errorf("got %d, want -2090711421", checksum)
	}
}

// -----------------------------------------------------------------------------
// TestChecksumCombine
// -----------------------------------------------------------------------------
func TestChecksumCombine(t *testing.T) {
	if got := ChecksumCombine([]int32{42}); got != 42 {
		t.Errorf("a single resource keeps its checksum, got %d", got)
	}

	combined := ChecksumCombine([]int32{1, 2})
	if combined == 1 || combined == 2 || combined == 0 {
		t.Errorf("unexpected combined checksum %d", combined)
	}
	if ChecksumCombine([]int32{1, 2}) != combined {
		t.Error("combining must be deterministic")
	}
	if ChecksumCombine([]int32{2, 1}) == combined {
		t.Error("combining must depend on the order")
	}
}
