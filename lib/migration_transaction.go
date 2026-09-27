// Copyright (C) 2026 Pau Sanchez
//
// A migration owns one physical connection, including COPY protocol operations.
package lib

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Registered only in builds containing the PostgreSQL driver.
var postgresCopyFrom func(*sql.Conn, string, string) error

type migrationTransaction struct {
	*sql.Tx
	conn *sql.Conn
}

// -----------------------------------------------------------------------------
// beginMigration
// -----------------------------------------------------------------------------
func (c *Connection) beginMigration() (*migrationTransaction, error) {
	conn, err := c.DB().Conn(context.Background())
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &migrationTransaction{Tx: tx, conn: conn}, nil
}

// -----------------------------------------------------------------------------
// Commit
// -----------------------------------------------------------------------------
func (m *migrationTransaction) Commit() error {
	return errors.Join(m.Tx.Commit(), m.conn.Close())
}

// -----------------------------------------------------------------------------
// Rollback
// -----------------------------------------------------------------------------
func (m *migrationTransaction) Rollback() error {
	return errors.Join(m.Tx.Rollback(), m.conn.Close())
}

// -----------------------------------------------------------------------------
// copyFrom
// -----------------------------------------------------------------------------
func (m *migrationTransaction) copyFrom(statement, data string) error {
	if postgresCopyFrom == nil {
		return fmt.Errorf("this build cannot execute PostgreSQL COPY")
	}
	return postgresCopyFrom(m.conn, statement, data)
}
