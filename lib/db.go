// Copyright (C) 2026 Pau Sanchez
//
// Database connectivity and the small dialect abstraction the rest of gofly
// builds upon. Only the differences that actually matter for migrating are
// abstracted: quoting, schema handling, the history table DDL and how each
// driver spells its bind parameters.
package lib

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	// SQL drivers
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"
)

// Supported dialects
const (
	DialectPostgres = "postgresql"
	DialectMysql    = "mysql"
	DialectMssql    = "sqlserver"
	DialectSqlite   = "sqlite"
)

// Dialect isolates everything that differs between the databases we support
type Dialect interface {
	// Name returns the dialect identifier, one of the Dialect* constants
	Name() string

	// QuoteIdentifier quotes a possibly qualified identifier
	QuoteIdentifier(parts ...string) string

	// Placeholder renders the bind parameter for the given 1-based position
	Placeholder(index int) string

	// DefaultSchema returns the schema the connection writes to by default,
	// never returning excluding, which is the schema gofly keeps its own
	// history in
	DefaultSchema(db *sql.DB, excluding string) (string, error)

	// DefaultHistorySchema is where gofly keeps its own history table. An empty
	// string means "the default schema of the connection".
	DefaultHistorySchema() string

	// SupportsSchemas reports whether the dialect has schemas at all
	SupportsSchemas() bool

	// SchemaExists reports whether a schema is already there
	SchemaExists(db *sql.DB, schema string) (bool, error)

	// CreateSchemaSQL returns the statements creating a schema
	CreateSchemaSQL(schema string) []string

	// TableExists reports whether a table is already there
	TableExists(db *sql.DB, schema string, table string) (bool, error)

	// CreateHistoryTableSQL returns the statements creating the history table,
	// byte for byte compatible with the one Flyway creates
	CreateHistoryTableSQL(schema string, table string) []string

	// SupportsDDLTransactions reports whether DDL can be rolled back
	SupportsDDLTransactions() bool

	// BooleanLiteral renders a boolean for this dialect
	BooleanLiteral(value bool) string

	// SetSessionSchema points the session at the given schema, when possible
	SetSessionSchema(db *sql.DB, schema string) error
}

// Connection bundles an open database handle with its dialect
type Connection struct {
	db      *sql.DB
	dialect Dialect
	url     string
}

// -----------------------------------------------------------------------------
// Connect
//
// Opens a connection from a Flyway style JDBC url (or its native equivalent),
// retrying up to connectRetries times, one second apart, the way the Flyway
// command line does.
// -----------------------------------------------------------------------------
func Connect(url string, user string, password string, connectRetries int) (*Connection, error) {
	driver, dsn, dialect, err := ParseURL(url, user, password)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= connectRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		if lastErr = db.Ping(); lastErr == nil {
			return &Connection{db: db, dialect: dialect, url: url}, nil
		}
	}

	db.Close()

	return nil, fmt.Errorf("unable to connect to the database: %w", lastErr)
}

// -----------------------------------------------------------------------------
// NewConnectionFromDB
//
// Wraps an already open handle, which is what the tests use.
// -----------------------------------------------------------------------------
func NewConnectionFromDB(db *sql.DB, dialect Dialect) *Connection {
	return &Connection{db: db, dialect: dialect}
}

// -----------------------------------------------------------------------------
// DB
// -----------------------------------------------------------------------------
func (c *Connection) DB() *sql.DB {
	return c.db
}

// -----------------------------------------------------------------------------
// Dialect
// -----------------------------------------------------------------------------
func (c *Connection) Dialect() Dialect {
	return c.dialect
}

// -----------------------------------------------------------------------------
// URL
// -----------------------------------------------------------------------------
func (c *Connection) URL() string {
	return c.url
}

// -----------------------------------------------------------------------------
// Close
// -----------------------------------------------------------------------------
func (c *Connection) Close() error {
	if c == nil || c.db == nil {
		return nil
	}

	err := c.db.Close()
	c.db = nil

	return err
}

// -----------------------------------------------------------------------------
// ParseURL
//
// Turns a Flyway JDBC url into the driver name and DSN the matching Go driver
// expects. Native Go DSNs (postgres://, mysql://, ...) are accepted too, so the
// tool is usable without dragging JDBC syntax around.
// -----------------------------------------------------------------------------
func ParseURL(url string, user string, password string) (string, string, Dialect, error) {
	if url == "" {
		return "", "", nil, errors.New("no database url provided, use -url=jdbc:postgresql://host:port/database")
	}

	lower := strings.ToLower(url)

	switch {
	case strings.HasPrefix(lower, "jdbc:postgresql:"), strings.HasPrefix(lower, "postgres://"),
		strings.HasPrefix(lower, "postgresql://"):
		dsn, err := postgresDSN(url, user, password)
		return "pgx", dsn, &postgresDialect{}, err

	case strings.HasPrefix(lower, "jdbc:mysql:"), strings.HasPrefix(lower, "jdbc:mariadb:"),
		strings.HasPrefix(lower, "mysql://"):
		dsn, err := mysqlDSN(url, user, password)
		return "mysql", dsn, &mysqlDialect{}, err

	case strings.HasPrefix(lower, "jdbc:sqlserver:"), strings.HasPrefix(lower, "sqlserver://"):
		dsn, err := mssqlDSN(url, user, password)
		return "sqlserver", dsn, &mssqlDialect{}, err

	case strings.HasPrefix(lower, "jdbc:sqlite:"), strings.HasPrefix(lower, "sqlite://"),
		strings.HasPrefix(lower, "file:"):
		return "sqlite", sqliteDSN(url), &sqliteDialect{}, nil
	}

	return "", "", nil, fmt.Errorf("unsupported database url: %s (expected jdbc:postgresql:, jdbc:mysql:, jdbc:sqlserver: or jdbc:sqlite:)", url)
}

// -----------------------------------------------------------------------------
// DialectByName
//
// Resolves a dialect from its short name, which is handy for tests and for the
// -type command line flag.
// -----------------------------------------------------------------------------
func DialectByName(name string) (Dialect, error) {
	switch strings.ToLower(name) {
	case "pg", "postgres", "postgresql":
		return &postgresDialect{}, nil
	case "mysql", "mariadb":
		return &mysqlDialect{}, nil
	case "mssql", "sqlserver":
		return &mssqlDialect{}, nil
	case "sqlite", "sqlite3":
		return &sqliteDialect{}, nil
	}

	return nil, fmt.Errorf("unsupported database type: %s (try pg | mysql | mssql | sqlite)", name)
}

// -----------------------------------------------------------------------------
// quoteWith
//
// Quotes each non empty part with the given delimiters and joins them with a
// dot, doubling any closing delimiter found inside.
// -----------------------------------------------------------------------------
func quoteWith(open string, close string, parts []string) string {
	quoted := make([]string, 0, len(parts))

	for _, part := range parts {
		if part == "" {
			continue
		}
		quoted = append(quoted, open+strings.ReplaceAll(part, close, close+close)+close)
	}

	return strings.Join(quoted, ".")
}
