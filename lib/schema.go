// Copyright (C) 2026 Pau Sanchez
//
// Schema inspection for the safeguards around adopting unmanaged databases.
package lib

import (
	"fmt"
	"slices"
	"strings"
)

// -----------------------------------------------------------------------------
// applicationSchemas
//
// The configured schemas with the default one first when it is not among them,
// which is SchemaHistoryFactory.prepareSchemas. The order shows up in the
// SCHEMA marker row, so it has to be Flyway's.
// -----------------------------------------------------------------------------
func (g *Gofly) applicationSchemas() []string {
	schemas := []string{}
	for _, schema := range g.Config.Schemas {
		if !slices.Contains(schemas, schema) {
			schemas = append(schemas, schema)
		}
	}
	if !slices.Contains(schemas, g.defaultSchema) {
		schemas = append([]string{g.defaultSchema}, schemas...)
	}
	return schemas
}

// -----------------------------------------------------------------------------
// nonEmptySchemas
// -----------------------------------------------------------------------------
func (g *Gofly) nonEmptySchemas() ([]string, error) {
	nonEmpty := []string{}
	for _, schema := range g.applicationSchemas() {
		empty, err := g.schemaEmpty(schema)
		if err != nil {
			return nil, err
		}
		if !empty {
			nonEmpty = append(nonEmpty, schema)
		}
	}
	return nonEmpty, nil
}

// -----------------------------------------------------------------------------
// schemasEmpty
// -----------------------------------------------------------------------------
func (g *Gofly) schemasEmpty() (bool, error) {
	nonEmpty, err := g.nonEmptySchemas()
	return len(nonEmpty) == 0, err
}

// -----------------------------------------------------------------------------
// schemaEmpty
//
// Each query is the matching Flyway XxxSchema.doEmpty, so that gofly refuses an
// unmanaged schema exactly when Flyway does. PostgreSQL skips objects owned by
// an extension, SQLite only counts tables. Our own history never counts.
// -----------------------------------------------------------------------------
func (g *Gofly) schemaEmpty(schema string) (bool, error) {
	history := ""
	if schema == g.History.schema || g.History.schema == "" || !g.Connection.Dialect().SupportsSchemas() {
		history = g.History.table
	}
	db := g.Connection.sessionDB()
	var query string
	args := []any{schema, history}
	switch g.Connection.Dialect().Name() {
	case DialectPostgres:
		query = `SELECT count(*) FROM (
    SELECT c.oid FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
    LEFT JOIN pg_catalog.pg_depend d ON d.objid = c.oid AND d.deptype = 'e'
    WHERE n.nspname = $1 AND d.objid IS NULL AND c.relkind IN ('r', 'v', 'S', 't') AND c.relname <> $2
  UNION ALL
    SELECT t.oid FROM pg_catalog.pg_type t
    JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
    LEFT JOIN pg_catalog.pg_depend d ON d.objid = t.oid AND d.deptype = 'e'
    WHERE n.nspname = $1 AND d.objid IS NULL AND t.typcategory NOT IN ('A', 'C')
  UNION ALL
    SELECT p.oid FROM pg_catalog.pg_proc p
    JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
    LEFT JOIN pg_catalog.pg_depend d ON d.objid = p.oid AND d.deptype = 'e'
    WHERE n.nspname = $1 AND d.objid IS NULL
) objects`
	case DialectSqlite:
		query = `SELECT count(*) FROM sqlite_master
   WHERE type = 'table' AND tbl_name NOT IN ('android_metadata', 'sqlite_sequence', ?)`
		args = []any{history}
	case DialectMysql:
		query = `SELECT (SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name <> ?)
   + (SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = ? AND table_name <> ?)
   + (SELECT count(*) FROM information_schema.triggers WHERE event_object_schema = ?)
   + (SELECT count(*) FROM information_schema.routines WHERE routine_schema = ?)`
		args = []any{schema, history, schema, history, schema, schema}
	case DialectMssql:
		query = `SELECT (SELECT count(*) FROM sys.objects AS obj
     LEFT JOIN sys.extended_properties AS eps ON obj.object_id = eps.major_id AND eps.class = 1
       AND eps.minor_id = 0 AND eps.name = 'microsoft_database_tools_support'
     WHERE SCHEMA_NAME(obj.schema_id) = @p1 AND eps.major_id IS NULL AND obj.is_ms_shipped = 0
       AND obj.type IN ('FN', 'AF', 'FS', 'FT', 'TF', 'P', 'PC', 'U', 'SN', 'SO', 'F', 'V')
       AND obj.name <> @p2)
   + (SELECT count(*) FROM sys.types t JOIN sys.schemas s ON t.schema_id = s.schema_id
     WHERE t.is_user_defined = 1 AND s.name = @p1)
   + (SELECT count(*) FROM sys.assemblies WHERE is_user_defined = 1)`
	default:
		return false, fmt.Errorf("cannot inspect schema for %s", g.Connection.Dialect().Name())
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return false, err
	}

	// MariaDB refuses to list events while the event scheduler is off, and
	// Flyway leaves them out then
	if count == 0 && g.Connection.Dialect().Name() == DialectMysql {
		var events int
		err := db.QueryRow(`SELECT count(*) FROM information_schema.events WHERE event_schema = ?`, schema).Scan(&events)
		if err == nil {
			count = events
		}
	}
	return count == 0, nil
}

// -----------------------------------------------------------------------------
// checkUnmanagedSchema
//
// Flyway's migrate refuses a non-empty schema without a history table, with
// this message, unless it may baseline it or is only recording migrations.
// -----------------------------------------------------------------------------
func (g *Gofly) checkUnmanagedSchema() error {
	exists, err := g.History.Exists()
	if err != nil || exists {
		return err
	}
	if g.Config.ImportFromFlyway {
		_, exists, err = g.flywayHistory()
		if err != nil || exists {
			return err
		}
	}
	if g.Config.BaselineOnMigrate || g.Config.SkipExecutingMigrations {
		return nil
	}
	nonEmpty, err := g.nonEmptySchemas()
	if err != nil || len(nonEmpty) == 0 {
		return err
	}
	quoted := make([]string, len(nonEmpty))
	for i, schema := range nonEmpty {
		quoted[i] = g.Connection.Dialect().QuoteIdentifier(schema)
	}
	return fmt.Errorf("Found non-empty schema(s) %s but no schema history table. Use baseline() or set baselineOnMigrate to true to initialize the schema history table.",
		strings.Join(quoted, ", "))
}

// -----------------------------------------------------------------------------
// ensureApplicationSchemas
//
// PostgreSQL silently ignores nonexistent search_path entries. Create every
// requested schema before any migration can fall through into public. Returns
// the schemas it created, for the SCHEMA marker.
// -----------------------------------------------------------------------------
func (g *Gofly) ensureApplicationSchemas() ([]string, error) {
	dialect := g.Connection.Dialect()
	if !dialect.SupportsSchemas() {
		return nil, nil
	}
	created := []string{}
	for _, schema := range g.applicationSchemas() {
		exists, err := dialect.SchemaExists(g.Connection.sessionDB(), schema)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		for _, statement := range dialect.CreateSchemaSQL(schema) {
			if _, err := g.Connection.sessionDB().Exec(statement); err != nil {
				return nil, fmt.Errorf("cannot create application schema %s: %w", schema, err)
			}
		}
		created = append(created, schema)
	}
	return created, dialect.SetSessionSchema(g.Connection.sessionDB(), g.defaultSchema)
}

// -----------------------------------------------------------------------------
// schemaMarker
//
// The row Flyway's SchemaHistory.addSchemasMarker writes after creating the
// schemas along with a new history table. Flyway's clean relies on it to know
// which schemas it may drop.
// -----------------------------------------------------------------------------
func (g *Gofly) schemaMarker(created []string) *AppliedMigration {
	quoted := make([]string, len(created))
	for i, schema := range created {
		quoted[i] = g.Connection.Dialect().QuoteIdentifier(schema)
	}
	return &AppliedMigration{
		InstalledRank: 0,
		Description:   "<< Flyway Schema Creation >>",
		Type:          MigrationTypeSchema,
		Script:        strings.Join(quoted, ","),
		InstalledBy:   g.Config.ResolveInstalledBy(),
		Success:       true,
	}
}

// -----------------------------------------------------------------------------
// withoutSchemaMarker
//
// The SCHEMA row records that schemas were created, not a migration, so it does
// not stop a baseline.
// -----------------------------------------------------------------------------
func withoutSchemaMarker(applied []*AppliedMigration) []*AppliedMigration {
	rows := []*AppliedMigration{}
	for _, row := range applied {
		if row.Type != MigrationTypeSchema {
			rows = append(rows, row)
		}
	}
	return rows
}
