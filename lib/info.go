// Copyright (C) 2026 Pau Sanchez
//
// Pairing of the migrations found on disk with the rows of the schema history,
// and the state each pair ends up in.
//
// See org.flywaydb.core.internal.info.MigrationInfoServiceImpl
package lib

import (
	"sort"
)

// MigrationState mirrors org.flywaydb.core.api.MigrationState
type MigrationState string

const (
	StatePending       MigrationState = "Pending"
	StateAboveTarget   MigrationState = "Above Target"
	StateBelowBaseline MigrationState = "Below Baseline"
	// StateBaselineIgnored is the migration sitting exactly at the baseline
	// version: the baseline row already stands for it, so it never runs
	StateBaselineIgnored MigrationState = "Ignored (Baseline)"
	StateBaseline        MigrationState = "Baseline"
	StateIgnored         MigrationState = "Ignored"
	StateMissingSuccess  MigrationState = "Missing"
	StateMissingFailed   MigrationState = "Failed (Missing)"
	StateSuccess         MigrationState = "Success"
	StateUndone          MigrationState = "Undone"
	StateAvailable       MigrationState = "Available"
	StateFailed          MigrationState = "Failed"
	StateOutOfOrder      MigrationState = "Out of Order"
	StateFutureSuccess   MigrationState = "Future"
	StateFutureFailed    MigrationState = "Failed (Future)"
	StateOutdated        MigrationState = "Outdated"
	StateSuperseded      MigrationState = "Superseded"
	StateDeleted         MigrationState = "Deleted"
)

// -----------------------------------------------------------------------------
// IsApplied
// -----------------------------------------------------------------------------
func (s MigrationState) IsApplied() bool {
	switch s {
	case StateBaseline, StateMissingSuccess, StateMissingFailed, StateSuccess, StateUndone,
		StateFailed, StateOutOfOrder, StateFutureSuccess, StateFutureFailed, StateOutdated,
		StateSuperseded, StateDeleted:
		return true
	}

	return false
}

// -----------------------------------------------------------------------------
// IsFailed
// -----------------------------------------------------------------------------
func (s MigrationState) IsFailed() bool {
	return s == StateFailed || s == StateMissingFailed || s == StateFutureFailed
}

// MigrationInfo pairs a migration on disk with its row in the history table
type MigrationInfo struct {
	Resolved   *ResolvedMigration
	Applied    *AppliedMigration
	State      MigrationState
	OutOfOrder bool
}

// -----------------------------------------------------------------------------
// Version
// -----------------------------------------------------------------------------
func (i *MigrationInfo) Version() *Version {
	if i.Applied != nil {
		return i.Applied.Version
	}

	return i.Resolved.Version
}

// -----------------------------------------------------------------------------
// Description
// -----------------------------------------------------------------------------
func (i *MigrationInfo) Description() string {
	if i.Applied != nil {
		return i.Applied.Description
	}

	return i.Resolved.Description
}

// -----------------------------------------------------------------------------
// Script
// -----------------------------------------------------------------------------
func (i *MigrationInfo) Script() string {
	if i.Applied != nil {
		return i.Applied.Script
	}

	return i.Resolved.Script
}

// -----------------------------------------------------------------------------
// Type
// -----------------------------------------------------------------------------
func (i *MigrationInfo) Type() MigrationType {
	if i.Applied != nil {
		return i.Applied.Type
	}

	return i.Resolved.Type
}

// -----------------------------------------------------------------------------
// IsVersioned
// -----------------------------------------------------------------------------
func (i *MigrationInfo) IsVersioned() bool {
	return i.Version() != nil
}

// MigrationInfoService is the full picture of a database at a point in time
type MigrationInfoService struct {
	Infos []*MigrationInfo

	// AppliedBaseline is the version the database was baselined at
	AppliedBaseline *Version

	// Current is the newest version successfully applied
	Current *Version

	// Target is the version migrations stop at
	Target *Version
}

// -----------------------------------------------------------------------------
// BuildMigrationInfo
//
// Pairs the resolved migrations with the applied ones and works out the state
// of each of them.
// -----------------------------------------------------------------------------
func BuildMigrationInfo(
	config *Config,
	resolved *ResolvedMigrations,
	applied []*AppliedMigration,
) (*MigrationInfoService, error) {
	target, err := config.TargetVersion()
	if err != nil {
		return nil, err
	}

	service := &MigrationInfoService{
		AppliedBaseline: VersionEmpty,
		Current:         VersionEmpty,
		Target:          target,
	}

	// the highest version we know about locally, used to tell a migration that
	// went missing from one applied by a newer deployment
	maxResolvedVersion := VersionEmpty
	for _, migration := range resolved.Versioned {
		if migration.Version.IsNewerThan(maxResolvedVersion) {
			maxResolvedVersion = migration.Version
		}
	}

	resolvedByVersion := map[string]*ResolvedMigration{}
	for _, migration := range resolved.Versioned {
		resolvedByVersion[migration.Version.CanonicalKey()] = migration
	}

	resolvedByDescription := map[string]*ResolvedMigration{}
	for _, migration := range resolved.Repeatable {
		resolvedByDescription[migration.Description] = migration
	}

	// ---- first pass over the history, in the order things happened ---------
	undone := map[string]bool{}
	deleted := map[string]bool{}
	pairedVersions := map[string]bool{}
	pairedDescriptions := map[string]bool{}
	outOfOrderVersions := map[string]bool{}
	latestRepeatableRank := map[string]int{}

	highestAppliedSoFar := VersionEmpty

	sortedApplied := make([]*AppliedMigration, len(applied))
	copy(sortedApplied, applied)
	sort.SliceStable(sortedApplied, func(i, j int) bool {
		return sortedApplied[i].InstalledRank < sortedApplied[j].InstalledRank
	})

	for _, migration := range sortedApplied {
		if migration.Type == MigrationTypeBaseline && migration.Version != nil {
			service.AppliedBaseline = migration.Version
		}

		if migration.Version == nil {
			if migration.Type != MigrationTypeDelete {
				latestRepeatableRank[migration.Description] = migration.InstalledRank
			}
			if migration.Type == MigrationTypeDelete {
				deleted["R:"+migration.Description] = true
			}
			continue
		}

		key := migration.Version.CanonicalKey()

		switch {
		case migration.Type == MigrationTypeDelete:
			deleted["V:"+key] = true

		case migration.Type.IsUndo():
			// the undo row cancels the versioned migration of the same version
			if migration.Success {
				undone[key] = true
			}

		default:
			// a re-run of the same version brings it back to life
			undone[key] = false
			deleted["V:"+key] = false

			if migration.Success {
				if migration.Version.Compare(highestAppliedSoFar) < 0 {
					outOfOrderVersions[key] = true
				} else {
					highestAppliedSoFar = migration.Version
				}
			}
		}
	}

	// ---- one info per history row -----------------------------------------
	for _, migration := range sortedApplied {
		info := &MigrationInfo{Applied: migration}

		if migration.Version != nil {
			key := migration.Version.CanonicalKey()
			if !migration.Type.IsUndo() && !migration.Type.IsSynthetic() {
				// an undone migration is deliberately left unpaired, so that
				// the file on disk shows up as pending and migrate runs it
				// again
				if !undone[key] {
					info.Resolved = resolvedByVersion[key]
					pairedVersions[key] = true
				}
			}
			if migration.Type.IsUndo() {
				info.Resolved = resolved.UndoFor(migration.Version)
			}
			info.OutOfOrder = outOfOrderVersions[key]
		} else if migration.Type != MigrationTypeDelete {
			if latestRepeatableRank[migration.Description] == migration.InstalledRank {
				info.Resolved = resolvedByDescription[migration.Description]
				pairedDescriptions[migration.Description] = true
			}
		}

		info.State = appliedState(info, migration, undone, deleted, maxResolvedVersion, latestRepeatableRank)
		service.Infos = append(service.Infos, info)
	}

	// ---- and one per migration that has never been applied -----------------
	for _, migration := range resolved.Versioned {
		if pairedVersions[migration.Version.CanonicalKey()] {
			continue
		}

		info := &MigrationInfo{Resolved: migration}
		info.State = pendingState(config, migration.Version, service.AppliedBaseline, highestAppliedSoFar, target)
		service.Infos = append(service.Infos, info)
	}

	for _, migration := range resolved.Undo {
		if undone[migration.Version.CanonicalKey()] {
			continue
		}
		if hasUndoInfo(service.Infos, migration) {
			continue
		}

		service.Infos = append(service.Infos, &MigrationInfo{Resolved: migration, State: StateAvailable})
	}

	for _, migration := range resolved.Repeatable {
		if pairedDescriptions[migration.Description] {
			continue
		}

		service.Infos = append(service.Infos, &MigrationInfo{Resolved: migration, State: StatePending})
	}

	service.Current = currentVersion(service.Infos)
	sortInfos(service.Infos)

	return service, nil
}

// -----------------------------------------------------------------------------
// appliedState
//
// Works out the state of a migration that has a row in the history table.
// -----------------------------------------------------------------------------
func appliedState(
	info *MigrationInfo,
	migration *AppliedMigration,
	undone map[string]bool,
	deleted map[string]bool,
	maxResolvedVersion *Version,
	latestRepeatableRank map[string]int,
) MigrationState {
	if migration.Version != nil {
		key := migration.Version.String()

		if deleted["V:"+key] || migration.Type == MigrationTypeDelete {
			return StateDeleted
		}
		if undone[key] && !migration.Type.IsUndo() {
			return StateUndone
		}
	} else {
		if deleted["R:"+migration.Description] || migration.Type == MigrationTypeDelete {
			return StateDeleted
		}
		if latestRepeatableRank[migration.Description] != migration.InstalledRank {
			return StateSuperseded
		}
	}

	if !migration.Success {
		if migration.Version != nil && migration.Version.IsNewerThan(maxResolvedVersion) {
			return StateFutureFailed
		}
		return StateFailed
	}

	if migration.Type == MigrationTypeBaseline {
		return StateBaseline
	}
	if migration.Type.IsSynthetic() || migration.Type.IsUndo() {
		return StateSuccess
	}

	if info.Resolved == nil {
		if migration.Version != nil && migration.Version.IsNewerThan(maxResolvedVersion) {
			return StateFutureSuccess
		}
		return StateMissingSuccess
	}

	// a repeatable migration whose file changed since it was last run has to be
	// applied again
	if migration.Version == nil && info.Resolved != nil {
		if migration.Checksum == nil || *migration.Checksum != info.Resolved.Checksum {
			return StateOutdated
		}
	}

	if info.OutOfOrder {
		return StateOutOfOrder
	}

	return StateSuccess
}

// -----------------------------------------------------------------------------
// pendingState
//
// Works out the state of a migration that is not in the history table yet.
// -----------------------------------------------------------------------------
func pendingState(
	config *Config,
	version *Version,
	appliedBaseline *Version,
	highestApplied *Version,
	target *Version,
) MigrationState {
	if target.Kind() == VersionKindReal && version.IsNewerThan(target) {
		return StateAboveTarget
	}
	if appliedBaseline.Kind() == VersionKindReal {
		// the file at the baseline version is reported apart from the ones
		// under it: the baseline row was taken to stand for exactly that
		// migration, while the older ones were simply never in scope
		if version.Compare(appliedBaseline) < 0 {
			return StateBelowBaseline
		}
		if version.Compare(appliedBaseline) == 0 {
			return StateBaselineIgnored
		}
	}
	if version.Compare(highestApplied) < 0 {
		// an older migration showing up after newer ones have run is only
		// applied when the user explicitly allows it
		if config.OutOfOrder {
			return StatePending
		}
		return StateIgnored
	}

	return StatePending
}

// -----------------------------------------------------------------------------
// hasUndoInfo
// -----------------------------------------------------------------------------
func hasUndoInfo(infos []*MigrationInfo, migration *ResolvedMigration) bool {
	for _, info := range infos {
		if info.Resolved == migration {
			return true
		}
	}

	return false
}

// -----------------------------------------------------------------------------
// currentVersion
//
// Returns the newest version that counts as applied right now.
// -----------------------------------------------------------------------------
func currentVersion(infos []*MigrationInfo) *Version {
	current := VersionEmpty

	for _, info := range infos {
		if !info.State.IsApplied() || info.Version() == nil {
			continue
		}
		if info.State == StateDeleted || info.State == StateUndone || info.Type() == MigrationTypeDelete {
			continue
		}
		if info.Type().IsUndo() {
			continue
		}
		if info.Version().IsNewerThan(current) {
			current = info.Version()
		}
	}

	return current
}

// -----------------------------------------------------------------------------
// isBaselineSkipped
//
// Tells whether a migration was left out because of the baseline, either
// because it sits below it or exactly at it.
// -----------------------------------------------------------------------------
func isBaselineSkipped(state MigrationState) bool {
	return state == StateBelowBaseline || state == StateBaselineIgnored
}

// -----------------------------------------------------------------------------
// sortInfos
//
// Applied migrations come first, in the order they were applied, followed by
// everything still to do, in version order. The migrations the baseline skipped
// are the exception and lead the table, see MigrationInfoImpl.compareTo.
// -----------------------------------------------------------------------------
func sortInfos(infos []*MigrationInfo) {
	sort.SliceStable(infos, func(i, j int) bool {
		left, right := infos[i], infos[j]

		if left.Applied != nil && right.Applied != nil {
			return left.Applied.InstalledRank < right.Applied.InstalledRank
		}

		// a migration the baseline skipped is listed ahead of the applied ones,
		// so that the file sitting at the baseline version reads next to the
		// baseline row rather than trailing the whole history
		if isBaselineSkipped(left.State) && right.State.IsApplied() {
			return true
		}
		if left.State.IsApplied() && isBaselineSkipped(right.State) {
			return false
		}

		if left.Applied != nil {
			return true
		}
		if right.Applied != nil {
			return false
		}

		leftVersion, rightVersion := left.Version(), right.Version()
		switch {
		case leftVersion == nil && rightVersion == nil:
			return left.Description() < right.Description()
		case leftVersion == nil:
			return false
		case rightVersion == nil:
			return true
		}

		if compared := leftVersion.Compare(rightVersion); compared != 0 {
			return compared < 0
		}

		// the undo migration of a version comes right after it
		return !left.Type().IsUndo() && right.Type().IsUndo()
	})
}

// -----------------------------------------------------------------------------
// Pending
//
// Returns the versioned and repeatable migrations that migrate would run, in
// the order they would run.
// -----------------------------------------------------------------------------
func (s *MigrationInfoService) Pending() []*MigrationInfo {
	pending := []*MigrationInfo{}

	for _, info := range s.Infos {
		if info.Type().IsUndo() {
			continue
		}
		if info.State == StatePending || info.State == StateOutdated {
			pending = append(pending, info)
		}
	}

	// versioned migrations run in version order, then the repeatable ones in
	// alphabetical order, which is what Flyway does
	sort.SliceStable(pending, func(i, j int) bool {
		left, right := pending[i].Version(), pending[j].Version()
		switch {
		case left == nil && right == nil:
			return pending[i].Description() < pending[j].Description()
		case left == nil:
			return false
		case right == nil:
			return true
		}

		return left.Compare(right) < 0
	})

	return pending
}

// -----------------------------------------------------------------------------
// Applied
// -----------------------------------------------------------------------------
func (s *MigrationInfoService) Applied() []*MigrationInfo {
	result := []*MigrationInfo{}

	for _, info := range s.Infos {
		if info.State.IsApplied() {
			result = append(result, info)
		}
	}

	return result
}

// -----------------------------------------------------------------------------
// Failed
// -----------------------------------------------------------------------------
func (s *MigrationInfoService) Failed() []*MigrationInfo {
	result := []*MigrationInfo{}

	for _, info := range s.Infos {
		if info.State.IsFailed() {
			result = append(result, info)
		}
	}

	return result
}

// -----------------------------------------------------------------------------
// Undoable
//
// Returns the applied versioned migrations that have an undo script, newest
// first, which is the order undo runs them in.
// -----------------------------------------------------------------------------
func (s *MigrationInfoService) Undoable(resolved *ResolvedMigrations) []*MigrationInfo {
	candidates := []*MigrationInfo{}

	for _, info := range s.Infos {
		if info.Version() == nil || info.Type().IsUndo() {
			continue
		}
		if info.State != StateSuccess && info.State != StateOutOfOrder {
			continue
		}
		if info.Type().IsSynthetic() {
			continue
		}
		if resolved.UndoFor(info.Version()) == nil {
			continue
		}
		candidates = append(candidates, info)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Applied.InstalledRank > candidates[j].Applied.InstalledRank
	})

	return candidates
}
