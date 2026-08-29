// Copyright (C) 2026 Pau Sanchez
//
// MySQL and MariaDB dialect.
//
// MySQL calls databases schemas, so the gofly history lives in a database of
// its own only when one is explicitly configured. By default it sits next to
// the migrated tables, under its own table name.
package lib

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

type mysqlDialect struct{}

// -----------------------------------------------------------------------------
// Name
// -----------------------------------------------------------------------------
func (d *mysqlDialect) Name() string {
	return DialectMysql
}

// -----------------------------------------------------------------------------
// QuoteIdentifier
// -----------------------------------------------------------------------------
func (d *mysqlDialect) QuoteIdentifier(parts ...string) string {
	return quoteWith("`", "`", parts)
}

// -----------------------------------------------------------------------------
// Placeholder
// -----------------------------------------------------------------------------
func (d *mysqlDialect) Placeholder(index int) string {
	return "?"
}

// -----------------------------------------------------------------------------
// DefaultSchema
// -----------------------------------------------------------------------------
func (d *mysqlDialect) DefaultSchema(db *sql.DB, excluding string) (string, error) {
	var schema sql.NullString
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&schema); err != nil {
		return "", err
	}

	return schema.String, nil
}

// -----------------------------------------------------------------------------
// DefaultHistorySchema
//
// Creating a whole database just to hold ten rows is rarely what people want on
// MySQL, and it needs privileges the migration user often lacks, so gofly keeps
// its history table in the migrated database unless told otherwise.
// -----------------------------------------------------------------------------
func (d *mysqlDialect) DefaultHistorySchema() string {
	return ""
}

// -----------------------------------------------------------------------------
// SupportsSchemas
// -----------------------------------------------------------------------------
func (d *mysqlDialect) SupportsSchemas() bool {
	return true
}

// -----------------------------------------------------------------------------
// SchemaExists
// -----------------------------------------------------------------------------
func (d *mysqlDialect) SchemaExists(db *sql.DB, schema string) (bool, error) {
	var found int
	err := db.QueryRow(
		`SELECT 1 FROM information_schema.schemata WHERE schema_name = ?`, schema,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateSchemaSQL
// -----------------------------------------------------------------------------
func (d *mysqlDialect) CreateSchemaSQL(schema string) []string {
	return []string{"CREATE DATABASE IF NOT EXISTS " + d.QuoteIdentifier(schema)}
}

// -----------------------------------------------------------------------------
// TableExists
// -----------------------------------------------------------------------------
func (d *mysqlDialect) TableExists(db *sql.DB, schema string, table string) (bool, error) {
	if schema == "" {
		var found int
		err := db.QueryRow(
			`SELECT 1 FROM information_schema.tables
			  WHERE table_schema = DATABASE() AND table_name = ?`, table,
		).Scan(&found)

		return existsFromScan(err)
	}

	var found int
	err := db.QueryRow(
		`SELECT 1 FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`,
		schema, table,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateHistoryTableSQL
//
// Mirrors MySQLDatabase.getRawCreateScript.
// -----------------------------------------------------------------------------
func (d *mysqlDialect) CreateHistoryTableSQL(schema string, table string) []string {
	qualified := d.QuoteIdentifier(schema, table)

	return []string{
		"CREATE TABLE " + qualified + " (\n" +
			"    `installed_rank` INT NOT NULL,\n" +
			"    `version` VARCHAR(50),\n" +
			"    `description` VARCHAR(200) NOT NULL,\n" +
			"    `type` VARCHAR(20) NOT NULL,\n" +
			"    `script` VARCHAR(1000) NOT NULL,\n" +
			"    `checksum` INT,\n" +
			"    `installed_by` VARCHAR(100) NOT NULL,\n" +
			"    `installed_on` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,\n" +
			"    `execution_time` INT NOT NULL,\n" +
			"    `success` BOOL NOT NULL,\n" +
			"    CONSTRAINT " + d.QuoteIdentifier(table+"_pk") + " PRIMARY KEY (`installed_rank`)\n" +
			")",
		"CREATE INDEX " + d.QuoteIdentifier(table+"_s_idx") + " ON " + qualified + " (`success`)",
	}
}

// -----------------------------------------------------------------------------
// SupportsDDLTransactions
//
// MySQL commits implicitly on every DDL statement, so a failed migration cannot
// be rolled back. gofly warns about it rather than pretending otherwise.
// -----------------------------------------------------------------------------
func (d *mysqlDialect) SupportsDDLTransactions() bool {
	return false
}

// -----------------------------------------------------------------------------
// BooleanLiteral
// -----------------------------------------------------------------------------
func (d *mysqlDialect) BooleanLiteral(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

// -----------------------------------------------------------------------------
// SetSessionSchema
// -----------------------------------------------------------------------------
func (d *mysqlDialect) SetSessionSchema(db *sql.DB, schema string) error {
	if schema == "" {
		return nil
	}

	_, err := db.Exec("USE " + d.QuoteIdentifier(schema))

	return err
}

// -----------------------------------------------------------------------------
// mysqlDSN
//
// Converts jdbc:mysql://host:port/db?params into the go-sql-driver DSN
// user:pass@tcp(host:port)/db?params.
// -----------------------------------------------------------------------------
func mysqlDSN(rawURL string, user string, password string) (string, error) {
	trimmed := rawURL
	for _, prefix := range []string{"jdbc:mysql:", "jdbc:mariadb:", "mysql:"} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			trimmed = trimmed[len(prefix):]
			break
		}
	}
	trimmed = strings.TrimPrefix(trimmed, "//")

	address := trimmed
	database := ""
	params := ""

	if index := strings.Index(address, "?"); index >= 0 {
		params = address[index+1:]
		address = address[:index]
	}
	if index := strings.Index(address, "/"); index >= 0 {
		database = address[index+1:]
		address = address[:index]
	}

	if address == "" {
		address = "127.0.0.1"
	}
	if !strings.Contains(address, ":") {
		address += ":3306"
	}

	credentials := ""
	if user != "" {
		credentials = user
		if password != "" {
			credentials += ":" + password
		}
		credentials += "@"
	}

	dsn := fmt.Sprintf("%stcp(%s)/%s", credentials, address, database)

	// multiStatements stays off on purpose: gofly splits the script itself so
	// that a failing statement can be reported with its line number
	extra := url.Values{}
	if params != "" {
		parsed, err := url.ParseQuery(params)
		if err != nil {
			return "", fmt.Errorf("cannot parse mysql url parameters %q: %w", params, err)
		}
		extra = parsed
	}
	if extra.Get("parseTime") == "" {
		extra.Set("parseTime", "true")
	}

	return dsn + "?" + extra.Encode(), nil
}
