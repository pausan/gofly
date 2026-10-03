// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"fmt"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// statementTexts
// -----------------------------------------------------------------------------
func statementTexts(statements []Statement) []string {
	texts := make([]string, 0, len(statements))
	for _, statement := range statements {
		texts = append(texts, statement.SQL)
	}

	return texts
}

// -----------------------------------------------------------------------------
// assertStatements
// -----------------------------------------------------------------------------
func assertStatements(t *testing.T, sql string, dialect string, expected []string) {
	t.Helper()

	got := statementTexts(SplitStatements(sql, dialect))
	if len(got) != len(expected) {
		t.Fatalf("got %d statements %q, want %d %q", len(got), got, len(expected), expected)
	}

	for index := range expected {
		if got[index] != expected[index] {
			t.Errorf("statement %d:\n got %q\nwant %q", index, got[index], expected[index])
		}
	}
}

// -----------------------------------------------------------------------------
// TestSplitSimpleStatements
// -----------------------------------------------------------------------------
func TestSplitSimpleStatements(t *testing.T) {
	assertStatements(t,
		"CREATE TABLE a (id INT);\nCREATE TABLE b (id INT);\n",
		DialectSqlite,
		[]string{"CREATE TABLE a (id INT)", "CREATE TABLE b (id INT)"},
	)
}

// -----------------------------------------------------------------------------
// TestSplitTolerateMissingFinalSemicolon
// -----------------------------------------------------------------------------
func TestSplitTolerateMissingFinalSemicolon(t *testing.T) {
	assertStatements(t, "SELECT 1", DialectSqlite, []string{"SELECT 1"})
}

// -----------------------------------------------------------------------------
// TestSplitIgnoresSemicolonsInsideStrings
// -----------------------------------------------------------------------------
func TestSplitIgnoresSemicolonsInsideStrings(t *testing.T) {
	assertStatements(t,
		"INSERT INTO a VALUES ('one; two');\nSELECT 1;",
		DialectSqlite,
		[]string{"INSERT INTO a VALUES ('one; two')", "SELECT 1"},
	)
}

// -----------------------------------------------------------------------------
// TestSplitHandlesDoubledQuotes
// -----------------------------------------------------------------------------
func TestSplitHandlesDoubledQuotes(t *testing.T) {
	assertStatements(t,
		"INSERT INTO a VALUES ('it''s; fine');\nSELECT 1;",
		DialectSqlite,
		[]string{"INSERT INTO a VALUES ('it''s; fine')", "SELECT 1"},
	)
}

// -----------------------------------------------------------------------------
// TestSplitIgnoresSemicolonsInsideComments
// -----------------------------------------------------------------------------
func TestSplitIgnoresSemicolonsInsideComments(t *testing.T) {
	sql := "-- a comment; with a semicolon\nSELECT 1;\n/* another; one */\nSELECT 2;"

	assertStatements(t, sql, DialectSqlite, []string{
		"-- a comment; with a semicolon\nSELECT 1",
		"/* another; one */\nSELECT 2",
	})
}

// -----------------------------------------------------------------------------
// TestSplitDropsCommentOnlyTrailer
// -----------------------------------------------------------------------------
func TestSplitDropsCommentOnlyTrailer(t *testing.T) {
	assertStatements(t,
		"SELECT 1;\n-- nothing else to do\n",
		DialectSqlite,
		[]string{"SELECT 1"},
	)
}

// -----------------------------------------------------------------------------
// TestSplitPostgresDollarQuoting
// -----------------------------------------------------------------------------
func TestSplitPostgresDollarQuoting(t *testing.T) {
	sql := `CREATE FUNCTION f() RETURNS void AS $$
BEGIN
  INSERT INTO a VALUES (1);
  INSERT INTO a VALUES (2);
END;
$$ LANGUAGE plpgsql;
SELECT 1;`

	statements := SplitStatements(sql, DialectPostgres)
	if len(statements) != 2 {
		t.Fatalf("got %d statements: %q", len(statements), statementTexts(statements))
	}
	if !strings.Contains(statements[0].SQL, "LANGUAGE plpgsql") {
		t.Errorf("the function body was split: %q", statements[0].SQL)
	}
	if statements[1].SQL != "SELECT 1" {
		t.Errorf("second statement is %q", statements[1].SQL)
	}
}

// -----------------------------------------------------------------------------
// TestSplitPostgresTaggedDollarQuoting
// -----------------------------------------------------------------------------
func TestSplitPostgresTaggedDollarQuoting(t *testing.T) {
	sql := "SELECT $tag$a;b$tag$;\nSELECT 2;"

	assertStatements(t, sql, DialectPostgres, []string{"SELECT $tag$a;b$tag$", "SELECT 2"})
}

// -----------------------------------------------------------------------------
// TestSplitDollarQuotingIsPostgresOnly
//
// A stray dollar sign in another dialect must not swallow the script.
// -----------------------------------------------------------------------------
func TestSplitDollarQuotingIsPostgresOnly(t *testing.T) {
	assertStatements(t, "SELECT '$100';\nSELECT 2;", DialectMysql,
		[]string{"SELECT '$100'", "SELECT 2"})
}

// -----------------------------------------------------------------------------
// TestSplitMysqlBackticksAndEscapes
// -----------------------------------------------------------------------------
func TestSplitMysqlBackticksAndEscapes(t *testing.T) {
	assertStatements(t,
		"CREATE TABLE `a;b` (id INT);\nINSERT INTO x VALUES ('a\\';b');",
		DialectMysql,
		[]string{"CREATE TABLE `a;b` (id INT)", "INSERT INTO x VALUES ('a\\';b')"},
	)
}

// -----------------------------------------------------------------------------
// TestSplitMysqlDelimiter
// -----------------------------------------------------------------------------
func TestSplitMysqlDelimiter(t *testing.T) {
	sql := `DELIMITER //
CREATE TRIGGER t BEFORE INSERT ON a FOR EACH ROW
BEGIN
  SET NEW.id = 1;
END//
DELIMITER ;
SELECT 1;`

	statements := SplitStatements(sql, DialectMysql)
	if len(statements) != 2 {
		t.Fatalf("got %d statements: %q", len(statements), statementTexts(statements))
	}
	if !strings.Contains(statements[0].SQL, "SET NEW.id = 1;") {
		t.Errorf("the trigger body was split: %q", statements[0].SQL)
	}
	if statements[1].SQL != "SELECT 1" {
		t.Errorf("second statement is %q", statements[1].SQL)
	}
}

// -----------------------------------------------------------------------------
// TestSplitSqlServerGoBatches
// -----------------------------------------------------------------------------
func TestSplitSqlServerGoBatches(t *testing.T) {
	sql := "CREATE TABLE a (id INT)\nGO\nCREATE TABLE b (id INT)\nGO\n"

	assertStatements(t, sql, DialectMssql,
		[]string{"CREATE TABLE a (id INT)", "CREATE TABLE b (id INT)"})
}

// -----------------------------------------------------------------------------
// TestSplitGoIsOnlyABatchSeparatorOnItsOwnLine
// -----------------------------------------------------------------------------
func TestSplitGoIsOnlyABatchSeparatorOnItsOwnLine(t *testing.T) {
	assertStatements(t, "SELECT 'GO GO';\n", DialectMssql, []string{"SELECT 'GO GO'"})
	assertStatements(t, "SELECT GOAL FROM t;\n", DialectMssql, []string{"SELECT GOAL FROM t"})
}

// -----------------------------------------------------------------------------
// TestSplitReportsTheLineEachStatementStartsOn
//
// Flyway reports the line of a statement's first token that is not a comment,
// not the line of the delimiter before it, and error messages point there.
// -----------------------------------------------------------------------------
func TestSplitReportsTheLineEachStatementStartsOn(t *testing.T) {
	cases := []struct {
		name    string
		sql     string
		dialect string
		lines   []int
	}{
		{"blank lines", "SELECT 1;\n\n\nSELECT 2;\n", DialectSqlite, []int{1, 4}},
		{"same line", "SELECT 1; SELECT 2;\n", DialectSqlite, []int{1, 1}},
		{"next line", "SELECT 1;\nSELECT 2;\n", DialectPostgres, []int{1, 2}},
		{"leading comments", "-- first\n/* a\n   b */\nSELECT 1;\n-- second\n\nSELECT 2;\n", DialectPostgres, []int{4, 7}},
		{"multi-line statement", "CREATE TABLE t (\n  id INT\n);\nSELECT 2;\n", DialectMysql, []int{1, 4}},
		{"delimiter", "DELIMITER //\nSELECT 1//\nDELIMITER ;\n\nSELECT 2;\n", DialectMysql, []int{2, 5}},
		{"batch separator", "SELECT 1\nGO\n\nSELECT 2\nGO\n", DialectMssql, []int{1, 4}},
		{"crlf", "SELECT 1;\r\n\r\nSELECT 2;\r\n", DialectSqlite, []int{1, 3}},
	}

	for _, c := range cases {
		statements := SplitStatements(c.sql, c.dialect)
		lines := []int{}
		for _, statement := range statements {
			lines = append(lines, statement.Line)
		}
		if fmt.Sprint(lines) != fmt.Sprint(c.lines) {
			t.Errorf("%s: statements start at lines %v, want %v", c.name, lines, c.lines)
		}
	}
}

// -----------------------------------------------------------------------------
// TestSplitEmptyScript
// -----------------------------------------------------------------------------
func TestSplitEmptyScript(t *testing.T) {
	for _, sql := range []string{"", "\n\n", ";", ";;", "-- only a comment\n"} {
		if statements := SplitStatements(sql, DialectSqlite); len(statements) != 0 {
			t.Errorf("%q produced %q, want nothing", sql, statementTexts(statements))
		}
	}
}

// -----------------------------------------------------------------------------
// TestSplitPostgresEscapeStringsWithoutChangingOrdinaryStrings
// -----------------------------------------------------------------------------
func TestSplitPostgresEscapeStringsWithoutChangingOrdinaryStrings(t *testing.T) {
	for _, prefix := range []string{"E", "e"} {
		literal := prefix + `'it\'s; valid'`
		assertStatements(t, "SELECT "+literal+"; SELECT 2;", DialectPostgres, []string{"SELECT " + literal, "SELECT 2"})
	}
	assertStatements(t, `SELECT '\'; SELECT 2;`, DialectPostgres, []string{`SELECT '\'`, "SELECT 2"})
}

// -----------------------------------------------------------------------------
// TestSplitKeepsNestedPostgresCommentsIntact
// -----------------------------------------------------------------------------
func TestSplitKeepsNestedPostgresCommentsIntact(t *testing.T) {
	statement := "/* outer /* inner */ ; SELECT 'ignored'; */ SELECT 42"
	assertStatements(t, statement+"; SELECT 2;", DialectPostgres, []string{statement, "SELECT 2"})
	assertStatements(t, "/* outer /* inner */ ; ignored */", DialectPostgres, []string{})
}

// -----------------------------------------------------------------------------
// TestSplitCopyPayloadIsNotParsedAsSQL
// -----------------------------------------------------------------------------
func TestSplitCopyPayloadIsNotParsedAsSQL(t *testing.T) {
	statements := SplitStatements("COPY events FROM STDIN;\r\nhello;world\r\n/* data */\r\n\\.\r\nSELECT 2;", DialectPostgres)
	if len(statements) != 2 {
		t.Fatalf("got %d statements", len(statements))
	}
	if statements[0].CopyData == nil || *statements[0].CopyData != "hello;world\r\n/* data */\r\n" {
		t.Fatalf("lost COPY data: %+v", statements[0])
	}
	if statements[1].SQL != "SELECT 2" {
		t.Errorf("lost statement after COPY: %+v", statements[1])
	}
	for _, script := range []string{"COPY events FROM STDIN;\nmissing terminator\n", "COPY events FROM STDIN"} {
		statements = SplitStatements(script, DialectPostgres)
		if statements[0].ParseError == "" {
			t.Errorf("malformed COPY was accepted: %q", script)
		}
	}
}

// -----------------------------------------------------------------------------
// TestSplitKeepsSQLAfterNonnestingDialectComments
// -----------------------------------------------------------------------------
func TestSplitKeepsSQLAfterNonnestingDialectComments(t *testing.T) {
	for _, dialect := range []string{DialectSqlite, DialectMysql} {
		assertStatements(t, "/* outer /* text */ SELECT 1;", dialect, []string{"/* outer /* text */ SELECT 1"})
	}
}
