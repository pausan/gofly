// Copyright (C) 2026 Pau Sanchez
//
// Registers the SQLite driver. See driver_pg.go for how the build tags work.

//go:build !goflymin || db_sqlite

package lib

import (
	_ "modernc.org/sqlite"
)
