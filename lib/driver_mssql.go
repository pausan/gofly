// Copyright (C) 2026 Pau Sanchez
//
// Registers the SQL Server driver. See driver_pg.go for how the build tags
// work.

//go:build !goflymin || db_mssql

package lib

import (
	_ "github.com/microsoft/go-mssqldb"
)
