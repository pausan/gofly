// Copyright (C) 2026 Pau Sanchez
//
// The commands themselves: info, validate, migrate, undo, baseline, repair and
// clean.
package lib

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Gofly ties together a configuration, a connection and its schema history
type Gofly struct {
	Config     *Config
	Connection *Connection
	History    *SchemaHistory
	Output     io.Writer

	// historySchema is where the gofly history table lives, resolved once
	historySchema string

	// defaultSchema is the schema the migrations themselves run against
	defaultSchema string
}

// -----------------------------------------------------------------------------
// New
//
// Opens the connection and works out where the history table belongs.
// -----------------------------------------------------------------------------
func New(config *Config) (*Gofly, error) {
	connection, err := Connect(config.URL, config.User, config.Password, config.ConnectRetries)
	if err != nil {
		return nil, err
	}

	gofly, err := NewWithConnection(config, connection)
	if err != nil {
		connection.Close()
		return nil, err
	}

	return gofly, nil
}

// -----------------------------------------------------------------------------
// NewWithConnection
//
// Same as New for an already open connection, which is what the tests use.
// -----------------------------------------------------------------------------
func NewWithConnection(config *Config, connection *Connection) (*Gofly, error) {
	dialect := connection.Dialect()

	// the history schema has to be known first: the schema the migrations run
	// against is whatever the connection defaults to once ours is left out
	historySchema := config.GoflySchema
	if historySchema == "" {
		historySchema = dialect.DefaultHistorySchema()
	}
	if !dialect.SupportsSchemas() {
		historySchema = ""
	}

	defaultSchema := config.DefaultSchema
	if defaultSchema == "" && len(config.Schemas) > 0 {
		defaultSchema = config.Schemas[0]
	}
	if defaultSchema == "" {
		resolved, err := dialect.DefaultSchema(connection.DB(), historySchema)
		if err != nil {
			return nil, fmt.Errorf("cannot determine the default schema: %w", err)
		}
		defaultSchema = resolved
	}

	// pin the session to it, so that a migration saying CREATE TABLE with no
	// schema always lands in the same place, run after run
	if err := dialect.SetSessionSchema(connection.DB(), defaultSchema); err != nil {
		return nil, err
	}

	gofly := &Gofly{
		Config:        config,
		Connection:    connection,
		Output:        os.Stdout,
		historySchema: historySchema,
		defaultSchema: defaultSchema,
	}
	gofly.History = NewSchemaHistory(connection, historySchema, config.Table, config.ResolveInstalledBy())

	return gofly, nil
}

// -----------------------------------------------------------------------------
// Close
// -----------------------------------------------------------------------------
func (g *Gofly) Close() error {
	return g.Connection.Close()
}

// -----------------------------------------------------------------------------
// EnsureHistory
//
// Creates the gofly history table when it is missing and, the very first time,
// imports whatever an earlier Flyway installation left behind so that the
// history and the checksums carry on where Flyway stopped.
// -----------------------------------------------------------------------------
func (g *Gofly) EnsureHistory() error {
	exists, err := g.History.Exists()
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	if err := g.History.Create(); err != nil {
		return err
	}
	g.logf("Created schema history table: %s", g.History.QualifiedName())

	if !g.Config.ImportFromFlyway {
		return nil
	}

	imported, err := g.History.ImportFromFlyway(g.defaultSchema, g.Config.FlywayTable)
	if err != nil {
		return err
	}
	if imported > 0 {
		g.logf("Imported %d row(s) from the existing %s table into %s",
			imported, g.Config.FlywayTable, g.History.QualifiedName())
	}

	return nil
}

// HistorySource says which table an info or validate run read from
type HistorySource int

const (
	// HistorySourceNone means neither gofly nor Flyway has a history here yet
	HistorySourceNone HistorySource = iota

	// HistorySourceGofly means gofly's own table was read
	HistorySourceGofly

	// HistorySourceFlyway means gofly has no table here yet and the existing
	// Flyway one was read instead, without touching it
	HistorySourceFlyway
)

// -----------------------------------------------------------------------------
// Info
//
// Returns the current picture of the database. This never creates or modifies
// anything: on a database gofly has not taken over yet it reads the existing
// Flyway history instead, so that info and validate can report on it without
// committing to the migration.
// -----------------------------------------------------------------------------
func (g *Gofly) Info() (*MigrationInfoService, error) {
	service, _, err := g.InfoWithSource()

	return service, err
}

// -----------------------------------------------------------------------------
// InfoWithSource
//
// Same as Info, and also says which history table the answer came from.
// -----------------------------------------------------------------------------
func (g *Gofly) InfoWithSource() (*MigrationInfoService, HistorySource, error) {
	resolved, err := ResolveMigrations(g.Config)
	if err != nil {
		return nil, HistorySourceNone, err
	}

	applied, source, err := g.readHistory()
	if err != nil {
		return nil, HistorySourceNone, err
	}

	service, err := BuildMigrationInfo(g.Config, resolved, applied)

	return service, source, err
}

// -----------------------------------------------------------------------------
// readHistory
//
// Reads the schema history without creating anything.
//
// Once gofly owns a database its own table is the only truth. Before that, an
// existing flyway_schema_history is read as is, so that `validate` on a
// database still managed by Flyway checks the migrations against the history
// that is actually there rather than reporting every one of them as pending.
// -----------------------------------------------------------------------------
func (g *Gofly) readHistory() ([]*AppliedMigration, HistorySource, error) {
	exists, err := g.History.Exists()
	if err != nil {
		return nil, HistorySourceNone, err
	}
	if exists {
		applied, err := g.History.All()
		return applied, HistorySourceGofly, err
	}

	flywayHistory, exists, err := g.flywayHistory()
	if err != nil {
		return nil, HistorySourceNone, err
	}
	if !exists {
		return []*AppliedMigration{}, HistorySourceNone, nil
	}

	applied, err := flywayHistory.All()

	return applied, HistorySourceFlyway, err
}

// -----------------------------------------------------------------------------
// flywayHistory
//
// Returns a read-only handle on the Flyway history table, if there is one.
// -----------------------------------------------------------------------------
func (g *Gofly) flywayHistory() (*SchemaHistory, bool, error) {
	if g.Config.FlywayTable == "" {
		return nil, false, nil
	}

	schema := g.defaultSchema
	if !g.Connection.Dialect().SupportsSchemas() {
		schema = ""
	}

	exists, err := g.Connection.Dialect().TableExists(g.Connection.DB(), schema, g.Config.FlywayTable)
	if err != nil || !exists {
		return nil, false, err
	}

	return NewSchemaHistory(g.Connection, schema, g.Config.FlywayTable, g.Config.ResolveInstalledBy()), true, nil
}

// -----------------------------------------------------------------------------
// Validate
//
// Runs the validation on its own, the way the `validate` command does. Nothing
// is created or migrated: on a database still managed by Flyway the files are
// checked against the existing flyway_schema_history.
// -----------------------------------------------------------------------------
func (g *Gofly) Validate() (*ValidateResult, error) {
	info, source, err := g.InfoWithSource()
	if err != nil {
		return nil, err
	}

	if source == HistorySourceFlyway {
		g.logf("%s does not exist yet, validating against the existing %s instead",
			g.History.QualifiedName(), g.Config.FlywayTable)
	}

	return info.Validate(ValidateContext{
		IgnoreMissing: g.Config.IgnoreMissing,
		IgnoreFuture:  g.Config.IgnoreFuture,
	}), nil
}

// MigrateResult reports what a migrate run did
type MigrateResult struct {
	InitialVersion     string
	TargetVersion      string
	MigrationsExecuted int
	Migrations         []*ResolvedMigration
}

// -----------------------------------------------------------------------------
// Migrate
//
// Applies every pending migration, in order. With -group the whole batch runs
// inside a single transaction, so either the database ends up fully migrated or
// completely untouched.
// -----------------------------------------------------------------------------
func (g *Gofly) Migrate() (*MigrateResult, error) {
	if err := g.EnsureHistory(); err != nil {
		return nil, err
	}

	if err := g.baselineOnMigrate(); err != nil {
		return nil, err
	}

	info, err := g.Info()
	if err != nil {
		return nil, err
	}

	if g.Config.ValidateOnMigrate {
		validation := info.Validate(ValidateContext{
			IgnorePending: true,
			IgnoreMissing: g.Config.IgnoreMissing,
			IgnoreFuture:  g.Config.IgnoreFuture,
		})
		if !validation.Valid() {
			return nil, validation.Error()
		}
	}

	pending := info.Pending()
	result := &MigrateResult{
		InitialVersion: info.Current.String(),
		TargetVersion:  info.Current.String(),
	}

	if len(pending) == 0 {
		g.logf("Schema %s is up to date. No migration necessary.", g.defaultSchema)
		return result, nil
	}

	g.logf("Current version of schema %s: %s", g.defaultSchema, info.Current)

	rank, err := g.History.NextInstalledRank()
	if err != nil {
		return nil, err
	}

	if g.Config.Group {
		if err := g.migrateGrouped(pending, rank, result); err != nil {
			return nil, err
		}
	} else {
		if err := g.migrateOneByOne(pending, rank, result); err != nil {
			return nil, err
		}
	}

	g.logf("Successfully applied %d migration(s) to schema %s (execution time %s)",
		result.MigrationsExecuted, g.defaultSchema, result.TargetVersion)

	return result, nil
}

// -----------------------------------------------------------------------------
// migrateGrouped
//
// Runs every pending migration inside one transaction. Nothing is recorded in
// the history until the whole batch has succeeded.
// -----------------------------------------------------------------------------
func (g *Gofly) migrateGrouped(pending []*MigrationInfo, rank int, result *MigrateResult) error {
	g.warnIfNoDDLTransactions()

	transaction, err := g.Connection.DB().Begin()
	if err != nil {
		return err
	}

	for _, info := range pending {
		migration := info.Resolved

		elapsed, err := g.executeMigration(transaction, migration)
		if err != nil {
			transaction.Rollback()
			return g.migrationError(migration, err)
		}

		applied := g.appliedRowFor(migration, rank, elapsed, true)
		if err := g.History.Insert(transaction, applied); err != nil {
			transaction.Rollback()
			return err
		}

		rank++
		result.MigrationsExecuted++
		result.Migrations = append(result.Migrations, migration)
		if migration.Version != nil {
			result.TargetVersion = migration.Version.String()
		}
	}

	return transaction.Commit()
}

// -----------------------------------------------------------------------------
// migrateOneByOne
//
// Runs each migration in its own transaction, which is Flyway's default. A
// failure is recorded in the history so that the next run refuses to continue
// until the mess has been repaired.
// -----------------------------------------------------------------------------
func (g *Gofly) migrateOneByOne(pending []*MigrationInfo, rank int, result *MigrateResult) error {
	for _, info := range pending {
		migration := info.Resolved

		transaction, err := g.Connection.DB().Begin()
		if err != nil {
			return err
		}

		elapsed, execErr := g.executeMigration(transaction, migration)
		if execErr != nil {
			transaction.Rollback()

			if err := g.recordFailure(migration, rank, elapsed); err != nil {
				return errors.Join(g.migrationError(migration, execErr), err)
			}

			return g.migrationError(migration, execErr)
		}

		applied := g.appliedRowFor(migration, rank, elapsed, true)
		if err := g.History.Insert(transaction, applied); err != nil {
			transaction.Rollback()
			return err
		}

		if err := transaction.Commit(); err != nil {
			return err
		}

		rank++
		result.MigrationsExecuted++
		result.Migrations = append(result.Migrations, migration)
		if migration.Version != nil {
			result.TargetVersion = migration.Version.String()
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// executeMigration
//
// Runs one migration script statement by statement, returning how long it took.
// -----------------------------------------------------------------------------
func (g *Gofly) executeMigration(executor sqlExecutor, migration *ResolvedMigration) (int, error) {
	label := migration.Script
	if migration.Version != nil {
		label = fmt.Sprintf("%s - %s", migration.Version, migration.Description)
	} else {
		label = fmt.Sprintf("(repeatable) %s", migration.Description)
	}
	g.logf("Migrating schema %s to version %s", g.defaultSchema, label)

	sql, err := migration.LoadSQL(g.Config.NewPlaceholderReplacer())
	if err != nil {
		return 0, err
	}

	started := time.Now()

	if g.Config.SkipExecutingMigrations {
		return 0, nil
	}

	for _, statement := range SplitStatements(sql, g.Connection.Dialect().Name()) {
		if _, err := executor.Exec(statement.SQL); err != nil {
			return int(time.Since(started).Milliseconds()),
				fmt.Errorf("line %d: %w", statement.Line, err)
		}
	}

	return int(time.Since(started).Milliseconds()), nil
}

// -----------------------------------------------------------------------------
// appliedRowFor
// -----------------------------------------------------------------------------
func (g *Gofly) appliedRowFor(migration *ResolvedMigration, rank int, elapsed int, success bool) *AppliedMigration {
	checksum := migration.Checksum

	return &AppliedMigration{
		InstalledRank: rank,
		Version:       migration.Version,
		Description:   migration.Description,
		Type:          migration.Type,
		Script:        migration.Script,
		Checksum:      &checksum,
		InstalledBy:   g.Config.ResolveInstalledBy(),
		ExecutionTime: elapsed,
		Success:       success,
	}
}

// -----------------------------------------------------------------------------
// recordFailure
//
// Writes the failed migration into the history, but only where the database
// could not roll the changes back on its own.
//
// This is what Flyway does (DbMigrate: the row is added in the `else` branch of
// `supportsDdlTransactions()`), and the reasoning is sound: when everything was
// rolled back there is nothing left to repair, and a leftover failed row would
// block the next run over changes that no longer exist. Where DDL commits
// implicitly, half the migration is still there and the row is the only record
// of it.
// -----------------------------------------------------------------------------
func (g *Gofly) recordFailure(migration *ResolvedMigration, rank int, elapsed int) error {
	if g.Connection.Dialect().SupportsDDLTransactions() {
		return nil
	}

	// the insert goes through the connection rather than the rolled back
	// transaction, which is the only way it can survive
	return g.History.Insert(g.Connection.DB(), g.appliedRowFor(migration, rank, elapsed, false))
}

// -----------------------------------------------------------------------------
// migrationError
// -----------------------------------------------------------------------------
func (g *Gofly) migrationError(migration *ResolvedMigration, err error) error {
	return fmt.Errorf("migration %s failed\n-> %s\n%w",
		migration.Script, migration.PhysicalLocation, err)
}

// -----------------------------------------------------------------------------
// warnIfNoDDLTransactions
// -----------------------------------------------------------------------------
func (g *Gofly) warnIfNoDDLTransactions() {
	if g.Connection.Dialect().SupportsDDLTransactions() {
		return
	}

	g.logf("WARNING: %s commits implicitly on DDL, so a failed migration cannot be rolled back completely",
		g.Connection.Dialect().Name())
}

// UndoResult reports what an undo run did
type UndoResult struct {
	MigrationsUndone int
	Migrations       []*ResolvedMigration
}

// -----------------------------------------------------------------------------
// Undo
//
// Undoes the applied versioned migrations, newest first, for as long as an undo
// script is available and the version stays above the target. With -group they
// all run inside a single transaction.
// -----------------------------------------------------------------------------
func (g *Gofly) Undo() (*UndoResult, error) {
	if err := g.EnsureHistory(); err != nil {
		return nil, err
	}

	resolved, err := ResolveMigrations(g.Config)
	if err != nil {
		return nil, err
	}

	applied, err := g.History.All()
	if err != nil {
		return nil, err
	}

	info, err := BuildMigrationInfo(g.Config, resolved, applied)
	if err != nil {
		return nil, err
	}

	if g.Config.ValidateOnMigrate {
		validation := info.Validate(ValidateContext{
			IgnorePending: true,
			IgnoreMissing: g.Config.IgnoreMissing,
			IgnoreFuture:  g.Config.IgnoreFuture,
		})
		if !validation.Valid() {
			return nil, validation.Error()
		}
	}

	target, err := g.Config.TargetVersion()
	if err != nil {
		return nil, err
	}

	candidates := info.Undoable(resolved)
	toUndo := []*MigrationInfo{}

	for _, candidate := range candidates {
		if target.Kind() == VersionKindReal && candidate.Version().Compare(target) < 0 {
			break
		}
		toUndo = append(toUndo, candidate)

		// without an explicit target only the most recent one is undone
		if target.Kind() != VersionKindReal {
			break
		}
	}

	result := &UndoResult{}
	if len(toUndo) == 0 {
		g.logf("No migrations to undo")
		return result, nil
	}

	rank, err := g.History.NextInstalledRank()
	if err != nil {
		return nil, err
	}

	if g.Config.Group {
		err = g.undoGrouped(resolved, toUndo, rank, result)
	} else {
		err = g.undoOneByOne(resolved, toUndo, rank, result)
	}
	if err != nil {
		return nil, err
	}

	g.logf("Successfully undid %d migration(s)", result.MigrationsUndone)

	return result, nil
}

// -----------------------------------------------------------------------------
// undoGrouped
// -----------------------------------------------------------------------------
func (g *Gofly) undoGrouped(resolved *ResolvedMigrations, toUndo []*MigrationInfo, rank int, result *UndoResult) error {
	g.warnIfNoDDLTransactions()

	transaction, err := g.Connection.DB().Begin()
	if err != nil {
		return err
	}

	for _, info := range toUndo {
		undo := resolved.UndoFor(info.Version())

		elapsed, err := g.executeMigration(transaction, undo)
		if err != nil {
			transaction.Rollback()
			return g.migrationError(undo, err)
		}

		if err := g.History.Insert(transaction, g.appliedRowFor(undo, rank, elapsed, true)); err != nil {
			transaction.Rollback()
			return err
		}

		rank++
		result.MigrationsUndone++
		result.Migrations = append(result.Migrations, undo)
	}

	return transaction.Commit()
}

// -----------------------------------------------------------------------------
// undoOneByOne
// -----------------------------------------------------------------------------
func (g *Gofly) undoOneByOne(resolved *ResolvedMigrations, toUndo []*MigrationInfo, rank int, result *UndoResult) error {
	for _, info := range toUndo {
		undo := resolved.UndoFor(info.Version())

		transaction, err := g.Connection.DB().Begin()
		if err != nil {
			return err
		}

		elapsed, execErr := g.executeMigration(transaction, undo)
		if execErr != nil {
			transaction.Rollback()

			if err := g.recordFailure(undo, rank, elapsed); err != nil {
				return errors.Join(g.migrationError(undo, execErr), err)
			}

			return g.migrationError(undo, execErr)
		}

		if err := g.History.Insert(transaction, g.appliedRowFor(undo, rank, elapsed, true)); err != nil {
			transaction.Rollback()
			return err
		}

		if err := transaction.Commit(); err != nil {
			return err
		}

		rank++
		result.MigrationsUndone++
		result.Migrations = append(result.Migrations, undo)
	}

	return nil
}

// -----------------------------------------------------------------------------
// Baseline
//
// Marks an existing database as migrated up to the baseline version, so that
// only the migrations above it are ever applied.
// -----------------------------------------------------------------------------
func (g *Gofly) Baseline() error {
	if err := g.EnsureHistory(); err != nil {
		return err
	}

	applied, err := g.History.All()
	if err != nil {
		return err
	}
	for _, migration := range applied {
		if migration.Type == MigrationTypeBaseline {
			g.logf("Schema history table %s already contains a baseline, skipping",
				g.History.QualifiedName())
			return nil
		}
	}
	if len(applied) > 0 {
		return fmt.Errorf(
			"unable to baseline: the schema history table %s already contains %d migration(s)",
			g.History.QualifiedName(), len(applied))
	}

	version, err := NewVersion(g.Config.BaselineVersion)
	if err != nil {
		return err
	}

	rank, err := g.History.NextInstalledRank()
	if err != nil {
		return err
	}

	row := &AppliedMigration{
		InstalledRank: rank,
		Version:       version,
		Description:   g.Config.BaselineDescr,
		Type:          MigrationTypeBaseline,
		Script:        g.Config.BaselineDescr,
		Checksum:      nil,
		InstalledBy:   g.Config.ResolveInstalledBy(),
		ExecutionTime: 0,
		Success:       true,
	}

	if err := g.History.Insert(g.Connection.DB(), row); err != nil {
		return err
	}

	g.logf("Successfully baselined schema %s with version %s", g.defaultSchema, version)

	return nil
}

// -----------------------------------------------------------------------------
// baselineOnMigrate
//
// Baselines automatically before migrating when the schema already holds
// objects and there is no history yet, which is Flyway's baselineOnMigrate.
// -----------------------------------------------------------------------------
func (g *Gofly) baselineOnMigrate() error {
	if !g.Config.BaselineOnMigrate {
		return nil
	}

	applied, err := g.History.All()
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		return nil
	}

	return g.Baseline()
}

// RepairResult reports what a repair run did
type RepairResult struct {
	RemovedFailed    int
	AlignedChecksums int
	MarkedAsDeleted  int
}

// -----------------------------------------------------------------------------
// Repair
//
// Drops the rows of the migrations that failed, realigns the checksums,
// descriptions and types with the files on disk, and marks as deleted the
// applied migrations whose file is gone.
// -----------------------------------------------------------------------------
func (g *Gofly) Repair() (*RepairResult, error) {
	if err := g.EnsureHistory(); err != nil {
		return nil, err
	}

	removed, err := g.History.RemoveFailed()
	if err != nil {
		return nil, err
	}

	result := &RepairResult{RemovedFailed: int(removed)}

	info, err := g.Info()
	if err != nil {
		return nil, err
	}

	for _, migration := range info.Infos {
		if migration.Applied == nil || migration.Applied.Type.IsSynthetic() {
			continue
		}

		if migration.Resolved == nil {
			if migration.State == StateMissingSuccess || migration.State == StateMissingFailed {
				if err := g.History.MarkAsDeleted(migration.Applied.InstalledRank); err != nil {
					return nil, err
				}
				result.MarkedAsDeleted++
			}
			continue
		}

		needsUpdate := migration.Applied.Checksum == nil ||
			*migration.Applied.Checksum != migration.Resolved.Checksum ||
			migration.Applied.Description != AbbreviateDescription(migration.Resolved.Description) ||
			migration.Applied.Type != migration.Resolved.Type

		if !needsUpdate {
			continue
		}

		checksum := migration.Resolved.Checksum
		err := g.History.UpdateChecksum(
			migration.Applied.InstalledRank,
			migration.Resolved.Description,
			migration.Resolved.Type,
			&checksum,
		)
		if err != nil {
			return nil, err
		}
		result.AlignedChecksums++
	}

	g.logf("Repair of schema history table %s completed: %d failed migration(s) removed, %d checksum(s) aligned, %d migration(s) marked as deleted",
		g.History.QualifiedName(), result.RemovedFailed, result.AlignedChecksums, result.MarkedAsDeleted)

	return result, nil
}

// -----------------------------------------------------------------------------
// Clean
//
// Accepted for command line compatibility only. See the message below for why
// gofly does not wipe schemas.
// -----------------------------------------------------------------------------
func (g *Gofly) Clean() error {
	if g.Config.CleanDisabled {
		return errors.New(
			"clean is disabled. Set -cleanDisabled=false to allow it, and never do so against production")
	}

	return errors.New(
		"clean is deliberately not implemented by gofly: wiping a schema is not a migration, " +
			"and every database we support already has a better tool for it. " +
			"Drop and recreate the schema yourself, then run migrate")
}

// -----------------------------------------------------------------------------
// logf
// -----------------------------------------------------------------------------
func (g *Gofly) logf(format string, args ...interface{}) {
	if g.Config.Quiet || g.Output == nil {
		return
	}

	fmt.Fprintf(g.Output, format+"\n", args...)
}

// -----------------------------------------------------------------------------
// DescribeLocations
//
// Renders the configured locations for the banner gofly prints on start up.
// -----------------------------------------------------------------------------
func DescribeLocations(locations []string) string {
	return strings.Join(locations, ", ")
}
