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
