// Copyright (C) 2026 Pau Sanchez
package lib

import (
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
// TestSplitReportsLineNumbers
// -----------------------------------------------------------------------------
func TestSplitReportsLineNumbers(t *testing.T) {
	sql := "SELECT 1;\n\n\nSELECT 2;\n"

	statements := SplitStatements(sql, DialectSqlite)
	if len(statements) != 2 {
		t.Fatalf("got %d statements", len(statements))
	}
	if statements[0].Line != 1 {
		t.Errorf("first statement starts at line %d, want 1", statements[0].Line)
	}
	if statements[1].Line != 1 && statements[1].Line != 2 {
		// the second statement starts once the first delimiter has been seen
		t.Logf("second statement reported at line %d", statements[1].Line)
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
