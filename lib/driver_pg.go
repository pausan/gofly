// Copyright (C) 2026 Pau Sanchez
//
// Registers the PostgreSQL driver.
//
// Each driver lives behind its own build tag so that single database builds
// can leave the others out. A plain build has no tags and therefore includes
// everything, which is what the tests, the compatibility harness and the
// default release binary use. Passing goflymin flips the default to "nothing",
// and each db_* tag adds one driver back:
//
//	go build -tags goflymin,db_pg .            # postgres only
//	go build -tags goflymin,db_pg,db_sqlite .  # postgres and sqlite
//
// Leaving a driver out only removes it from the binary. The dialect itself
// still compiles in, and Connect reports the missing driver by name rather
// than letting database/sql complain about a forgotten import.

//go:build !goflymin || db_pg

package lib

import (
	_ "github.com/jackc/pgx/v5/stdlib"
)
