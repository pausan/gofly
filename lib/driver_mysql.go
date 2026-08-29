// Copyright (C) 2026 Pau Sanchez
//
// Registers the MySQL and MariaDB driver. See driver_pg.go for how the build
// tags work.

//go:build !goflymin || db_mysql

package lib

import (
	_ "github.com/go-sql-driver/mysql"
)
