// Copyright (C) 2026 Pau Sanchez
package lib

import "testing"

// -----------------------------------------------------------------------------
// TestPostgresHistoryLockKeyMatchesFlyway
//
// Flyway locks LOCK_MAGIC_NUM + String.hashCode() of the quoted table name.
// The emoji checks the hash runs over UTF-16 code units, surrogates included.
// -----------------------------------------------------------------------------
func TestPostgresHistoryLockKeyMatchesFlyway(t *testing.T) {
	cases := map[string]int64{
		`"public"."flyway_schema_history"`: 77433833903597,
		`"gofly"."gofly_schema_history"`:   77433485886404,
		`"app"."histoire_😀"`:               77430081018815,
	}
	for name, want := range cases {
		if got := postgresHistoryLockKey(name); got != want {
			t.Errorf("%s: got %d, want %d", name, got, want)
		}
	}
}
