// Copyright (C) 2026 Pau Sanchez
//
// One writable history is necessary for safe handover in both directions.
package lib

import "fmt"

// -----------------------------------------------------------------------------
// selectSharedHistory
//
// Called under the mutation lock, after any competing migrator finishes, and
// points g.History at whichever table sharedHistory settles on.
// -----------------------------------------------------------------------------
func (g *Gofly) selectSharedHistory() error {
	shared, stale, err := g.sharedHistory()
	if err != nil {
		return err
	}
	if stale != nil {
		g.logf("Using %s: %s is an older copy of it and is not kept up to date",
			g.History.QualifiedName(), stale.QualifiedName())
	}
	if shared != nil {
		g.History = shared
	}
	return nil
}

// -----------------------------------------------------------------------------
// sharedHistory
//
// Returns the Flyway table when it is the history to use, or nil when gofly's
// own table is. When both exist, the Flyway one is also returned as stale if it
// is nothing more than the copy an earlier separate-history import started
// from; gofly then keeps its own table, which is how every database taken over
// before reuse became the default looks.
//
// Otherwise never guess which of two histories is authoritative: either could
// hold changes absent from the other.
// -----------------------------------------------------------------------------
func (g *Gofly) sharedHistory() (shared *SchemaHistory, stale *SchemaHistory, err error) {
	if !g.Config.ReuseFlywayHistory || !g.Config.ImportFromFlyway {
		return nil, nil, nil
	}
	source, exists, err := g.flywayHistory()
	if err != nil || !exists {
		return nil, nil, err
	}
	private := NewSchemaHistory(g.Connection, g.historySchema, g.Config.Table, g.Config.ResolveInstalledBy())
	if private.QualifiedName() == source.QualifiedName() {
		return source, nil, nil
	}
	exists, err = private.Exists()
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return source, nil, nil
	}

	flywayRows, err := source.All()
	if err != nil {
		return nil, nil, err
	}
	goflyRows, err := private.All()
	if err != nil {
		return nil, nil, err
	}
	if isStaleCopy(flywayRows, goflyRows) {
		return nil, source, nil
	}
	return nil, nil, fmt.Errorf("both %s and %s exist and %s has migrations %s does not; reconcile and explicitly select one shared history table, or set reuseFlywayHistory=false to keep the private history (do not run Flyway against the stale table)",
		private.QualifiedName(), source.QualifiedName(), source.QualifiedName(), private.QualifiedName())
}

// -----------------------------------------------------------------------------
// isStaleCopy
//
// Reports whether every Flyway row is already in gofly's history, which is the
// case when gofly imported the Flyway table and nothing ran Flyway since. The
// import kept installed_rank, so rows are paired by it. Only the version and
// script have to agree: a gofly repair may have realigned checksums,
// descriptions and types since, or removed a failed row altogether.
//
// A Flyway run after the import adds a row gofly lacks, or one at a rank gofly
// used for another migration, and is refused.
// -----------------------------------------------------------------------------
func isStaleCopy(flyway []*AppliedMigration, gofly []*AppliedMigration) bool {
	byRank := map[int]*AppliedMigration{}
	highest := 0
	for _, row := range gofly {
		byRank[row.InstalledRank] = row
		highest = max(highest, row.InstalledRank)
	}

	for _, row := range flyway {
		copied, found := byRank[row.InstalledRank]
		if !found {
			// a failed row gofly's repair removed. A rank past gofly's last
			// one is Flyway having run after the import, failure or not.
			if !row.Success && row.InstalledRank <= highest {
				continue
			}
			return false
		}
		if copied.Script != row.Script || !sameVersion(copied.Version, row.Version) {
			return false
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// sameVersion
// -----------------------------------------------------------------------------
func sameVersion(a *Version, b *Version) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.CanonicalKey() == b.CanonicalKey()
}
