// Copyright (C) 2026 Pau Sanchez
//
// One writable history is necessary for safe handover in both directions.
package lib

import "fmt"

// -----------------------------------------------------------------------------
// selectSharedHistory
//
// Called under the mutation lock, after any competing migrator finishes. Never
// guess which of two pre-existing histories is authoritative: either could hold
// changes absent from the other. Legacy private-history use remains explicit.
// -----------------------------------------------------------------------------
func (g *Gofly) selectSharedHistory() error {
	if !g.Config.ReuseFlywayHistory || !g.Config.ImportFromFlyway {
		return nil
	}
	source, exists, err := g.flywayHistory()
	if err != nil || !exists {
		return err
	}
	private := NewSchemaHistory(g.Connection, g.historySchema, g.Config.Table, g.Config.ResolveInstalledBy())
	if private.QualifiedName() != source.QualifiedName() {
		exists, err := private.Exists()
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("both %s and %s exist; reconcile and explicitly select one shared history table, or set reuseFlywayHistory=false to keep the private history (do not run Flyway against the stale table)", private.QualifiedName(), source.QualifiedName())
		}
	}
	g.History = source
	return nil
}
