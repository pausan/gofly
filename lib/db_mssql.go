// Copyright (C) 2026 Pau Sanchez
//
// SQL Server dialect.
package lib

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

type mssqlDialect struct{}

// -----------------------------------------------------------------------------
// Name
// -----------------------------------------------------------------------------
func (d *mssqlDialect) Name() string {
	return DialectMssql
}

// -----------------------------------------------------------------------------
// QuoteIdentifier
// -----------------------------------------------------------------------------
func (d *mssqlDialect) QuoteIdentifier(parts ...string) string {
	return quoteWith("[", "]", parts)
}

// -----------------------------------------------------------------------------
// Placeholder
// -----------------------------------------------------------------------------
func (d *mssqlDialect) Placeholder(index int) string {
	return fmt.Sprintf("@p%d", index)
}

// -----------------------------------------------------------------------------
// DefaultSchema
// -----------------------------------------------------------------------------
func (d *mssqlDialect) DefaultSchema(db *sql.DB, excluding string) (string, error) {
	var schema sql.NullString
	if err := db.QueryRow(`SELECT SCHEMA_NAME()`).Scan(&schema); err != nil {
		return "", err
	}

	if !schema.Valid || schema.String == "" {
		return "dbo", nil
	}

	return schema.String, nil
}

// -----------------------------------------------------------------------------
// DefaultHistorySchema
// -----------------------------------------------------------------------------
func (d *mssqlDialect) DefaultHistorySchema() string {
	return DefaultGoflySchema
}

// -----------------------------------------------------------------------------
// SupportsSchemas
// -----------------------------------------------------------------------------
func (d *mssqlDialect) SupportsSchemas() bool {
	return true
}

// -----------------------------------------------------------------------------
// SchemaExists
// -----------------------------------------------------------------------------
func (d *mssqlDialect) SchemaExists(db *sql.DB, schema string) (bool, error) {
	var found int
	err := db.QueryRow(
		`SELECT 1 FROM sys.schemas WHERE name = @p1`, schema,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateSchemaSQL
//
// SQL Server refuses CREATE SCHEMA unless it is the first statement of a batch,
// hence the EXEC wrapper.
// -----------------------------------------------------------------------------
func (d *mssqlDialect) CreateSchemaSQL(schema string) []string {
	escaped := strings.ReplaceAll(schema, "'", "''")

	return []string{
		fmt.Sprintf(
			"IF NOT EXISTS (SELECT 1 FROM sys.schemas WHERE name = '%s') EXEC('CREATE SCHEMA %s')",
			escaped, strings.ReplaceAll(d.QuoteIdentifier(schema), "'", "''"),
		),
	}
}

// -----------------------------------------------------------------------------
// TableExists
// -----------------------------------------------------------------------------
func (d *mssqlDialect) TableExists(db *sql.DB, schema string, table string) (bool, error) {
	if schema == "" {
		var found int
		err := db.QueryRow(
			`SELECT 1 FROM information_schema.tables
			  WHERE table_schema = SCHEMA_NAME() AND table_name = @p1`, table,
		).Scan(&found)

		return existsFromScan(err)
	}

	var found int
	err := db.QueryRow(
		`SELECT 1 FROM information_schema.tables WHERE table_schema = @p1 AND table_name = @p2`,
		schema, table,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateHistoryTableSQL
//
// Mirrors SQLServerDatabase.getRawCreateScript.
// -----------------------------------------------------------------------------
func (d *mssqlDialect) CreateHistoryTableSQL(schema string, table string) []string {
	qualified := d.QuoteIdentifier(schema, table)

	return []string{
		"CREATE TABLE " + qualified + " (\n" +
			"    [installed_rank] INT NOT NULL,\n" +
			"    [version] NVARCHAR(50),\n" +
			"    [description] NVARCHAR(200),\n" +
			"    [type] NVARCHAR(20) NOT NULL,\n" +
			"    [script] NVARCHAR(1000) NOT NULL,\n" +
			"    [checksum] INT,\n" +
			"    [installed_by] NVARCHAR(100) NOT NULL,\n" +
			"    [installed_on] DATETIME NOT NULL DEFAULT GETDATE(),\n" +
			"    [execution_time] INT NOT NULL,\n" +
			"    [success] BIT NOT NULL\n" +
			")",
		"ALTER TABLE " + qualified + " ADD CONSTRAINT " + d.QuoteIdentifier(table+"_pk") +
			" PRIMARY KEY ([installed_rank])",
		"CREATE INDEX " + d.QuoteIdentifier(table+"_s_idx") + " ON " + qualified + " ([success])",
	}
}

// -----------------------------------------------------------------------------
// SupportsDDLTransactions
// -----------------------------------------------------------------------------
func (d *mssqlDialect) SupportsDDLTransactions() bool {
	return true
}

// -----------------------------------------------------------------------------
// BooleanLiteral
// -----------------------------------------------------------------------------
func (d *mssqlDialect) BooleanLiteral(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// -----------------------------------------------------------------------------
// SetSessionSchema
//
// SQL Server binds the default schema to the user, not to the session, so there
// is nothing to switch here: gofly always qualifies its own table instead.
// -----------------------------------------------------------------------------
func (d *mssqlDialect) SetSessionSchema(db *sql.DB, schema string) error {
	return nil
}

// -----------------------------------------------------------------------------
// mssqlDSN
//
// Converts jdbc:sqlserver://host:port;databaseName=db;key=value into the
// sqlserver:// DSN the Go driver expects.
// -----------------------------------------------------------------------------
func mssqlDSN(rawURL string, user string, password string) (string, error) {
	trimmed := rawURL
	for _, prefix := range []string{"jdbc:sqlserver:", "sqlserver:"} {
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			trimmed = trimmed[len(prefix):]
			break
		}
	}
	trimmed = strings.TrimPrefix(trimmed, "//")
	trimmed = strings.TrimPrefix(trimmed, "/")

	// the JDBC syntax separates properties with semicolons
	segments := strings.Split(trimmed, ";")
	address := segments[0]

	query := url.Values{}
	if index := strings.Index(address, "?"); index >= 0 {
		parsed, err := url.ParseQuery(address[index+1:])
		if err != nil {
			return "", fmt.Errorf("cannot parse sqlserver url parameters: %w", err)
		}
		query = parsed
		address = address[:index]
	}

	for _, segment := range segments[1:] {
		if segment == "" {
			continue
		}
		key, value, found := strings.Cut(segment, "=")
		if !found {
			continue
		}
		if strings.EqualFold(key, "databaseName") {
			key = "database"
		}
		query.Set(key, value)
	}

	if address == "" {
		address = "127.0.0.1:1433"
	}
	if !strings.Contains(address, ":") {
		address += ":1433"
	}

	dsn := &url.URL{Scheme: "sqlserver", Host: address, RawQuery: query.Encode()}
	if user != "" {
		dsn.User = url.UserPassword(user, password)
	}

	return dsn.String(), nil
}
