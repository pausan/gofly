// Copyright (C) 2026 Pau Sanchez
//
// Splitting of a migration into the individual statements to send to the
// database. Every driver we support requires one statement per round trip, so
// the script has to be cut on the statement boundaries while respecting string
// literals, comments and the quoting rules of each dialect.
package lib

import (
	"strings"
	"unicode"
)

// Statement is a single executable statement together with the line it starts
// at, so that errors can be reported the way Flyway does.
type Statement struct {
	SQL        string
	Line       int
	CopyData   *string
	ParseError string

	// Batch counts the SQL Server GO separators before the statement. Flyway
	// parses a whole batch as one statement, which matters for transactions.
	Batch int
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

	// mysqlComments enables # line comments and versioned executable comments
	mysqlComments bool
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
	batch := 0

	runes := []rune(sql)
	index := 0

	// flush appends whatever has been collected so far as a new statement.
	// statementLine is where the collected text begins, usually the line of
	// the previous delimiter, but Flyway reports the line of the first token
	// that is not a comment, so the leading blank lines and comments are
	// counted in.
	flush := func() {
		text := current.String()
		current.Reset()
		skipped, found := leadingComments(text, flavour)
		if found {
			statements = append(statements, Statement{SQL: strings.TrimSpace(text), Line: statementLine + skipped, Batch: batch})
		}
		statementLine = line
	}

	for index < len(runes) {
		char := runes[index]

		// ---- line comment -------------------------------------------------
		if (char == '-' && index+1 < len(runes) && runes[index+1] == '-') ||
			(flavour.mysqlComments && char == '#' && !matchesLiteral(runes, index, delimiter)) {
			for index < len(runes) && runes[index] != '\n' {
				current.WriteRune(runes[index])
				index++
			}
			continue
		}

		// ---- block comment ------------------------------------------------
		if char == '/' && index+1 < len(runes) && runes[index+1] == '*' {
			end := readBlockComment(runes, index, flavour.dollarQuoted || flavour.batchSeparator != "")
			text := string(runes[index:end])
			current.WriteString(text)
			line += strings.Count(text, "\n")
			index = end
			continue
		}

		// ---- string literal -----------------------------------------------
		if char == '\'' {
			escaped := flavour.backslashEscapes || (flavour.dollarQuoted && postgresEscapePrefix(runes, index))
			consumed, text := readQuoted(runes, index, '\'', escaped)
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
			batch++
			for index < len(runes) && runes[index] != '\n' {
				index++
			}
			continue
		}

		// ---- statement delimiter ------------------------------------------
		if matchesLiteral(runes, index, delimiter) {
			copyInput := flavour.dollarQuoted && postgresCopyStdin(current.String())
			index += len([]rune(delimiter))
			flush()
			if copyInput {
				start := index
				end, data, message := readCopyPayload(runes, index)
				index = end
				line += strings.Count(string(runes[start:end]), "\n")
				statements[len(statements)-1].CopyData = &data
				statements[len(statements)-1].ParseError = message
				statementLine = line
			}
			continue
		}

		if char == '\n' {
			line++
		}
		current.WriteRune(char)
		index++
	}

	flush()
	if flavour.dollarQuoted {
		for index := range statements {
			statement := &statements[index]
			if statement.CopyData == nil && postgresCopyStdin(statement.SQL) {
				statement.ParseError = "COPY FROM STDIN requires a semicolon and terminated input data"
			}
		}
	}
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
			mysqlComments:     true,
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
// leadingComments
//
// Skips the whitespace and comments a fragment starts with, returning how many
// lines they span and whether any executable SQL follows. A fragment with none
// is dropped, so that a trailing comment after the last semicolon is not sent
// to the database.
// -----------------------------------------------------------------------------
func leadingComments(text string, flavour scanFlavour) (int, bool) {
	runes := []rune(text)
	index := 0
	for index < len(runes) {
		switch {
		case (runes[index] == '-' && index+1 < len(runes) && runes[index+1] == '-') ||
			(flavour.mysqlComments && runes[index] == '#'):
			for index < len(runes) && runes[index] != '\n' {
				index++
			}

		case runes[index] == '/' && index+1 < len(runes) && runes[index+1] == '*':
			if flavour.mysqlComments && mysqlExecutableComment(runes, index) {
				return strings.Count(string(runes[:index]), "\n"), true
			}
			index = readBlockComment(runes, index, flavour.dollarQuoted || flavour.batchSeparator != "")

		case unicode.IsSpace(runes[index]):
			index++

		default:
			return strings.Count(string(runes[:index]), "\n"), true
		}
	}

	return 0, false
}

// -----------------------------------------------------------------------------
// mysqlExecutableComment
//
// Flyway's MySQLParser treats /*! followed by five version digits as SQL, even
// when it is the whole statement. Preserve the wrapper: the server evaluates
// the version guard, and delimiters inside it must not split the statement.
// -----------------------------------------------------------------------------
func mysqlExecutableComment(runes []rune, index int) bool {
	if index+8 > len(runes) || !matchesLiteral(runes, index, "/*!") {
		return false
	}
	for _, char := range runes[index+3 : index+8] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// postgresEscapePrefix
//
// E/e must be a separate token. Ordinary strings retain standard_conforming_strings
// behavior: a backslash is data, not an escape for the closing quote.
// -----------------------------------------------------------------------------
func postgresEscapePrefix(runes []rune, quote int) bool {
	if quote == 0 || (runes[quote-1] != 'E' && runes[quote-1] != 'e') {
		return false
	}
	if quote == 1 {
		return true
	}
	previous := runes[quote-2]
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous) && previous != '_' && previous != '$'
}

// -----------------------------------------------------------------------------
// readBlockComment
// -----------------------------------------------------------------------------
func readBlockComment(runes []rune, start int, nested bool) int {
	depth := 1
	for index := start + 2; index < len(runes); {
		if index+1 < len(runes) {
			if nested && runes[index] == '/' && runes[index+1] == '*' {
				depth++
				index += 2
				continue
			}
			if runes[index] == '*' && runes[index+1] == '/' {
				depth--
				index += 2
				if depth == 0 {
					return index
				}
				continue
			}
		}
		index++
	}
	return len(runes)
}

// -----------------------------------------------------------------------------
// readCopyPayload
//
// COPY data is not SQL: semicolons, comments and quote characters are data until
// the standalone backslash-dot terminator. Missing terminators fail locally.
// -----------------------------------------------------------------------------
func readCopyPayload(runes []rune, index int) (int, string, string) {
	for index < len(runes) && (runes[index] == ' ' || runes[index] == '\t' || runes[index] == '\r') {
		index++
	}
	if index >= len(runes) || runes[index] != '\n' {
		return len(runes), "", "COPY FROM STDIN requires data on the following line and a \\. terminator"
	}
	index++
	start := index
	for index < len(runes) {
		lineStart := index
		for index < len(runes) && runes[index] != '\n' {
			index++
		}
		line := strings.TrimSuffix(string(runes[lineStart:index]), "\r")
		if index < len(runes) {
			index++
		}
		if line == `\.` {
			return index, string(runes[start:lineStart]), ""
		}
	}
	return index, "", "COPY FROM STDIN is missing its \\. terminator"
}
