// Copyright (C) 2026 Pau Sanchez
//
// PostgreSQL statement classification without matching keywords inside literals.
package lib

import (
	"strings"
	"unicode"
)

// -----------------------------------------------------------------------------
// postgresKeywords
//
// Quoted contents are deliberately omitted. Parentheses remain so callers can
// distinguish top-level COPY FROM from a SELECT nested inside COPY (...).
// -----------------------------------------------------------------------------
func postgresKeywords(statement string) []string {
	runes := []rune(statement)
	var words []string
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i = readBlockComment(runes, i, true)
		case c == '\'' || c == '"':
			i, _ = readQuoted(runes, i, c, c == '\'' && postgresEscapePrefix(runes, i))
		case c == '$':
			end, _, ok := readDollarQuoted(runes, i)
			if ok {
				i = end
			} else {
				i++
			}
		case unicode.IsLetter(c) || c == '_':
			start := i
			i++
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_' || runes[i] == '$') {
				i++
			}
			words = append(words, strings.ToUpper(string(runes[start:i])))
		default:
			if c == '(' || c == ')' {
				words = append(words, string(c))
			}
			i++
		}
	}
	return words
}

// -----------------------------------------------------------------------------
// postgresCopyStdin
// -----------------------------------------------------------------------------
func postgresCopyStdin(statement string) bool {
	words := postgresKeywords(statement)
	if len(words) == 0 || words[0] != "COPY" {
		return false
	}
	depth := 0
	for i, word := range words {
		if word == "(" {
			depth++
		}
		if word == ")" {
			depth--
		}
		if depth == 0 && word == "FROM" && i+1 < len(words) && words[i+1] == "STDIN" {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// flywayKeywords
//
// The leading keywords of a statement, collected the way Flyway's Parser does
// for transaction detection: unquoted words made only of letters and
// underscores, outside parentheses, and no more than ten of them. Flyway treats
// a word holding a digit or a dot as an identifier, so it is skipped here too.
// Literals, quoted identifiers and comments never contribute.
// -----------------------------------------------------------------------------
func flywayKeywords(statement string, dialect string) []string {
	const cutoff = 10

	flavour := flavourFor(dialect)
	nested := flavour.dollarQuoted || flavour.batchSeparator != ""
	runes := []rune(statement)
	words := []string{}
	depth := 0

	for i := 0; i < len(runes) && len(words) < cutoff; {
		c := runes[i]
		switch {
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i = readBlockComment(runes, i, nested)
		case c == '\'':
			escaped := flavour.backslashEscapes || (flavour.dollarQuoted && postgresEscapePrefix(runes, i))
			i, _ = readQuoted(runes, i, '\'', escaped)
		case c == '"' || (flavour.identifierQuote != 0 && c == flavour.identifierQuote):
			i, _ = readQuoted(runes, i, c, false)
		case c == '[' && flavour.batchSeparator != "":
			i, _ = readQuoted(runes, i, ']', false)
		case c == '$' && flavour.dollarQuoted:
			end, _, ok := readDollarQuoted(runes, i)
			if ok {
				i = end
			} else {
				i++
			}
		case c == '(':
			depth++
			i++
		case c == ')':
			depth--
			i++
		case unicode.IsLetter(c) || c == '_':
			start := i
			keyword := true
			for i < len(runes) && (isWordRune(runes[i]) || runes[i] == '.') {
				if !unicode.IsLetter(runes[i]) && runes[i] != '_' {
					keyword = false
				}
				i++
			}

			// N'', E'' and X'' prefix a literal rather than being a keyword
			if i < len(runes) && runes[i] == '\'' {
				continue
			}
			if keyword && depth == 0 {
				words = append(words, strings.ToUpper(string(runes[start:i])))
			}
		default:
			i++
		}
	}

	return words
}

// -----------------------------------------------------------------------------
// isWordRune
// -----------------------------------------------------------------------------
func isWordRune(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '$'
}
