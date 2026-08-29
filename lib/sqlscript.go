// Copyright (C) 2026 Pau Sanchez
//
// Splitting of a migration into the individual statements to send to the
// database. Every driver we support requires one statement per round trip, so
// the script has to be cut on the statement boundaries while respecting string
// literals, comments and the quoting rules of each dialect.
package lib

import (
	"strings"
)

// Statement is a single executable statement together with the line it starts
// at, so that errors can be reported the way Flyway does.
type Statement struct {
	SQL  string
	Line int
}

// scanFlavour captures the few lexical differences between the databases we
// support. Everything else is common SQL.
type scanFlavour struct {
	// identifierQuote is the character used to quote identifiers besides `"`
	identifierQuote rune

	// dollarQuoted enables PostgreSQL's $tag$ ... $tag$ string literals
	dollarQuoted bool

	// batchSeparator is SQL Server's standalone GO
	batchSeparator string

	// supportsDelimiter enables MySQL's DELIMITER directive
	supportsDelimiter bool

	// backslashEscapes enables MySQL style \' escaping inside string literals
	backslashEscapes bool
}

// -----------------------------------------------------------------------------
// SplitStatements
//
// Cuts a migration script into statements for the given dialect. Empty
// statements and comment-only fragments are dropped.
// -----------------------------------------------------------------------------
func SplitStatements(sql string, dialect string) []Statement {
	flavour := flavourFor(dialect)

	statements := []Statement{}
	current := strings.Builder{}

	line := 1
	statementLine := 1
	delimiter := ";"

	runes := []rune(sql)
	index := 0

	// flush appends whatever has been collected so far as a new statement
	flush := func() {
		text := strings.TrimSpace(current.String())
		current.Reset()
		if text != "" && !isCommentOnly(text) {
			statements = append(statements, Statement{SQL: text, Line: statementLine})
		}
		statementLine = line
	}

	for index < len(runes) {
		char := runes[index]

		// ---- line comment -------------------------------------------------
		if char == '-' && index+1 < len(runes) && runes[index+1] == '-' {
			for index < len(runes) && runes[index] != '\n' {
				current.WriteRune(runes[index])
				index++
			}
			continue
		}

		// ---- block comment ------------------------------------------------
		if char == '/' && index+1 < len(runes) && runes[index+1] == '*' {
			current.WriteString("/*")
			index += 2
			for index < len(runes) {
				if runes[index] == '\n' {
					line++
				}
				if runes[index] == '*' && index+1 < len(runes) && runes[index+1] == '/' {
					current.WriteString("*/")
					index += 2
					break
				}
				current.WriteRune(runes[index])
				index++
			}
			continue
		}

		// ---- string literal -----------------------------------------------
		if char == '\'' {
			consumed, text := readQuoted(runes, index, '\'', flavour.backslashEscapes)
			current.WriteString(text)
			line += strings.Count(text, "\n")
			index = consumed
			continue
		}

		// ---- quoted identifiers -------------------------------------------
		if char == '"' || (flavour.identifierQuote != 0 && char == flavour.identifierQuote) {
			consumed, text := readQuoted(runes, index, char, false)
			current.WriteString(text)
			line += strings.Count(text, "\n")
			index = consumed
			continue
		}

		// ---- PostgreSQL dollar quoted string ------------------------------
		if flavour.dollarQuoted && char == '$' {
			consumed, text, ok := readDollarQuoted(runes, index)
			if ok {
				current.WriteString(text)
				line += strings.Count(text, "\n")
				index = consumed
				continue
			}
		}

		// ---- MySQL DELIMITER directive ------------------------------------
		if flavour.supportsDelimiter && atLineStart(current.String()) && matchesKeyword(runes, index, "DELIMITER") {
			newDelimiter, consumed := readDelimiter(runes, index)
			if newDelimiter != "" {
				flush()
				delimiter = newDelimiter
				index = consumed
				continue
			}
		}

		// ---- SQL Server GO batch separator --------------------------------
		if flavour.batchSeparator != "" && atLineStart(current.String()) && isStandaloneGo(runes, index) {
			flush()
			for index < len(runes) && runes[index] != '\n' {
				index++
			}
			continue
		}

		// ---- statement delimiter ------------------------------------------
		if matchesLiteral(runes, index, delimiter) {
			index += len([]rune(delimiter))
			flush()
			continue
		}

		if char == '\n' {
			line++
		}
		current.WriteRune(char)
		index++
	}

	flush()

	return statements
}

// -----------------------------------------------------------------------------
// flavourFor
// -----------------------------------------------------------------------------
func flavourFor(dialect string) scanFlavour {
	switch strings.ToLower(dialect) {
	case DialectPostgres:
		return scanFlavour{dollarQuoted: true}

	case DialectMysql:
		return scanFlavour{
			identifierQuote:   '`',
			supportsDelimiter: true,
			backslashEscapes:  true,
		}

	case DialectMssql:
		return scanFlavour{batchSeparator: "GO"}

	default:
		return scanFlavour{}
	}
}

// -----------------------------------------------------------------------------
// readQuoted
//
// Reads a quoted run starting at index, returning the index just past it and
// the text including both quotes. A doubled quote is an escaped quote in every
// dialect we support.
// -----------------------------------------------------------------------------
func readQuoted(runes []rune, index int, quote rune, backslashEscapes bool) (int, string) {
	text := strings.Builder{}
	text.WriteRune(runes[index])
	index++

	for index < len(runes) {
		char := runes[index]

		if backslashEscapes && char == '\\' && index+1 < len(runes) {
			text.WriteRune(char)
			text.WriteRune(runes[index+1])
			index += 2
			continue
		}

		if char == quote {
			// a doubled quote stands for a literal quote
			if index+1 < len(runes) && runes[index+1] == quote {
				text.WriteRune(char)
				text.WriteRune(char)
				index += 2
				continue
			}
			text.WriteRune(char)
			return index + 1, text.String()
		}

		text.WriteRune(char)
		index++
	}

	return index, text.String()
}

// -----------------------------------------------------------------------------
// readDollarQuoted
//
// Reads a PostgreSQL $tag$ ... $tag$ literal. Returns ok == false when what
// follows the dollar sign is not actually an opening tag.
// -----------------------------------------------------------------------------
func readDollarQuoted(runes []rune, index int) (int, string, bool) {
	start := index
	cursor := index + 1

	for cursor < len(runes) && runes[cursor] != '$' {
		if !isTagRune(runes[cursor]) {
			return index, "", false
		}
		cursor++
	}
	if cursor >= len(runes) {
		return index, "", false
	}

	tag := string(runes[start : cursor+1])
	cursor++

	closing := strings.Index(string(runes[cursor:]), tag)
	if closing < 0 {
		// unterminated, swallow the rest so we do not split in the middle
		return len(runes), string(runes[start:]), true
	}

	body := []rune(string(runes[cursor:])[:closing])
	end := cursor + len(body) + len([]rune(tag))

	return end, tag + string(body) + tag, true
}

// -----------------------------------------------------------------------------
// isTagRune
// -----------------------------------------------------------------------------
func isTagRune(char rune) bool {
	return char == '_' ||
		(char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9')
}

// -----------------------------------------------------------------------------
// matchesLiteral
// -----------------------------------------------------------------------------
func matchesLiteral(runes []rune, index int, literal string) bool {
	target := []rune(literal)
	if len(target) == 0 || index+len(target) > len(runes) {
		return false
	}

	for i, char := range target {
		if runes[index+i] != char {
			return false
		}
	}

	return true
}

// -----------------------------------------------------------------------------
// matchesKeyword
//
// Case insensitive keyword match that also requires a word boundary after it.
// -----------------------------------------------------------------------------
func matchesKeyword(runes []rune, index int, keyword string) bool {
	target := []rune(strings.ToUpper(keyword))
	if index+len(target) > len(runes) {
		return false
	}

	for i, char := range target {
		if toUpperRune(runes[index+i]) != char {
			return false
		}
	}

	next := index + len(target)
	if next < len(runes) && isTagRune(runes[next]) {
		return false
	}

	return true
}

// -----------------------------------------------------------------------------
// toUpperRune
// -----------------------------------------------------------------------------
func toUpperRune(char rune) rune {
	if char >= 'a' && char <= 'z' {
		return char - 'a' + 'A'
	}
	return char
}

// -----------------------------------------------------------------------------
// atLineStart
//
// Reports whether nothing but whitespace has been collected since the last
// newline, which is where MySQL's DELIMITER and SQL Server's GO must appear.
// -----------------------------------------------------------------------------
func atLineStart(collected string) bool {
	newline := strings.LastIndex(collected, "\n")
	return strings.TrimSpace(collected[newline+1:]) == ""
}

// -----------------------------------------------------------------------------
// readDelimiter
//
// Parses a `DELIMITER //` directive, returning the new delimiter and the index
// just past the directive line.
// -----------------------------------------------------------------------------
func readDelimiter(runes []rune, index int) (string, int) {
	cursor := index + len("DELIMITER")

	for cursor < len(runes) && (runes[cursor] == ' ' || runes[cursor] == '\t') {
		cursor++
	}

	start := cursor
	for cursor < len(runes) && runes[cursor] != '\n' && runes[cursor] != '\r' {
		cursor++
	}

	delimiter := strings.TrimSpace(string(runes[start:cursor]))
	if delimiter == "" {
		return "", index
	}

	return delimiter, cursor
}

// -----------------------------------------------------------------------------
// isStandaloneGo
//
// Reports whether a SQL Server GO batch separator starts at index, that is a GO
// alone on its line.
// -----------------------------------------------------------------------------
func isStandaloneGo(runes []rune, index int) bool {
	if !matchesKeyword(runes, index, "GO") {
		return false
	}

	cursor := index + 2
	for cursor < len(runes) && (runes[cursor] == ' ' || runes[cursor] == '\t' || runes[cursor] == '\r') {
		cursor++
	}

	return cursor >= len(runes) || runes[cursor] == '\n'
}

// -----------------------------------------------------------------------------
// isCommentOnly
//
// Reports whether a fragment carries no executable SQL, so that a trailing
// comment after the last semicolon is not sent to the database.
// -----------------------------------------------------------------------------
func isCommentOnly(text string) bool {
	stripped := strings.Builder{}

	runes := []rune(text)
	for index := 0; index < len(runes); {
		if runes[index] == '-' && index+1 < len(runes) && runes[index+1] == '-' {
			for index < len(runes) && runes[index] != '\n' {
				index++
			}
			continue
		}

		if runes[index] == '/' && index+1 < len(runes) && runes[index+1] == '*' {
			index += 2
			for index < len(runes) {
				if runes[index] == '*' && index+1 < len(runes) && runes[index+1] == '/' {
					index += 2
					break
				}
				index++
			}
			continue
		}

		stripped.WriteRune(runes[index])
		index++
	}

	return strings.TrimSpace(stripped.String()) == ""
}
