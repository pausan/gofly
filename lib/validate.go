// Copyright (C) 2026 Pau Sanchez
//
// Validation of the schema history against the migrations on disk.
//
// The checks, their order and their wording follow
// org.flywaydb.core.internal.info.MigrationInfoImpl.validate so that a database
// Flyway refuses to migrate is refused by gofly too, and for the same reason.
package lib

import (
	"fmt"
	"strings"
)

// ErrorCode identifies a validation failure, using Flyway's own codes
type ErrorCode string

const (
	ErrorFailedVersionedMigration              ErrorCode = "FAILED_VERSIONED_MIGRATION"
	ErrorFailedRepeatableMigration             ErrorCode = "FAILED_REPEATABLE_MIGRATION"
	ErrorAppliedVersionedMigrationNotResolved  ErrorCode = "APPLIED_VERSIONED_MIGRATION_NOT_RESOLVED"
	ErrorAppliedRepeatableMigrationNotResolved ErrorCode = "APPLIED_REPEATABLE_MIGRATION_NOT_RESOLVED"
	ErrorResolvedVersionedMigrationNotApplied  ErrorCode = "RESOLVED_VERSIONED_MIGRATION_NOT_APPLIED"
	ErrorResolvedRepeatableMigrationNotApplied ErrorCode = "RESOLVED_REPEATABLE_MIGRATION_NOT_APPLIED"
	ErrorOutdatedRepeatableMigration           ErrorCode = "OUTDATED_REPEATABLE_MIGRATION"
	ErrorTypeMismatch                          ErrorCode = "TYPE_MISMATCH"
	ErrorChecksumMismatch                      ErrorCode = "CHECKSUM_MISMATCH"
	ErrorDescriptionMismatch                   ErrorCode = "DESCRIPTION_MISMATCH"
)

// ValidateError is a single validation failure
type ValidateError struct {
	Code        ErrorCode
	Version     string
	Description string
	FilePath    string
	Message     string
}

// -----------------------------------------------------------------------------
// Error
// -----------------------------------------------------------------------------
func (e *ValidateError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// ValidateContext says which discrepancies are tolerated
type ValidateContext struct {
	// IgnorePending is set while migrating, since the pending migrations are
	// about to be applied anyway
	IgnorePending bool

	// IgnoreMissing tolerates applied migrations whose file is gone
	IgnoreMissing bool

	// IgnoreFuture tolerates history rows newer than anything available here
	IgnoreFuture bool

	// IgnoreIgnored tolerates out of order migrations that will not be applied
	IgnoreIgnored bool
}

// ValidateResult is the outcome of a validation run
type ValidateResult struct {
	Errors          []*ValidateError
	ValidationCount int
}

// -----------------------------------------------------------------------------
// Valid
// -----------------------------------------------------------------------------
func (r *ValidateResult) Valid() bool {
	return len(r.Errors) == 0
}

// -----------------------------------------------------------------------------
// Error
//
// Renders every failure the way the Flyway command line reports them.
// -----------------------------------------------------------------------------
func (r *ValidateResult) Error() error {
	if r.Valid() {
		return nil
	}

	messages := make([]string, 0, len(r.Errors))
	for _, failure := range r.Errors {
		messages = append(messages, failure.Message)
	}

	return fmt.Errorf("validate failed: %s", strings.Join(messages, "\n"))
}

// -----------------------------------------------------------------------------
// Validate
//
// Checks every migration and returns all the problems found.
// -----------------------------------------------------------------------------
func (s *MigrationInfoService) Validate(context ValidateContext) *ValidateResult {
	result := &ValidateResult{}

	for _, info := range s.Infos {
		if failure := validateInfo(info, s.AppliedBaseline, context); failure != nil {
			result.Errors = append(result.Errors, failure)
			continue
		}
		result.ValidationCount++
	}

	return result
}

// -----------------------------------------------------------------------------
// validateInfo
// -----------------------------------------------------------------------------
func validateInfo(info *MigrationInfo, appliedBaseline *Version, context ValidateContext) *ValidateError {
	state := info.State

	// undone, above target and deleted migrations are deliberately out of scope
	if state == StateUndone || state == StateAboveTarget || state == StateDeleted {
		return nil
	}
	if state == StateAvailable || state == StateBelowBaseline {
		return nil
	}

	if state.IsFailed() && (!context.IgnoreFuture || state != StateFutureFailed) {
		if info.Version() == nil {
			return &ValidateError{
				Code:        ErrorFailedRepeatableMigration,
				Description: info.Description(),
				Message: "Detected failed repeatable migration: " + info.Description() +
					".\nPlease remove any half-completed changes then run repair to fix the schema history.",
			}
		}

		return &ValidateError{
			Code:        ErrorFailedVersionedMigration,
			Version:     info.Version().String(),
			Description: info.Description(),
			Message: "Detected failed migration to version " + info.Version().String() +
				" (" + info.Description() + ")" +
				".\nPlease remove any half-completed changes then run repair to fix the schema history.",
		}
	}

	// ---- applied but no longer on disk ------------------------------------
	if info.Resolved == nil && info.Applied != nil &&
		!info.Applied.Type.IsSynthetic() && !info.Applied.Type.IsUndo() &&
		state != StateSuperseded &&
		(!context.IgnoreMissing || (state != StateMissingSuccess && state != StateMissingFailed)) &&
		(!context.IgnoreFuture || (state != StateFutureSuccess && state != StateFutureFailed)) {

		if info.Applied.Version != nil {
			return &ValidateError{
				Code:    ErrorAppliedVersionedMigrationNotResolved,
				Version: info.Applied.Version.String(),
				Message: "Detected applied migration not resolved locally: " + info.Applied.Version.String() +
					".\nIf you removed this migration intentionally, run repair to mark the migration as deleted.",
			}
		}

		return &ValidateError{
			Code:        ErrorAppliedRepeatableMigrationNotResolved,
			Description: info.Applied.Description,
			Message: "Detected applied migration not resolved locally: " + info.Applied.Description +
				".\nIf you removed this migration intentionally, run repair to mark the migration as deleted.",
		}
	}

	// ---- on disk but skipped because a newer one already ran --------------
	if !context.IgnoreIgnored && state == StateIgnored &&
		info.Resolved != nil && !info.Resolved.Type.IsBaseline() && !info.Resolved.Type.IsUndo() {

		if info.Version() != nil {
			return &ValidateError{
				Code:     ErrorResolvedVersionedMigrationNotApplied,
				Version:  info.Version().String(),
				FilePath: info.Resolved.PhysicalLocation,
				Message: "Detected resolved migration not applied to database: " + info.Version().String() +
					".\nTo allow executing this migration, set -outOfOrder=true.",
			}
		}

		return &ValidateError{
			Code:        ErrorResolvedRepeatableMigrationNotApplied,
			Description: info.Description(),
			Message:     "Detected resolved repeatable migration not applied to database: " + info.Description(),
		}
	}

	// ---- on disk and still to run -----------------------------------------
	if !context.IgnorePending && state == StatePending {
		if info.Version() != nil {
			return &ValidateError{
				Code:     ErrorResolvedVersionedMigrationNotApplied,
				Version:  info.Version().String(),
				FilePath: info.Resolved.PhysicalLocation,
				Message: "Detected resolved migration not applied to database: " + info.Version().String() +
					".\nTo fix this error, either run migrate, or ignore pending migrations.",
			}
		}

		return &ValidateError{
			Code:        ErrorResolvedRepeatableMigrationNotApplied,
			Description: info.Description(),
			FilePath:    info.Resolved.PhysicalLocation,
			Message: "Detected resolved repeatable migration not applied to database: " + info.Description() +
				".\nTo fix this error, either run migrate, or ignore pending migrations.",
		}
	}

	if !context.IgnorePending && state == StateOutdated {
		return &ValidateError{
			Code:        ErrorOutdatedRepeatableMigration,
			Description: info.Description(),
			Message: "Detected outdated resolved repeatable migration that should be re-applied to database: " +
				info.Description() + ".\nRun migrate to execute this migration.",
		}
	}

	// ---- the migration is both on disk and in the history ------------------
	if info.Resolved == nil || info.Applied == nil {
		return nil
	}
	if info.Applied.Type == MigrationTypeDelete || info.Applied.Type.IsUndo() {
		return nil
	}

	// migrations at or below the baseline were never run by us, so their
	// contents are none of our business
	if info.Version() != nil && appliedBaseline.Kind() == VersionKindReal &&
		info.Version().Compare(appliedBaseline) <= 0 {
		return nil
	}

	identifier := info.Applied.Script
	if info.Applied.Version != nil {
		identifier = "version " + info.Applied.Version.String()
	}

	if info.Resolved.Type != info.Applied.Type {
		return &ValidateError{
			Code:     ErrorTypeMismatch,
			Version:  versionText(info.Version()),
			FilePath: info.Resolved.PhysicalLocation,
			Message: mismatchMessage("type", identifier,
				string(info.Applied.Type), string(info.Resolved.Type)),
		}
	}

	// a repeatable migration that is due to be re-applied is expected to differ
	// from the row recording its previous run
	checksumMatters := info.Resolved.Version != nil ||
		(context.IgnorePending && state != StateOutdated && state != StateSuperseded)

	if checksumMatters && !checksumMatches(info) {
		return &ValidateError{
			Code:     ErrorChecksumMismatch,
			Version:  versionText(info.Version()),
			FilePath: info.Resolved.PhysicalLocation,
			Message: mismatchMessage("checksum", identifier,
				checksumText(info.Applied.Checksum), fmt.Sprintf("%d", info.Resolved.Checksum)),
		}
	}

	if descriptionMismatch(info) {
		return &ValidateError{
			Code:     ErrorDescriptionMismatch,
			Version:  versionText(info.Version()),
			FilePath: info.Resolved.PhysicalLocation,
			Message: mismatchMessage("description", identifier,
				info.Applied.Description, info.Resolved.Description),
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// checksumMatches
//
// A history row with no checksum at all, which is what a baseline looks like,
// never fails the comparison.
// -----------------------------------------------------------------------------
func checksumMatches(info *MigrationInfo) bool {
	if info.Applied.Checksum == nil {
		return true
	}

	return *info.Applied.Checksum == info.Resolved.Checksum
}

// -----------------------------------------------------------------------------
// descriptionMismatch
// -----------------------------------------------------------------------------
func descriptionMismatch(info *MigrationInfo) bool {
	return AbbreviateDescription(info.Resolved.Description) != info.Applied.Description
}

// -----------------------------------------------------------------------------
// mismatchMessage
// -----------------------------------------------------------------------------
func mismatchMessage(mismatch string, identifier string, applied string, resolved string) string {
	return fmt.Sprintf(
		"Migration %s mismatch for migration %s\n"+
			"-> Applied to database : %s\n"+
			"-> Resolved locally    : %s\n"+
			"Either revert the changes to the migration, or run repair to update the schema history.",
		mismatch, identifier, applied, resolved,
	)
}

// -----------------------------------------------------------------------------
// checksumText
// -----------------------------------------------------------------------------
func checksumText(checksum *int32) string {
	if checksum == nil {
		return "null"
	}

	return fmt.Sprintf("%d", *checksum)
}

// -----------------------------------------------------------------------------
// versionText
// -----------------------------------------------------------------------------
func versionText(version *Version) string {
	if version == nil {
		return ""
	}

	return version.String()
}
