// Copyright (C) 2026 Pau Sanchez
//
// URL parsing and the per dialect DDL.
package lib

import (
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// TestParsePostgresURL
// -----------------------------------------------------------------------------
func TestParsePostgresURL(t *testing.T) {
	driver, dsn, dialect, err := ParseURL("jdbc:postgresql://db:5432/ultirent", "admin", "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if driver != "pgx" || dialect.Name() != DialectPostgres {
		t.Errorf("driver %q dialect %q", driver, dialect.Name())
	}
	if !strings.Contains(dsn, "admin:secret@db:5432/ultirent") {
		t.Errorf("dsn built as %q", dsn)
	}
}

// -----------------------------------------------------------------------------
// TestParsePostgresURLDefaultsThePort
// -----------------------------------------------------------------------------
func TestParsePostgresURLDefaultsThePort(t *testing.T) {
	_, dsn, _, err := ParseURL("jdbc:postgresql://db/mydb", "u", "p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, "db:5432") {
		t.Errorf("dsn built as %q, want the default port", dsn)
	}
}

// -----------------------------------------------------------------------------
// TestParseMysqlURL
//
// This is the url shape artypist passes to Flyway.
// -----------------------------------------------------------------------------
func TestParseMysqlURL(t *testing.T) {
	driver, dsn, dialect, err := ParseURL("jdbc:mysql://mysqldb:3306/artypistdb", "root", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if driver != "mysql" || dialect.Name() != DialectMysql {
		t.Errorf("driver %q dialect %q", driver, dialect.Name())
	}
	if !strings.HasPrefix(dsn, "root:pass@tcp(mysqldb:3306)/artypistdb?") {
		t.Errorf("dsn built as %q", dsn)
	}
}

// -----------------------------------------------------------------------------
// TestParseMariadbURL
// -----------------------------------------------------------------------------
func TestParseMariadbURL(t *testing.T) {
	_, dsn, dialect, err := ParseURL("jdbc:mariadb://db/mydb", "u", "p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dialect.Name() != DialectMysql {
		t.Errorf("mariadb should use the mysql dialect, got %q", dialect.Name())
	}
	if !strings.Contains(dsn, "tcp(db:3306)/mydb") {
		t.Errorf("dsn built as %q", dsn)
	}
}

// -----------------------------------------------------------------------------
// TestParseSqlServerURL
// -----------------------------------------------------------------------------
func TestParseSqlServerURL(t *testing.T) {
	driver, dsn, dialect, err := ParseURL(
		"jdbc:sqlserver://host:1433;databaseName=dbtest;encrypt=false", "sa", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if driver != "sqlserver" || dialect.Name() != DialectMssql {
		t.Errorf("driver %q dialect %q", driver, dialect.Name())
	}
	if !strings.Contains(dsn, "sa:pw@host:1433") {
		t.Errorf("dsn built as %q", dsn)
	}
	if !strings.Contains(dsn, "database=dbtest") {
		t.Errorf("databaseName was not mapped: %q", dsn)
	}
	if !strings.Contains(dsn, "encrypt=false") {
		t.Errorf("the extra property was dropped: %q", dsn)
	}
}

// -----------------------------------------------------------------------------
// TestParseSqliteURL
// -----------------------------------------------------------------------------
func TestParseSqliteURL(t *testing.T) {
	cases := map[string]string{
		"jdbc:sqlite:/tmp/test.db": "/tmp/test.db",
		"jdbc:sqlite:test.db":      "test.db",
		"sqlite:///tmp/test.db":    "/tmp/test.db",
	}

	for url, expected := range cases {
		driver, dsn, dialect, err := ParseURL(url, "", "")
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if driver != "sqlite" || dialect.Name() != DialectSqlite {
			t.Errorf("%s: driver %q dialect %q", url, driver, dialect.Name())
		}
		if dsn != expected {
			t.Errorf("%s: dsn %q, want %q", url, dsn, expected)
		}
	}
}

// -----------------------------------------------------------------------------
// TestParseURLRejectsWhatItCannotHandle
// -----------------------------------------------------------------------------
func TestParseURLRejectsWhatItCannotHandle(t *testing.T) {
	for _, url := range []string{"", "jdbc:oracle:thin:@//host:1521/svc", "jdbc:h2:mem:test", "nonsense"} {
		if _, _, _, err := ParseURL(url, "u", "p"); err == nil {
			t.Errorf("%q should have been rejected", url)
		}
	}
}

// -----------------------------------------------------------------------------
// TestDialectByName
// -----------------------------------------------------------------------------
func TestDialectByName(t *testing.T) {
	cases := map[string]string{
		"pg":        DialectPostgres,
		"postgres":  DialectPostgres,
		"mysql":     DialectMysql,
		"mariadb":   DialectMysql,
		"mssql":     DialectMssql,
		"sqlserver": DialectMssql,
		"sqlite":    DialectSqlite,
	}

	for name, expected := range cases {
		dialect, err := DialectByName(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dialect.Name() != expected {
			t.Errorf("%s resolved to %q, want %q", name, dialect.Name(), expected)
		}
	}

	if _, err := DialectByName("oracle"); err == nil {
		t.Error("oracle should have been rejected")
	}
}

// -----------------------------------------------------------------------------
// TestHistoryTableDDLMatchesFlyway
//
// The column names, order and types have to line up with Flyway's, otherwise
// the import and any later hand off back to Flyway would break.
// -----------------------------------------------------------------------------
func TestHistoryTableDDLMatchesFlyway(t *testing.T) {
	columns := []string{
		"installed_rank", "version", "description", "type", "script",
		"checksum", "installed_by", "installed_on", "execution_time", "success",
	}

	dialects := []Dialect{
		&postgresDialect{}, &mysqlDialect{}, &mssqlDialect{}, &sqliteDialect{},
	}

	for _, dialect := range dialects {
		statements := dialect.CreateHistoryTableSQL("myschema", "gofly_schema_history")
		create := statements[0]

		for _, column := range columns {
			if !strings.Contains(create, column) {
				t.Errorf("%s: the DDL is missing the %s column:\n%s", dialect.Name(), column, create)
			}
		}

		joined := strings.Join(statements, "\n")
		if !strings.Contains(joined, "gofly_schema_history_s_idx") {
			t.Errorf("%s: the success index is missing", dialect.Name())
		}
		if dialect.Name() != DialectSqlite && !strings.Contains(joined, "gofly_schema_history_pk") {
			t.Errorf("%s: the primary key constraint is missing", dialect.Name())
		}
	}
}

// -----------------------------------------------------------------------------
// TestQuoteIdentifier
// -----------------------------------------------------------------------------
func TestQuoteIdentifier(t *testing.T) {
	cases := []struct {
		dialect  Dialect
		expected string
	}{
		{&postgresDialect{}, `"s"."t"`},
		{&mysqlDialect{}, "`s`.`t`"},
		{&mssqlDialect{}, "[s].[t]"},
		{&sqliteDialect{}, `"s"."t"`},
	}

	for _, c := range cases {
		if got := c.dialect.QuoteIdentifier("s", "t"); got != c.expected {
			t.Errorf("%s: quoted as %s, want %s", c.dialect.Name(), got, c.expected)
		}

		// an empty part is simply left out
		if got := c.dialect.QuoteIdentifier("", "t"); strings.Contains(got, ".") {
			t.Errorf("%s: an empty schema should not produce %s", c.dialect.Name(), got)
		}
	}
}

// -----------------------------------------------------------------------------
// TestPlaceholderSyntaxPerDialect
// -----------------------------------------------------------------------------
func TestPlaceholderSyntaxPerDialect(t *testing.T) {
	cases := []struct {
		dialect  Dialect
		expected string
	}{
		{&postgresDialect{}, "$2"},
		{&mysqlDialect{}, "?"},
		{&mssqlDialect{}, "@p2"},
		{&sqliteDialect{}, "?"},
	}

	for _, c := range cases {
		if got := c.dialect.Placeholder(2); got != c.expected {
			t.Errorf("%s: placeholder %q, want %q", c.dialect.Name(), got, c.expected)
		}
	}
}

// -----------------------------------------------------------------------------
// TestDefaultHistorySchema
// -----------------------------------------------------------------------------
func TestDefaultHistorySchema(t *testing.T) {
	if (&postgresDialect{}).DefaultHistorySchema() != DefaultGoflySchema {
		t.Error("postgresql keeps the history in its own schema")
	}
	if (&mssqlDialect{}).DefaultHistorySchema() != DefaultGoflySchema {
		t.Error("sql server keeps the history in its own schema")
	}
	if (&mysqlDialect{}).DefaultHistorySchema() != "" {
		t.Error("mysql would need a whole database, so it stays alongside by default")
	}
	if (&sqliteDialect{}).DefaultHistorySchema() != "" {
		t.Error("sqlite has no schemas")
	}
}
