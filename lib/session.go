// Copyright (C) 2026 Pau Sanchez
//
// PostgreSQL session state and advisory locks, and MySQL's USE, must stay on one
// physical connection.
package lib

import (
	"context"
	"database/sql"
)

// Database is the query surface used by dialects, pools and pinned sessions.
type Database interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Begin() (*sql.Tx, error)
}

type pinnedDatabase struct{ conn *sql.Conn }

// -----------------------------------------------------------------------------
// Exec
// -----------------------------------------------------------------------------
func (p pinnedDatabase) Exec(query string, args ...any) (sql.Result, error) {
	return p.conn.ExecContext(context.Background(), query, args...)
}

// -----------------------------------------------------------------------------
// Query
// -----------------------------------------------------------------------------
func (p pinnedDatabase) Query(query string, args ...any) (*sql.Rows, error) {
	return p.conn.QueryContext(context.Background(), query, args...)
}

// -----------------------------------------------------------------------------
// QueryRow
// -----------------------------------------------------------------------------
func (p pinnedDatabase) QueryRow(query string, args ...any) *sql.Row {
	return p.conn.QueryRowContext(context.Background(), query, args...)
}

// -----------------------------------------------------------------------------
// Begin
// -----------------------------------------------------------------------------
func (p pinnedDatabase) Begin() (*sql.Tx, error) { return p.conn.BeginTx(context.Background(), nil) }

// -----------------------------------------------------------------------------
// pinSession
// -----------------------------------------------------------------------------
func (c *Connection) pinSession() error {
	name := c.Dialect().Name()
	if (name != DialectPostgres && name != DialectMysql) || c.session != nil {
		return nil
	}
	conn, err := c.db.Conn(context.Background())
	if err != nil {
		return err
	}
	c.session = conn
	return nil
}

// -----------------------------------------------------------------------------
// sessionDB
// -----------------------------------------------------------------------------
func (c *Connection) sessionDB() Database {
	if c.session != nil {
		return pinnedDatabase{c.session}
	}
	return c.db
}
