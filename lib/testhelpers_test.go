// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"os"
	"path/filepath"
	"testing"
)

// -----------------------------------------------------------------------------
// writeTempFile
//
// Writes a file inside the test's temporary directory and returns its path.
// -----------------------------------------------------------------------------
func writeTempFile(t *testing.T, name string, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("cannot write %s: %v", path, err)
	}

	return path
}

// -----------------------------------------------------------------------------
// writeFilesInDir
//
// Creates a temporary directory populated with the given files and returns it.
// -----------------------------------------------------------------------------
func writeFilesInDir(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", path, err)
		}
	}

	return dir
}
