// Copyright (C) 2026 Pau Sanchez
//
// PostgreSQL dialect.
package lib

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

type postgresDialect struct{}

// -----------------------------------------------------------------------------
// Name
// -----------------------------------------------------------------------------
func (d *postgresDialect) Name() string {
	return DialectPostgres
}

// -----------------------------------------------------------------------------
// QuoteIdentifier
// -----------------------------------------------------------------------------
func (d *postgresDialect) QuoteIdentifier(parts ...string) string {
	return quoteWith(`"`, `"`, parts)
}

// -----------------------------------------------------------------------------
// Placeholder
// -----------------------------------------------------------------------------
func (d *postgresDialect) Placeholder(index int) string {
	return fmt.Sprintf("$%d", index)
}

// -----------------------------------------------------------------------------
// DefaultSchema
// -----------------------------------------------------------------------------
func (d *postgresDialect) DefaultSchema(db *sql.DB, excluding string) (string, error) {
	// current_schema() alone is a trap here: the stock search_path is
	// "$user", public, so the moment a schema named after the connecting user
	// exists, current_schema() switches to it. gofly creates exactly such a
	// schema when the login happens to be called gofly, which would silently
	// redirect every migration into it. Walking the search path and skipping
	// our own schema avoids that.
	var schema sql.NullString
	err := db.QueryRow(
		`SELECT n FROM unnest(current_schemas(false)) AS n WHERE n <> $1 LIMIT 1`, excluding,
	).Scan(&schema)

	if err == sql.ErrNoRows || (err == nil && (!schema.Valid || schema.String == "")) {
		return "public", nil
	}
	if err != nil {
		return "", err
	}

	return schema.String, nil
}

// -----------------------------------------------------------------------------
// DefaultHistorySchema
// -----------------------------------------------------------------------------
func (d *postgresDialect) DefaultHistorySchema() string {
	return DefaultGoflySchema
}

// -----------------------------------------------------------------------------
// SupportsSchemas
// -----------------------------------------------------------------------------
func (d *postgresDialect) SupportsSchemas() bool {
	return true
}

// -----------------------------------------------------------------------------
// SchemaExists
// -----------------------------------------------------------------------------
func (d *postgresDialect) SchemaExists(db *sql.DB, schema string) (bool, error) {
	var found int
	err := db.QueryRow(
		`SELECT 1 FROM information_schema.schemata WHERE schema_name = $1`, schema,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateSchemaSQL
// -----------------------------------------------------------------------------
func (d *postgresDialect) CreateSchemaSQL(schema string) []string {
	return []string{"CREATE SCHEMA IF NOT EXISTS " + d.QuoteIdentifier(schema)}
}

// -----------------------------------------------------------------------------
// TableExists
// -----------------------------------------------------------------------------
func (d *postgresDialect) TableExists(db *sql.DB, schema string, table string) (bool, error) {
	if schema == "" {
		var found int
		err := db.QueryRow(
			`SELECT 1 FROM information_schema.tables
			  WHERE table_schema = current_schema() AND table_name = $1`, table,
		).Scan(&found)

		return existsFromScan(err)
	}

	var found int
	err := db.QueryRow(
		`SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2`,
		schema, table,
	).Scan(&found)

	return existsFromScan(err)
}

// -----------------------------------------------------------------------------
// CreateHistoryTableSQL
//
// Same layout as PostgreSQLDatabase.getRawCreateScript so that the table stays
// readable and writable by Flyway itself.
// -----------------------------------------------------------------------------
func (d *postgresDialect) CreateHistoryTableSQL(schema string, table string) []string {
	qualified := d.QuoteIdentifier(schema, table)

	return []string{
		"CREATE TABLE " + qualified + " (\n" +
			`    "installed_rank" INT NOT NULL,` + "\n" +
			`    "version" VARCHAR(50),` + "\n" +
			`    "description" VARCHAR(200) NOT NULL,` + "\n" +
			`    "type" VARCHAR(20) NOT NULL,` + "\n" +
			`    "script" VARCHAR(1000) NOT NULL,` + "\n" +
			`    "checksum" INTEGER,` + "\n" +
			`    "installed_by" VARCHAR(100) NOT NULL,` + "\n" +
			`    "installed_on" TIMESTAMP NOT NULL DEFAULT now(),` + "\n" +
			`    "execution_time" INTEGER NOT NULL,` + "\n" +
			`    "success" BOOLEAN NOT NULL` + "\n" +
			")",
		"ALTER TABLE " + qualified + " ADD CONSTRAINT " + d.QuoteIdentifier(table+"_pk") +
			` PRIMARY KEY ("installed_rank")`,
		"CREATE INDEX " + d.QuoteIdentifier(table+"_s_idx") + " ON " + qualified + ` ("success")`,
	}
}

// -----------------------------------------------------------------------------
// SupportsDDLTransactions
// -----------------------------------------------------------------------------
func (d *postgresDialect) SupportsDDLTransactions() bool {
	return true
}

// -----------------------------------------------------------------------------
// BooleanLiteral
// -----------------------------------------------------------------------------
func (d *postgresDialect) BooleanLiteral(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

// -----------------------------------------------------------------------------
// SetSessionSchema
// -----------------------------------------------------------------------------
func (d *postgresDialect) SetSessionSchema(db *sql.DB, schema string) error {
	if schema == "" {
		return nil
	}

	_, err := db.Exec("SET search_path TO " + d.QuoteIdentifier(schema) + ", public")

	return err
}

// -----------------------------------------------------------------------------
// postgresDSN
//
// Converts jdbc:postgresql://host:port/db?params into the pgx DSN, injecting
// the credentials passed on the command line when the url carries none.
// -----------------------------------------------------------------------------
func postgresDSN(rawURL string, user string, password string) (string, error) {
	trimmed := rawURL
	if index := strings.Index(strings.ToLower(trimmed), "jdbc:"); index == 0 {
		trimmed = trimmed[len("jdbc:"):]
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("cannot parse postgresql url %s: %w", rawURL, err)
	}

	parsed.Scheme = "postgres"
	if parsed.User == nil && user != "" {
		parsed.User = url.UserPassword(user, password)
	}
	if parsed.Port() == "" && parsed.Hostname() != "" {
		parsed.Host = parsed.Hostname() + ":5432"
	}

	return parsed.String(), nil
}
