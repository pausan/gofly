// Copyright (C) 2026 Pau Sanchez
//
// Schema inspection for the safeguards around adopting unmanaged databases.
package lib

import "fmt"

// -----------------------------------------------------------------------------
// applicationSchemas
// -----------------------------------------------------------------------------
func (g *Gofly) applicationSchemas() []string {
	schemas := []string{g.defaultSchema}
	for _, schema := range g.Config.Schemas {
		found := false
		for _, existing := range schemas {
			if existing == schema {
				found = true
			}
		}
		if !found {
			schemas = append(schemas, schema)
		}
	}
	return schemas
}

// -----------------------------------------------------------------------------
// schemasEmpty
//
// A schema containing views, routines, sequences or types is not empty just
// because it has no ordinary tables. Our own history never counts as user DDL.
// -----------------------------------------------------------------------------
func (g *Gofly) schemasEmpty() (bool, error) {
	for _, schema := range g.applicationSchemas() {
		empty, err := g.schemaEmpty(schema)
		if err != nil || !empty {
			return false, err
		}
	}
	return true, nil
}

// -----------------------------------------------------------------------------
// schemaEmpty
// -----------------------------------------------------------------------------
func (g *Gofly) schemaEmpty(schema string) (bool, error) {
	history := ""
	if schema == g.historySchema || g.historySchema == "" || !g.Connection.Dialect().SupportsSchemas() {
		history = g.Config.Table
	}
	var query string
	args := []any{schema, history}
	switch g.Connection.Dialect().Name() {
	case DialectPostgres:
		query = `SELECT (
   SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','S','f') AND c.relname<>$2
  ) + (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1)
    + (SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=$1 AND t.typtype IN ('e','d','r'))`
	case DialectSqlite:
		query = `SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND name<>? AND tbl_name<>?`
		args = []any{history, history}
	case DialectMysql:
		query = `SELECT (SELECT count(*) FROM information_schema.tables WHERE table_schema=? AND table_name<>?)
   + (SELECT count(*) FROM information_schema.routines WHERE routine_schema=?)`
		args = []any{schema, history, schema}
	case DialectMssql:
		query = `SELECT count(*) FROM sys.objects o JOIN sys.schemas s ON s.schema_id=o.schema_id
   WHERE s.name=@p1 AND o.is_ms_shipped=0 AND o.name<>@p2
   AND (o.parent_object_id=0 OR OBJECT_NAME(o.parent_object_id)<>@p2)`
	default:
		return false, fmt.Errorf("cannot inspect schema for %s", g.Connection.Dialect().Name())
	}
	var count int
	if err := g.Connection.DB().QueryRow(query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

// -----------------------------------------------------------------------------
// checkUnmanagedSchema
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
	empty, err := g.schemasEmpty()
	if err != nil {
		return err
	}
	if !empty && !g.Config.BaselineOnMigrate {
		return fmt.Errorf("found non-empty schema(s) without a schema history table; use baseline or baselineOnMigrate to initialize the schema history")
	}
	return nil
}

// -----------------------------------------------------------------------------
// ensureApplicationSchemas
//
// PostgreSQL silently ignores nonexistent search_path entries. Create every
// requested schema before any migration can fall through into public.
// -----------------------------------------------------------------------------
func (g *Gofly) ensureApplicationSchemas() error {
	dialect := g.Connection.Dialect()
	if !dialect.SupportsSchemas() {
		return nil
	}
	for _, schema := range g.applicationSchemas() {
		exists, err := dialect.SchemaExists(g.Connection.DB(), schema)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		for _, statement := range dialect.CreateSchemaSQL(schema) {
			if _, err := g.Connection.DB().Exec(statement); err != nil {
				return fmt.Errorf("cannot create application schema %s: %w", schema, err)
			}
		}
	}
	return dialect.SetSessionSchema(g.Connection.DB(), g.defaultSchema)
}
