//go:build e2e

// Copyright (C) 2026 Pau Sanchez
//
// The migration fixtures the scenarios run. They are written per dialect
// because the point of the harness is to exercise real SQL against real
// engines, and CREATE TABLE is not quite the same everywhere.
package e2e

// Fixture is one migration file
type Fixture struct {
	Name string
	SQL  string
}

// -----------------------------------------------------------------------------
// createUsers
// -----------------------------------------------------------------------------
func createUsers(dialect string) string {
	switch dialect {
	case "postgres":
		return "CREATE TABLE e2e_users (\n  id   SERIAL PRIMARY KEY,\n  name VARCHAR(100) NOT NULL\n);\n"
	case "mysql":
		return "CREATE TABLE e2e_users (\n  id   INT AUTO_INCREMENT PRIMARY KEY,\n  name VARCHAR(100) NOT NULL\n);\n"
	case "mssql":
		return "CREATE TABLE e2e_users (\n  id   INT IDENTITY(1,1) PRIMARY KEY,\n  name NVARCHAR(100) NOT NULL\n);\n"
	default:
		return "CREATE TABLE e2e_users (\n  id   INTEGER PRIMARY KEY,\n  name TEXT NOT NULL\n);\n"
	}
}

// -----------------------------------------------------------------------------
// addEmail
// -----------------------------------------------------------------------------
func addEmail(dialect string) string {
	switch dialect {
	case "mssql":
		return "ALTER TABLE e2e_users ADD email NVARCHAR(200);\n"
	default:
		return "ALTER TABLE e2e_users ADD COLUMN email VARCHAR(200);\n"
	}
}

// -----------------------------------------------------------------------------
// createOrders
// -----------------------------------------------------------------------------
func createOrders(dialect string) string {
	switch dialect {
	case "postgres":
		return "CREATE TABLE e2e_orders (\n  id      SERIAL PRIMARY KEY,\n  user_id INT NOT NULL\n);\n"
	case "mysql":
		return "CREATE TABLE e2e_orders (\n  id      INT AUTO_INCREMENT PRIMARY KEY,\n  user_id INT NOT NULL\n);\n"
	case "mssql":
		return "CREATE TABLE e2e_orders (\n  id      INT IDENTITY(1,1) PRIMARY KEY,\n  user_id INT NOT NULL\n);\n"
	default:
		return "CREATE TABLE e2e_orders (\n  id      INTEGER PRIMARY KEY,\n  user_id INTEGER NOT NULL\n);\n"
	}
}

// -----------------------------------------------------------------------------
// createAudit
// -----------------------------------------------------------------------------
func createAudit(dialect string) string {
	switch dialect {
	case "mssql":
		return "CREATE TABLE e2e_audit (\n  note NVARCHAR(200)\n);\n"
	default:
		return "CREATE TABLE e2e_audit (\n  note VARCHAR(200)\n);\n"
	}
}

// -----------------------------------------------------------------------------
// createExtra
// -----------------------------------------------------------------------------
func createExtra(dialect string) string {
	switch dialect {
	case "mssql":
		return "CREATE TABLE e2e_extra (\n  note NVARCHAR(200)\n);\n"
	default:
		return "CREATE TABLE e2e_extra (\n  note VARCHAR(200)\n);\n"
	}
}

// -----------------------------------------------------------------------------
// repeatableView
//
// A repeatable migration has to be safe to run over and over, so it drops the
// view first. SQL Server will not take DROP VIEW IF EXISTS before 2016, hence
// the OBJECT_ID dance.
// -----------------------------------------------------------------------------
func repeatableView(dialect string, columns string) string {
	switch dialect {
	case "mssql":
		return "IF OBJECT_ID('[e2e_user_names]', 'V') IS NOT NULL DROP VIEW [e2e_user_names];\n" +
			"GO\n" +
			"CREATE VIEW e2e_user_names AS SELECT " + columns + " FROM e2e_users;\n"
	default:
		return "DROP VIEW IF EXISTS e2e_user_names;\n" +
			"CREATE VIEW e2e_user_names AS SELECT " + columns + " FROM e2e_users;\n"
	}
}

// -----------------------------------------------------------------------------
// brokenSQL
// -----------------------------------------------------------------------------
func brokenSQL() string {
	return "THIS IS NOT VALID SQL AT ALL;\n"
}

// -----------------------------------------------------------------------------
// placeholderMigration
// -----------------------------------------------------------------------------
func placeholderMigration(dialect string) string {
	switch dialect {
	case "mssql":
		return "CREATE TABLE ${table_name} (\n  note NVARCHAR(200)\n);\n"
	default:
		return "CREATE TABLE ${table_name} (\n  note VARCHAR(200)\n);\n"
	}
}

// -----------------------------------------------------------------------------
// baseSchema
//
// The three migrations most scenarios build on.
// -----------------------------------------------------------------------------
func baseSchema(dialect string) []Fixture {
	return []Fixture{
		{"V1__Create_users.sql", createUsers(dialect)},
		{"V2__Add_email.sql", addEmail(dialect)},
		{"V3__Create_orders.sql", createOrders(dialect)},
	}
}

// -----------------------------------------------------------------------------
// WriteAll
// -----------------------------------------------------------------------------
func (w *Workspace) WriteAll(fixtures []Fixture) {
	for _, fixture := range fixtures {
		w.Write(fixture.Name, fixture.SQL)
	}
}
