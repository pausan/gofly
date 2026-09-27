// Copyright (C) 2026 Pau Sanchez
package lib

import "testing"

// -----------------------------------------------------------------------------
// TestPostgresTransactionDetectionIgnoresQuotedKeywords
// -----------------------------------------------------------------------------
func TestPostgresTransactionDetectionIgnoresQuotedKeywords(t *testing.T) {
	for _, sql := range []string{"VACUUM events", "/* header */ CREATE UNIQUE INDEX CONCURRENTLY ix ON events(v)", "DROP INDEX CONCURRENTLY ix", "REINDEX INDEX CONCURRENTLY ix", "CREATE DATABASE other"} {
		if !postgresNontransactional(sql) {
			t.Errorf("not detected: %s", sql)
		}
	}
	for _, sql := range []string{`SELECT 'VACUUM'`, `CREATE INDEX "CONCURRENTLY" ON events(v)`, `DO $$ BEGIN RAISE NOTICE 'VACUUM'; END $$`, `CREATE TABLE events(v text DEFAULT 'CONCURRENTLY')`} {
		if postgresNontransactional(sql) {
			t.Errorf("false positive: %s", sql)
		}
	}
}
