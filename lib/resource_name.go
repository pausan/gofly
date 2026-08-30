// Copyright (C) 2026 Pau Sanchez
//
// Flyway-compatible migration file name parsing.
//
// Versioned and undo migrations are named prefixVERSIONseparatorDESCRIPTIONsuffix
// (V1.1__Add_users.sql), repeatable ones prefixSeparatorDESCRIPTIONsuffix
// (R__Refresh_views.sql).
//
// See org.flywaydb.core.internal.resource.ResourceNameParser
package lib

import (
	"fmt"
	"sort"
	"strings"
)

// MigrationType is the value stored in the `type` column of the history table
type MigrationType string

const (
	MigrationTypeSchema   MigrationType = "SCHEMA"
	MigrationTypeBaseline MigrationType = "BASELINE"
	MigrationTypeDelete   MigrationType = "DELETE"
	MigrationTypeSQL      MigrationType = "SQL"
	MigrationTypeUndoSQL  MigrationType = "UNDO_SQL"
)

// -----------------------------------------------------------------------------
// IsSynthetic
//
// Synthetic types only ever exist in the history table, they are never resolved
// from the file system.
// -----------------------------------------------------------------------------
func (t MigrationType) IsSynthetic() bool {
	return t == MigrationTypeSchema || t == MigrationTypeBaseline || t == MigrationTypeDelete
}

// -----------------------------------------------------------------------------
// IsUndo
// -----------------------------------------------------------------------------
func (t MigrationType) IsUndo() bool {
	return t == MigrationTypeUndoSQL
}

// -----------------------------------------------------------------------------
// IsBaseline
// -----------------------------------------------------------------------------
func (t MigrationType) IsBaseline() bool {
	return t == MigrationTypeBaseline
}

// ResourceType tells which kind of file a name refers to
type ResourceType int

const (
	ResourceTypeUnknown ResourceType = iota
	ResourceTypeVersioned
	ResourceTypeUndo
	ResourceTypeRepeatable
	ResourceTypeCallback
)

// -----------------------------------------------------------------------------
// IsVersioned
// -----------------------------------------------------------------------------
func (t ResourceType) IsVersioned() bool {
	return t == ResourceTypeVersioned || t == ResourceTypeUndo
}

// ResourceName holds a migration file name broken into its components
type ResourceName struct {
	Prefix       string
	VersionText  string
	Separator    string
	Description  string
	RawStem      string
	Suffix       string
	Type         ResourceType
	Version      *Version
	Valid        bool
	InvalidCause string
}

// callbackEvents are the callback names Flyway recognises as file prefixes.
// They are listed here so that a file named afterMigrate.sql is never mistaken
// for a versioned migration.
var callbackEvents = []string{
	"beforeConnect",
	"beforeMigrate",
	"beforeRepeatables",
	"beforeEachMigrate",
	"beforeEachMigrateStatement",
	"afterEachMigrateStatement",
	"afterEachMigrateStatementError",
	"afterEachMigrate",
	"afterEachMigrateError",
	"afterMigrate",
	"afterMigrateApplied",
	"afterVersioned",
	"afterMigrateError",
	"beforeUndo",
	"beforeEachUndo",
	"beforeEachUndoStatement",
	"afterEachUndoStatement",
	"afterEachUndoStatementError",
	"afterEachUndo",
	"afterEachUndoError",
	"afterUndo",
	"afterUndoError",
	"beforeClean",
	"afterClean",
	"afterCleanError",
	"beforeInfo",
	"afterInfo",
	"afterInfoError",
	"beforeValidate",
	"afterValidate",
	"afterValidateError",
	"beforeBaseline",
	"afterBaseline",
	"afterBaselineError",
	"beforeRepair",
	"afterRepair",
	"afterRepairError",
	"createSchema",
}

// Naming groups every setting that drives migration file name resolution
type Naming struct {
	SQLMigrationPrefix           string
	UndoSQLMigrationPrefix       string
	RepeatableSQLMigrationPrefix string
	SQLMigrationSeparator        string
	SQLMigrationSuffixes         []string
}

// -----------------------------------------------------------------------------
// DefaultNaming
//
// Returns Flyway's out of the box naming conventions.
// -----------------------------------------------------------------------------
func DefaultNaming() Naming {
	return Naming{
		SQLMigrationPrefix:           "V",
		UndoSQLMigrationPrefix:       "U",
		RepeatableSQLMigrationPrefix: "R",
		SQLMigrationSeparator:        "__",
		SQLMigrationSuffixes:         []string{".sql"},
	}
}

// prefixEntry associates a file name prefix with the kind of resource it marks
type prefixEntry struct {
	prefix       string
	resourceType ResourceType
}

// -----------------------------------------------------------------------------
// prefixes
//
// Builds the prefix table sorted by descending length, so that the most
// specific prefix always wins (this is what lets a custom prefix such as "VV"
// coexist with "V").
// -----------------------------------------------------------------------------
func (n Naming) prefixes() []prefixEntry {
	entries := []prefixEntry{}

	if n.SQLMigrationPrefix != "" {
		entries = append(entries, prefixEntry{n.SQLMigrationPrefix, ResourceTypeVersioned})
	}
	if n.UndoSQLMigrationPrefix != "" {
		entries = append(entries, prefixEntry{n.UndoSQLMigrationPrefix, ResourceTypeUndo})
	}
	if n.RepeatableSQLMigrationPrefix != "" {
		entries = append(entries, prefixEntry{n.RepeatableSQLMigrationPrefix, ResourceTypeRepeatable})
	}
	for _, event := range callbackEvents {
		entries = append(entries, prefixEntry{event, ResourceTypeCallback})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return len(entries[i].prefix) > len(entries[j].prefix)
	})

	return entries
}

// -----------------------------------------------------------------------------
// ParseResourceName
//
// Splits a migration file name into its parts. A name that does not follow the
// conventions comes back with Valid set to false and InvalidCause explaining
// why, mirroring how Flyway reports these problems.
// -----------------------------------------------------------------------------
func ParseResourceName(fileName string, naming Naming) ResourceName {
	stem, suffix := stripSuffix(fileName, naming.SQLMigrationSuffixes)

	entry, found := findPrefix(stem, naming.prefixes())
	if !found {
		return ResourceName{
			Valid:        false,
			InvalidCause: "Unrecognised migration name format: " + fileName,
			Type:         ResourceTypeUnknown,
		}
	}

	withoutPrefix := stem[len(entry.prefix):]
	versionText, rawDescription := splitAtFirstSeparator(withoutPrefix, naming.SQLMigrationSeparator)

	name := ResourceName{
		Prefix:      entry.prefix,
		VersionText: versionText,
		Separator:   naming.SQLMigrationSeparator,
		Description: strings.ReplaceAll(rawDescription, "_", " "),
		RawStem:     rawDescription,
		Suffix:      suffix,
		Type:        entry.resourceType,
		Valid:       true,
	}

	exampleDescription := rawDescription
	if exampleDescription == "" {
		exampleDescription = "description"
	}

	if !entry.resourceType.IsVersioned() {
		// repeatable migrations and callbacks must not carry a version
		if versionText != "" {
			name.Valid = false
			name.InvalidCause = fmt.Sprintf(
				"Invalid repeatable migration / callback name format: %s (It cannot contain a version and should look like this: %s%s%s%s)",
				fileName, entry.prefix, naming.SQLMigrationSeparator, exampleDescription, suffix,
			)
		}
		return name
	}

	if versionText == "" {
		name.Valid = false
		name.InvalidCause = fmt.Sprintf(
			"Invalid versioned migration name format: %s (It must contain a version and should look like this: %s1.2%s%s%s)",
			fileName, entry.prefix, naming.SQLMigrationSeparator, exampleDescription, suffix,
		)
		return name
	}

	version, err := NewVersion(versionText)
	if err != nil {
		name.Valid = false
		name.InvalidCause = fmt.Sprintf(
			"Invalid versioned migration name format: %s (could not recognise version number %s)",
			fileName, versionText,
		)
		return name
	}

	name.Version = version
	return name
}

// -----------------------------------------------------------------------------
// stripSuffix
//
// Removes the first matching suffix, comparing case insensitively the way
// Flyway does.
// -----------------------------------------------------------------------------
func stripSuffix(name string, suffixes []string) (string, string) {
	upperName := strings.ToUpper(name)

	for _, suffix := range suffixes {
		if suffix == "" {
			continue
		}
		if strings.HasSuffix(upperName, strings.ToUpper(suffix)) {
			cut := len(name) - len(suffix)
			return name[:cut], name[cut:]
		}
	}

	return name, ""
}

// -----------------------------------------------------------------------------
// findPrefix
// -----------------------------------------------------------------------------
func findPrefix(stem string, entries []prefixEntry) (prefixEntry, bool) {
	for _, entry := range entries {
		if strings.HasPrefix(stem, entry.prefix) {
			return entry, true
		}
	}

	return prefixEntry{}, false
}

// -----------------------------------------------------------------------------
// splitAtFirstSeparator
// -----------------------------------------------------------------------------
func splitAtFirstSeparator(input string, separator string) (string, string) {
	index := strings.Index(input, separator)
	if index < 0 {
		return input, ""
	}

	return input[:index], input[index+len(separator):]
}

// candidateSeparators are the separators worth suggesting when the configured
// one does not parse the migrations found on disk. "__" is Flyway's default,
// "_" and "-" show up in projects that configured it long ago and have since
// lost track of where it was set.
var candidateSeparators = []string{"__", "_", "-", "--"}

// -----------------------------------------------------------------------------
// DetectSeparator
//
// Looks for a separator that parses more of the given file names than the
// configured one does, and returns it along with the number of names it
// parses.
//
// Only a strict winner is reported: when two candidates parse the same number
// of names the convention in use is ambiguous, and guessing would be worse
// than saying nothing.
//
// This never changes how migrations are read, it only feeds an error message.
// Picking a separator silently would quietly change what a name means: with
// "__" the file V1_2__add_users.sql is version 1.2 described "add users",
// while with "_" it is version 1 described "2  add users". Applying the wrong
// reading would write the wrong version into the schema history, so the choice
// stays with whoever knows the project.
// -----------------------------------------------------------------------------
func DetectSeparator(fileNames []string, naming Naming) (string, int, bool) {
	best := ""
	bestCount := countParseable(fileNames, naming, naming.SQLMigrationSeparator)
	ambiguous := false

	for _, candidate := range candidateSeparators {
		if candidate == naming.SQLMigrationSeparator {
			continue
		}

		switch count := countParseable(fileNames, naming, candidate); {
		case count > bestCount:
			best, bestCount, ambiguous = candidate, count, false
		case count == bestCount && best != "":
			ambiguous = true
		}
	}

	if best == "" || ambiguous {
		return "", 0, false
	}

	return best, bestCount, true
}

// -----------------------------------------------------------------------------
// countParseable
//
// Counts how many of the names parse cleanly using the given separator.
// -----------------------------------------------------------------------------
func countParseable(fileNames []string, naming Naming, separator string) int {
	naming.SQLMigrationSeparator = separator

	count := 0
	for _, fileName := range fileNames {
		if ParseResourceName(fileName, naming).Valid {
			count++
		}
	}

	return count
}
