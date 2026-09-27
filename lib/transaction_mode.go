// Copyright (C) 2026 Pau Sanchez
//
// Plan transaction boundaries before executing any part of a migration.
package lib

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// -----------------------------------------------------------------------------
// prepareMigration
//
// Keep the parsed statements so classification and execution use identical SQL.
// A script override takes precedence over PostgreSQL's automatic detection.
// -----------------------------------------------------------------------------
func (g *Gofly) prepareMigration(m *ResolvedMigration) (bool, error) {
	text, err := m.LoadSQL(g.Config.NewPlaceholderReplacer())
	if err != nil {
		return false, err
	}
	m.statements = SplitStatements(text, g.Connection.Dialect().Name())
	transactional, nontransactional := false, false
	for _, s := range m.statements {
		if s.ParseError != "" {
			return false, fmt.Errorf("%s: %s", m.Script, s.ParseError)
		}
		if g.Connection.Dialect().Name() == DialectPostgres && postgresNontransactional(s.SQL) {
			nontransactional = true
		} else {
			transactional = true
		}
	}
	override, err := scriptTransactionOverride(m.PhysicalLocation + ".conf")
	if err != nil {
		return false, err
	}
	if override != nil {
		return *override, nil
	}
	if transactional && nontransactional && !g.Config.Mixed {
		return false, fmt.Errorf("%s mixes transactional and non-transactional statements; set mixed=true or executeInTransaction=false for this script", m.Script)
	}
	return !nontransactional, nil
}

// -----------------------------------------------------------------------------
// prepareGroup
// -----------------------------------------------------------------------------
func (g *Gofly) prepareGroup(migrations []*ResolvedMigration) (bool, error) {
	transactional, nontransactional := false, false
	for _, m := range migrations {
		mode, err := g.prepareMigration(m)
		if err != nil {
			return false, err
		}
		if mode {
			transactional = true
		} else {
			nontransactional = true
		}
	}
	if transactional && nontransactional && !g.Config.Mixed {
		return false, fmt.Errorf("group contains transactional and non-transactional migrations; set mixed=true to execute without a transaction")
	}
	return !nontransactional, nil
}

// -----------------------------------------------------------------------------
// scriptTransactionOverride
// -----------------------------------------------------------------------------
func scriptTransactionOverride(path string) (*bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var result *bool
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "executeInTransaction" {
			return nil, fmt.Errorf("unsupported script configuration in %s: %s", path, line)
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		result = &parsed
	}
	return result, scanner.Err()
}

// -----------------------------------------------------------------------------
// postgresNontransactional
//
// Match statement keywords, never literals, comments or quoted identifiers.
// ALTER TYPE ADD VALUE is transactional on PostgreSQL 12 and newer.
// -----------------------------------------------------------------------------
func postgresNontransactional(statement string) bool {
	words := postgresKeywords(statement)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "VACUUM", "DISCARD":
		return true
	case "CREATE", "DROP":
		if len(words) > 1 && (words[1] == "DATABASE" || words[1] == "TABLESPACE") {
			return true
		}
		i := 1
		if i < len(words) && words[i] == "UNIQUE" {
			i++
		}
		return i+1 < len(words) && words[i] == "INDEX" && words[i+1] == "CONCURRENTLY"
	case "REINDEX":
		for _, word := range words[1:] {
			if word == "CONCURRENTLY" || word == "DATABASE" || word == "SYSTEM" || word == "SCHEMA" {
				return true
			}
		}
	case "ALTER":
		return len(words) > 1 && words[1] == "SYSTEM"
	}
	return false
}
