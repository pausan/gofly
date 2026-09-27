// Copyright (C) 2026 Pau Sanchez
//
// Plan transaction boundaries before executing any part of a migration.
package lib

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// -----------------------------------------------------------------------------
// prepareMigration
//
// Keep the parsed statements so classification and execution use identical SQL.
// A script override takes precedence over the automatic detection.
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
	}
	for _, unit := range detectionUnits(m.statements, g.Connection.Dialect().Name()) {
		inTransaction, err := g.canExecuteInTransaction(unit)
		if err != nil {
			return false, err
		}
		if inTransaction {
			transactional = true
		} else {
			nontransactional = true
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
// detectionUnits
//
// What Flyway calls a statement when it decides on a transaction: each
// statement, except on SQL Server, where it is the whole GO batch. A batch
// holding one nontransactional statement then runs entirely outside a
// transaction, as it does with Flyway, instead of being refused as mixed.
// -----------------------------------------------------------------------------
func detectionUnits(statements []Statement, dialect string) []string {
	units := []string{}
	for index, statement := range statements {
		if dialect == DialectMssql && index > 0 && statements[index-1].Batch == statement.Batch {
			units[len(units)-1] += ";\n" + statement.SQL
			continue
		}
		units = append(units, statement.SQL)
	}
	return units
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

// Flyway's PostgreSQLParser rules. Each is matched against the leading keywords
// one at a time, the first one, then the first two and so on, which is how
// Flyway's parser feeds them in.
var postgresNontransactionalRules = []*regexp.Regexp{
	regexp.MustCompile(`^(CREATE|DROP) (DATABASE|TABLESPACE|SUBSCRIPTION)$`),
	regexp.MustCompile(`^ALTER SYSTEM$`),
	regexp.MustCompile(`^(CREATE|DROP)( UNIQUE)? INDEX CONCURRENTLY$`),
	regexp.MustCompile(`^REINDEX( VERBOSE)? (SCHEMA|DATABASE|SYSTEM)$`),
	regexp.MustCompile(`^VACUUM$`),
	regexp.MustCompile(`^DISCARD ALL$`),

	// not in Flyway, which sends this inside a transaction for PostgreSQL to
	// refuse. Running it where it can succeed changes no history Flyway could
	// have written.
	regexp.MustCompile(`^REINDEX( VERBOSE)? (INDEX|TABLE) CONCURRENTLY$`),
}

// ALTER TYPE ... ADD VALUE only became transactional in PostgreSQL 12
var postgresAddEnumValue = regexp.MustCompile(`^ALTER TYPE( .*)? ADD VALUE$`)

// SQLServerParser.SPROCS_INVALID_IN_TRANSACTIONS
var mssqlProceduresOutsideTransactions = map[string]bool{
	"SP_ADDSUBSCRIPTION": true, "SP_DROPSUBSCRIPTION": true,
	"SP_ADDDISTRIBUTOR": true, "SP_DROPDISTRIBUTOR": true,
	"SP_ADDDISTPUBLISHER": true, "SP_DROPDISTPUBLISHER": true,
	"SP_ADDLINKEDSERVER": true, "SP_DROPLINKEDSERVER": true,
	"SP_ADDLINKEDSRVLOGIN": true, "SP_DROPLINKEDSRVLOGIN": true,
	"SP_SERVEROPTION": true, "SP_REPLICATIONDBOPTION": true,
	"SP_FULLTEXT_DATABASE": true,
}

// -----------------------------------------------------------------------------
// canExecuteInTransaction
//
// Mirrors each Flyway parser's detectCanExecuteInTransaction. MySQL has no
// rules: it cannot roll DDL back anyway.
// -----------------------------------------------------------------------------
func (g *Gofly) canExecuteInTransaction(statement string) (bool, error) {
	dialect := g.Connection.Dialect().Name()
	words := flywayKeywords(statement, dialect)
	if nontransactionalKeywords(dialect, words) {
		return false, nil
	}
	if dialect != DialectPostgres || !matchesAnyPrefix(postgresAddEnumValue, words) {
		return true, nil
	}

	if g.serverVersion == 0 {
		err := g.Connection.sessionDB().QueryRow(`SELECT current_setting('server_version_num')::int`).Scan(&g.serverVersion)
		if err != nil {
			return false, err
		}
	}
	return g.serverVersion >= 120000, nil
}

// -----------------------------------------------------------------------------
// nontransactionalKeywords
// -----------------------------------------------------------------------------
func nontransactionalKeywords(dialect string, words []string) bool {
	switch dialect {
	case DialectPostgres:
		for _, rule := range postgresNontransactionalRules {
			if matchesAnyPrefix(rule, words) {
				return true
			}
		}

	case DialectMssql:
		for i, current := range words {
			if current == "BACKUP" || current == "RESTORE" || current == "RECONFIGURE" {
				return true
			}
			if i == 0 {
				continue
			}
			previous := words[i-1]
			if previous == "EXEC" && mssqlProceduresOutsideTransactions[current] {
				return true
			}
			if (previous == "CREATE" || previous == "ALTER" || previous == "DROP") &&
				(current == "DATABASE" || current == "FULLTEXT") {
				return true
			}
		}

	case DialectSqlite:
		// PRAGMA foreign_keys is silently ignored inside a transaction
		return len(words) > 1 && words[0] == "PRAGMA" && words[1] == "FOREIGN_KEYS"
	}

	return false
}

// -----------------------------------------------------------------------------
// matchesAnyPrefix
// -----------------------------------------------------------------------------
func matchesAnyPrefix(rule *regexp.Regexp, words []string) bool {
	for i := 1; i <= len(words); i++ {
		if rule.MatchString(strings.Join(words[:i], " ")) {
			return true
		}
	}
	return false
}
