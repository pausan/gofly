// Copyright (C) 2026 Pau Sanchez
//
// Serialize mutating PostgreSQL commands before inspecting migration history.
package lib

import (
	"errors"
	"sort"
	"unicode/utf16"
)

// -----------------------------------------------------------------------------
// postgresHistoryLockKey
//
// Flyway's advisory-lock discriminator is Java String.hashCode over the quoted
// schema-qualified history name. Java hashes UTF-16 code units, not UTF-8 bytes.
// -----------------------------------------------------------------------------
func postgresHistoryLockKey(qualified string) int64 {
	var hash int32
	for _, unit := range utf16.Encode([]rune(qualified)) {
		hash = 31*hash + int32(unit)
	}
	return 0x466c79776179 + int64(hash)
}

// -----------------------------------------------------------------------------
// lockOperation
// -----------------------------------------------------------------------------
func (g *Gofly) lockOperation() (func() error, error) {
	g.operationMu.Lock()
	unlockLocal := func() error { g.operationMu.Unlock(); return nil }
	if g.Connection.Dialect().Name() != DialectPostgres {
		return unlockLocal, nil
	}
	names := []string{g.History.QualifiedName()}
	if g.Config.ImportFromFlyway && g.Config.FlywayTable != "" {
		names = append(names, g.Connection.Dialect().QuoteIdentifier(g.defaultSchema, g.Config.FlywayTable))
	}
	keys := []int64{}
	for _, name := range names {
		key := postgresHistoryLockKey(name)
		if len(keys) == 0 || key != keys[0] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	acquired := []int64{}
	release := func() error {
		var result error
		for i := len(acquired) - 1; i >= 0; i-- {
			_, err := g.Connection.sessionDB().Exec("SELECT pg_advisory_unlock($1)", acquired[i])
			result = errors.Join(result, err)
		}
		g.operationMu.Unlock()
		return result
	}
	for _, key := range keys {
		if _, err := g.Connection.sessionDB().Exec("SELECT pg_advisory_lock($1)", key); err != nil {
			return nil, errors.Join(err, release())
		}
		acquired = append(acquired, key)
	}
	return release, nil
}

// -----------------------------------------------------------------------------
// Migrate
// -----------------------------------------------------------------------------
func (g *Gofly) Migrate() (result *MigrateResult, err error) {
	release, err := g.lockOperation()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, release()) }()
	return g.migrate()
}

// -----------------------------------------------------------------------------
// Undo
// -----------------------------------------------------------------------------
func (g *Gofly) Undo() (result *UndoResult, err error) {
	release, err := g.lockOperation()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, release()) }()
	return g.undo()
}

// -----------------------------------------------------------------------------
// Repair
// -----------------------------------------------------------------------------
func (g *Gofly) Repair() (result *RepairResult, err error) {
	release, err := g.lockOperation()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, release()) }()
	return g.repair()
}

// -----------------------------------------------------------------------------
// Baseline
// -----------------------------------------------------------------------------
func (g *Gofly) Baseline() (err error) {
	release, err := g.lockOperation()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	return g.baseline()
}

// -----------------------------------------------------------------------------
// EnsureHistory
// -----------------------------------------------------------------------------
func (g *Gofly) EnsureHistory() (err error) {
	release, err := g.lockOperation()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	return g.ensureHistory()
}
