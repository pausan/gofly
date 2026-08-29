// Copyright (C) 2026 Pau Sanchez
//
// SQLite dialect, backed by a pure Go driver so that gofly stays a single
// static binary with no cgo and no system libraries to install.
package lib

import (
	"database/sql"
	"strings"
)

type sqliteDialect struct{}

// -----------------------------------------------------------------------------
// Name
// -----------------------------------------------------------------------------
func (d *sqliteDialect) Name() string {
	return DialectSqlite
}

// -----------------------------------------------------------------------------
// QuoteIdentifier
// -----------------------------------------------------------------------------
func (d *sqliteDialect) QuoteIdentifier(parts ...string) string {
	return quoteWith(`"`, `"`, parts)
}

// -----------------------------------------------------------------------------
// Placeholder
// -----------------------------------------------------------------------------
func (d *sqliteDialect) Placeholder(index int) string {
	return "?"
}

// -----------------------------------------------------------------------------
// DefaultSchema
// -----------------------------------------------------------------------------
func (d *sqliteDialect) DefaultSchema(db *sql.DB, excluding string) (string, error) {
	return "main", nil
}

// -----------------------------------------------------------------------------
// DefaultHistorySchema
//
// SQLite has no schemas, only attached databases, so the history table lives in
// the migrated file under its own name.
// -----------------------------------------------------------------------------
func (d *sqliteDialect) DefaultHistorySchema() string {
	return ""
}

// -----------------------------------------------------------------------------
// SupportsSchemas
// -----------------------------------------------------------------------------
func (d *sqliteDialect) SupportsSchemas() bool {
	return false
}

// -----------------------------------------------------------------------------
// SchemaExists
// -----------------------------------------------------------------------------
func (d *sqliteDialect) SchemaExists(db *sql.DB, schema string) (bool, error) {
	return schema == "" || schema == "main", nil
}

// -----------------------------------------------------------------------------
// CreateSchemaSQL
// -----------------------------------------------------------------------------
func (d *sqliteDialect) CreateSchemaSQL(schema string) []string {
	return nil
}

// -----------------------------------------------------------------------------
// TableExists
// -----------------------------------------------------------------------------
func (d *sqliteDialect) TableExists(db *sql.DB, schema string, table string) (bool, error) {
	var found int
	err := db.QueryRow(
		`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateHistoryTableSQL
//
// Mirrors SQLiteDatabase.getRawCreateScript.
// -----------------------------------------------------------------------------
func (d *sqliteDialect) CreateHistoryTableSQL(schema string, table string) []string {
	qualified := d.QuoteIdentifier(table)

	return []string{
		"CREATE TABLE " + qualified + " (\n" +
			`    "installed_rank" INT NOT NULL PRIMARY KEY,` + "\n" +
			`    "version" VARCHAR(50),` + "\n" +
			`    "description" VARCHAR(200) NOT NULL,` + "\n" +
			`    "type" VARCHAR(20) NOT NULL,` + "\n" +
			`    "script" VARCHAR(1000) NOT NULL,` + "\n" +
			`    "checksum" INT,` + "\n" +
			`    "installed_by" VARCHAR(100) NOT NULL,` + "\n" +
			`    "installed_on" TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),` + "\n" +
			`    "execution_time" INT NOT NULL,` + "\n" +
			`    "success" BOOLEAN NOT NULL` + "\n" +
			")",
		"CREATE INDEX " + d.QuoteIdentifier(table+"_s_idx") + " ON " + qualified + ` ("success")`,
	}
}

// -----------------------------------------------------------------------------
// SupportsDDLTransactions
// -----------------------------------------------------------------------------
func (d *sqliteDialect) SupportsDDLTransactions() bool {
	return true
}

// -----------------------------------------------------------------------------
// BooleanLiteral
// -----------------------------------------------------------------------------
func (d *sqliteDialect) BooleanLiteral(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// -----------------------------------------------------------------------------
// SetSessionSchema
// -----------------------------------------------------------------------------
func (d *sqliteDialect) SetSessionSchema(db *sql.DB, schema string) error {
	return nil
}

// -----------------------------------------------------------------------------
// sqliteDSN
//
// Accepts jdbc:sqlite:/path/to.db, sqlite:///path/to.db and plain file: urls.
// -----------------------------------------------------------------------------
func sqliteDSN(rawURL string) string {
	trimmed := rawURL

	for _, prefix := range []string{"jdbc:sqlite:", "sqlite://", "sqlite:"} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			return trimmed[len(prefix):]
		}
	}

	return trimmed
}

// -----------------------------------------------------------------------------
// existsFromScan
//
// Turns the result of a `SELECT 1 ...` existence probe into a boolean.
// -----------------------------------------------------------------------------
func existsFromScan(err error) (bool, error) {
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return true, nil
}
