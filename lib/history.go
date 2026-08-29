// Copyright (C) 2026 Pau Sanchez
//
// The schema history table.
//
// gofly keeps its own table, in its own schema when the database supports it,
// with exactly the layout Flyway uses. On the very first run against a database
// that was previously migrated with Flyway, every row of flyway_schema_history
// is copied over so that the history, and therefore the checksum validation,
// carries on uninterrupted.
package lib

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// AppliedMigration is one row of the schema history table
type AppliedMigration struct {
	InstalledRank int
	Version       *Version
	Description   string
	Type          MigrationType
	Script        string
	Checksum      *int32
	InstalledBy   string
	InstalledOn   time.Time
	ExecutionTime int
	Success       bool
}

// SchemaHistory reads and writes the history table
type SchemaHistory struct {
	connection  *Connection
	schema      string
	table       string
	installedBy string
}

// -----------------------------------------------------------------------------
// NewSchemaHistory
// -----------------------------------------------------------------------------
func NewSchemaHistory(connection *Connection, schema string, table string, installedBy string) *SchemaHistory {
	return &SchemaHistory{
		connection:  connection,
		schema:      schema,
		table:       table,
		installedBy: installedBy,
	}
}

// -----------------------------------------------------------------------------
// QualifiedName
// -----------------------------------------------------------------------------
func (h *SchemaHistory) QualifiedName() string {
	if h.schema == "" || !h.connection.Dialect().SupportsSchemas() {
		return h.connection.Dialect().QuoteIdentifier(h.table)
	}

	return h.connection.Dialect().QuoteIdentifier(h.schema, h.table)
}

// -----------------------------------------------------------------------------
// Exists
// -----------------------------------------------------------------------------
func (h *SchemaHistory) Exists() (bool, error) {
	return h.connection.Dialect().TableExists(h.connection.DB(), h.schema, h.table)
}

// -----------------------------------------------------------------------------
// Create
//
// Creates the schema, if needed, and then the history table itself.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) Create() error {
	dialect := h.connection.Dialect()
	db := h.connection.DB()

	if h.schema != "" && dialect.SupportsSchemas() {
		exists, err := dialect.SchemaExists(db, h.schema)
		if err != nil {
			return err
		}
		if !exists {
			for _, statement := range dialect.CreateSchemaSQL(h.schema) {
				if _, err := db.Exec(statement); err != nil {
					return fmt.Errorf("cannot create schema %s: %w", h.schema, err)
				}
			}
		}
	}

	for _, statement := range dialect.CreateHistoryTableSQL(h.schema, h.table) {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("cannot create the schema history table %s: %w", h.QualifiedName(), err)
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// All
//
// Returns every row, ordered by installed_rank, which is the order in which the
// migrations were applied.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) All() ([]*AppliedMigration, error) {
	query := `SELECT ` + h.columnList() + ` FROM ` + h.QualifiedName() + ` ORDER BY "installed_rank"`
	query = h.rewriteQuotes(query)

	rows, err := h.connection.DB().Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAppliedMigrations(rows)
}

// -----------------------------------------------------------------------------
// Insert
//
// Records the outcome of a migration.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) Insert(executor sqlExecutor, migration *AppliedMigration) error {
	dialect := h.connection.Dialect()

	placeholders := make([]string, 9)
	for index := range placeholders {
		placeholders[index] = dialect.Placeholder(index + 1)
	}

	statement := "INSERT INTO " + h.QualifiedName() + " (" + h.columnListForInsert() + ") VALUES (" +
		strings.Join(placeholders, ", ") + ")"

	var version interface{}
	if migration.Version != nil && !migration.Version.IsPredefined() {
		version = migration.Version.RawVersion()
	}

	var checksum interface{}
	if migration.Checksum != nil {
		checksum = *migration.Checksum
	}

	_, err := executor.Exec(h.rewriteQuotes(statement),
		migration.InstalledRank,
		version,
		AbbreviateDescription(migration.Description),
		string(migration.Type),
		AbbreviateScript(migration.Script),
		checksum,
		migration.InstalledBy,
		migration.ExecutionTime,
		migration.Success,
	)

	return err
}

// -----------------------------------------------------------------------------
// UpdateChecksum
//
// Realigns the stored description, type and checksum with the migration on
// disk, which is what `repair` does.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) UpdateChecksum(installedRank int, description string, migrationType MigrationType, checksum *int32) error {
	dialect := h.connection.Dialect()

	statement := "UPDATE " + h.QualifiedName() +
		` SET "description" = ` + dialect.Placeholder(1) +
		`, "type" = ` + dialect.Placeholder(2) +
		`, "checksum" = ` + dialect.Placeholder(3) +
		` WHERE "installed_rank" = ` + dialect.Placeholder(4)

	var checksumValue interface{}
	if checksum != nil {
		checksumValue = *checksum
	}

	_, err := h.connection.DB().Exec(h.rewriteQuotes(statement),
		AbbreviateDescription(description), string(migrationType), checksumValue, installedRank)

	return err
}

// -----------------------------------------------------------------------------
// MarkAsDeleted
//
// Turns an applied migration into a DELETE marker, which is how `repair`
// records a migration that no longer exists locally.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) MarkAsDeleted(installedRank int) error {
	dialect := h.connection.Dialect()

	statement := "UPDATE " + h.QualifiedName() +
		` SET "type" = ` + dialect.Placeholder(1) +
		` WHERE "installed_rank" = ` + dialect.Placeholder(2)

	_, err := h.connection.DB().Exec(h.rewriteQuotes(statement), string(MigrationTypeDelete), installedRank)

	return err
}

// -----------------------------------------------------------------------------
// RemoveFailed
//
// Deletes the rows of the migrations that failed, which is the other half of
// what `repair` does.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) RemoveFailed() (int64, error) {
	dialect := h.connection.Dialect()

	statement := "DELETE FROM " + h.QualifiedName() +
		` WHERE "success" = ` + dialect.BooleanLiteral(false)

	result, err := h.connection.DB().Exec(h.rewriteQuotes(statement))
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// -----------------------------------------------------------------------------
// NextInstalledRank
// -----------------------------------------------------------------------------
func (h *SchemaHistory) NextInstalledRank() (int, error) {
	query := h.rewriteQuotes(`SELECT MAX("installed_rank") FROM ` + h.QualifiedName())

	var highest sql.NullInt64
	if err := h.connection.DB().QueryRow(query).Scan(&highest); err != nil {
		return 0, err
	}

	return int(highest.Int64) + 1, nil
}

// -----------------------------------------------------------------------------
// ImportFromFlyway
//
// Copies every row of an existing Flyway history table into the gofly one.
// Returns how many rows were moved, or zero when there was nothing to import.
//
// This runs only when the gofly table has just been created, so an import never
// overwrites a history gofly is already maintaining.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) ImportFromFlyway(flywaySchema string, flywayTable string) (int, error) {
	dialect := h.connection.Dialect()
	db := h.connection.DB()

	exists, err := dialect.TableExists(db, flywaySchema, flywayTable)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}

	source := dialect.QuoteIdentifier(flywayTable)
	if flywaySchema != "" && dialect.SupportsSchemas() {
		source = dialect.QuoteIdentifier(flywaySchema, flywayTable)
	}

	// gofly never touches the Flyway table, it only reads it, so a rollback of
	// the migration that follows leaves the original history untouched
	query := h.rewriteQuotes(`SELECT ` + h.columnList() + ` FROM ` + source + ` ORDER BY "installed_rank"`)

	rows, err := db.Query(query)
	if err != nil {
		return 0, fmt.Errorf("cannot read the flyway schema history %s: %w", source, err)
	}

	migrations, err := scanAppliedMigrations(rows)
	rows.Close()
	if err != nil {
		return 0, err
	}

	transaction, err := db.Begin()
	if err != nil {
		return 0, err
	}

	for _, migration := range migrations {
		if err := h.Insert(transaction, migration); err != nil {
			transaction.Rollback()
			return 0, fmt.Errorf("cannot import row %d of %s: %w", migration.InstalledRank, source, err)
		}
	}

	if err := transaction.Commit(); err != nil {
		return 0, err
	}

	return len(migrations), nil
}

// -----------------------------------------------------------------------------
// columnList
// -----------------------------------------------------------------------------
func (h *SchemaHistory) columnList() string {
	return `"installed_rank", "version", "description", "type", "script", "checksum",` +
		` "installed_by", "installed_on", "execution_time", "success"`
}

// -----------------------------------------------------------------------------
// columnListForInsert
// -----------------------------------------------------------------------------
func (h *SchemaHistory) columnListForInsert() string {
	return h.rewriteQuotes(`"installed_rank", "version", "description", "type", "script", "checksum",` +
		` "installed_by", "execution_time", "success"`)
}

// -----------------------------------------------------------------------------
// rewriteQuotes
//
// The statements above are written with the ANSI double quote, which is what
// PostgreSQL, SQLite and SQL Server understand. MySQL needs backticks unless
// ANSI_QUOTES is on, so the quotes are swapped for the dialects that need it.
// -----------------------------------------------------------------------------
func (h *SchemaHistory) rewriteQuotes(statement string) string {
	switch h.connection.Dialect().Name() {
	case DialectMysql:
		return strings.ReplaceAll(statement, `"`, "`")
	case DialectMssql:
		return replaceQuotedIdentifiers(statement, "[", "]")
	}

	return statement
}

// -----------------------------------------------------------------------------
// replaceQuotedIdentifiers
//
// Swaps every "identifier" for the bracketed form SQL Server prefers. Only the
// statements this file builds go through here, and none of them carries string
// literals, so a straight pairwise replacement is enough.
// -----------------------------------------------------------------------------
func replaceQuotedIdentifiers(statement string, open string, close string) string {
	result := strings.Builder{}
	inside := false

	for _, char := range statement {
		if char == '"' {
			if inside {
				result.WriteString(close)
			} else {
				result.WriteString(open)
			}
			inside = !inside
			continue
		}
		result.WriteRune(char)
	}

	return result.String()
}

// sqlExecutor is implemented by both *sql.DB and *sql.Tx
type sqlExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// -----------------------------------------------------------------------------
// scanAppliedMigrations
// -----------------------------------------------------------------------------
func scanAppliedMigrations(rows *sql.Rows) ([]*AppliedMigration, error) {
	migrations := []*AppliedMigration{}

	for rows.Next() {
		var (
			installedRank int
			version       sql.NullString
			description   sql.NullString
			migrationType string
			script        string
			checksum      sql.NullInt64
			installedBy   string
			installedOn   interface{}
			executionTime int
			success       bool
		)

		err := rows.Scan(&installedRank, &version, &description, &migrationType, &script,
			&checksum, &installedBy, &installedOn, &executionTime, &success)
		if err != nil {
			return nil, err
		}

		migration := &AppliedMigration{
			InstalledRank: installedRank,
			Description:   description.String,
			Type:          MigrationType(migrationType),
			Script:        script,
			InstalledBy:   installedBy,
			InstalledOn:   parseInstalledOn(installedOn),
			ExecutionTime: executionTime,
			Success:       success,
		}

		if version.Valid && version.String != "" {
			parsed, err := NewVersion(version.String)
			if err != nil {
				return nil, fmt.Errorf("the schema history holds an invalid version %q: %w", version.String, err)
			}
			migration.Version = parsed
		}

		if checksum.Valid {
			value := int32(checksum.Int64)
			migration.Checksum = &value
		}

		migrations = append(migrations, migration)
	}

	return migrations, rows.Err()
}

// -----------------------------------------------------------------------------
// parseInstalledOn
//
// SQLite keeps installed_on as text, the other drivers hand back a time.Time.
// A timestamp we cannot read is only ever printed, so a zero value is a fine
// outcome rather than an error.
// -----------------------------------------------------------------------------
func parseInstalledOn(value interface{}) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case []byte:
		return parseTimestampText(string(typed))
	case string:
		return parseTimestampText(typed)
	}

	return time.Time{}
}

// -----------------------------------------------------------------------------
// parseTimestampText
// -----------------------------------------------------------------------------
func parseTimestampText(text string) time.Time {
	layouts := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}

	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed
		}
	}

	return time.Time{}
}

// -----------------------------------------------------------------------------
// AbbreviateDescription
//
// Descriptions are stored in a VARCHAR(200), Flyway truncates the overflow with
// an ellipsis and gofly has to do the same for the checksums to line up.
// -----------------------------------------------------------------------------
func AbbreviateDescription(description string) string {
	if len(description) <= 200 {
		return description
	}

	return description[:197] + "..."
}

// -----------------------------------------------------------------------------
// AbbreviateScript
// -----------------------------------------------------------------------------
func AbbreviateScript(script string) string {
	if len(script) <= 1000 {
		return script
	}

	return "..." + script[3:1000]
}
