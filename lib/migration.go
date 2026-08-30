// Copyright (C) 2026 Pau Sanchez
//
// Discovery of the migrations available on disk.
package lib

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolvedMigration is a migration found on the file system
type ResolvedMigration struct {
	Version          *Version
	Description      string
	Type             MigrationType
	Script           string
	Checksum         int32
	PhysicalLocation string
	IsUndo           bool
	IsRepeatable     bool
}

// -----------------------------------------------------------------------------
// Key
//
// Returns the identity used to pair a resolved migration with an applied one.
// -----------------------------------------------------------------------------
func (m *ResolvedMigration) Key() string {
	if m.IsRepeatable {
		return "R:" + m.Description
	}
	if m.IsUndo {
		return "U:" + m.Version.String()
	}

	return "V:" + m.Version.String()
}

// ResolvedMigrations groups everything found across all the locations
type ResolvedMigrations struct {
	Versioned  []*ResolvedMigration
	Undo       []*ResolvedMigration
	Repeatable []*ResolvedMigration
}

// -----------------------------------------------------------------------------
// UndoFor
//
// Returns the undo migration matching a version, if any.
// -----------------------------------------------------------------------------
func (r *ResolvedMigrations) UndoFor(version *Version) *ResolvedMigration {
	for _, migration := range r.Undo {
		if migration.Version.Equals(version) {
			return migration
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// ResolveMigrations
//
// Scans every configured location and returns the migrations found, sorted by
// version (and by description for the repeatable ones). Duplicated versions and
// unparseable names are reported as errors, the way Flyway does.
// -----------------------------------------------------------------------------
func ResolveMigrations(config *Config) (*ResolvedMigrations, error) {
	resolved := &ResolvedMigrations{}

	// every name that matched a prefix, kept so that a name failure can be
	// explained against the whole set rather than the one file that tripped
	candidateNames := []string{}
	var nameError error

	seenVersioned := map[string]string{}
	seenUndo := map[string]string{}
	seenRepeatable := map[string]string{}

	for _, location := range config.Locations {
		root, err := locationPath(location)
		if err != nil {
			return nil, err
		}
		if root == "" {
			continue
		}

		info, err := os.Stat(root)
		if err != nil {
			// Flyway only warns about locations that are not there, since a
			// project may legitimately ship some of them empty
			continue
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("location %s is not a directory", root)
		}

		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}

			// anything that does not carry one of the configured suffixes is
			// not a migration at all: a README.md sitting next to the scripts
			// must not be mistaken for a repeatable one
			if !hasMigrationSuffix(entry.Name(), config.SQLMigrationSuffixes) {
				return nil
			}

			name := ParseResourceName(entry.Name(), config.Naming)
			if name.Type == ResourceTypeCallback {
				return nil
			}
			// files that match no prefix at all are simply not migrations
			if name.Type == ResourceTypeUnknown {
				return nil
			}
			candidateNames = append(candidateNames, entry.Name())

			if !name.Valid {
				// the scan carries on so that DetectSeparator below sees every
				// name; the first failure is still the one reported
				if nameError == nil {
					nameError = fmt.Errorf("%s", name.InvalidCause)
				}
				return nil
			}

			checksum, err := ChecksumFile(path)
			if err != nil {
				return fmt.Errorf("cannot read %s: %w", path, err)
			}

			script, err := filepath.Rel(root, path)
			if err != nil {
				script = entry.Name()
			}
			script = filepath.ToSlash(script)

			migration := &ResolvedMigration{
				Version:          name.Version,
				Description:      name.Description,
				Script:           script,
				Checksum:         checksum,
				PhysicalLocation: path,
			}

			switch name.Type {
			case ResourceTypeVersioned:
				migration.Type = MigrationTypeSQL
				if previous, duplicated := seenVersioned[migration.Version.CanonicalKey()]; duplicated {
					return fmt.Errorf(
						"found more than one migration with version %s\n-> %s\n-> %s",
						migration.Version, previous, path,
					)
				}
				seenVersioned[migration.Version.CanonicalKey()] = path
				resolved.Versioned = append(resolved.Versioned, migration)

			case ResourceTypeUndo:
				migration.Type = MigrationTypeUndoSQL
				migration.IsUndo = true
				if previous, duplicated := seenUndo[migration.Version.CanonicalKey()]; duplicated {
					return fmt.Errorf(
						"found more than one undo migration with version %s\n-> %s\n-> %s",
						migration.Version, previous, path,
					)
				}
				seenUndo[migration.Version.CanonicalKey()] = path
				resolved.Undo = append(resolved.Undo, migration)

			case ResourceTypeRepeatable:
				migration.Type = MigrationTypeSQL
				migration.IsRepeatable = true
				if previous, duplicated := seenRepeatable[migration.Description]; duplicated {
					return fmt.Errorf(
						"found more than one repeatable migration with description %s\n-> %s\n-> %s",
						migration.Description, previous, path,
					)
				}
				seenRepeatable[migration.Description] = path
				resolved.Repeatable = append(resolved.Repeatable, migration)
			}

			return nil
		})

		if err != nil {
			return nil, err
		}
	}

	if nameError != nil {
		return nil, withSeparatorHint(nameError, candidateNames, config.Naming)
	}

	sortByVersion(resolved.Versioned)
	sortByVersion(resolved.Undo)
	sort.SliceStable(resolved.Repeatable, func(i, j int) bool {
		return resolved.Repeatable[i].Description < resolved.Repeatable[j].Description
	})

	return resolved, nil
}

// -----------------------------------------------------------------------------
// withSeparatorHint
//
// Adds a "you probably meant this separator" line to a name error. A project
// that set sqlMigrationSeparator years ago and has since lost the config file
// is otherwise only told that a version number looks wrong, which says nothing
// about the real cause.
//
// The separator is never applied on its own, see DetectSeparator for why.
// -----------------------------------------------------------------------------
func withSeparatorHint(err error, fileNames []string, naming Naming) error {
	separator, count, found := DetectSeparator(fileNames, naming)
	if !found {
		return err
	}

	return fmt.Errorf(
		"%w\n-> %d of the %d migration file(s) found parse with sqlMigrationSeparator=%q rather than the configured %q, add --sqlMigrationSeparator=%s if that is the convention this project uses",
		err, count, len(fileNames), separator, naming.SQLMigrationSeparator, separator,
	)
}

// -----------------------------------------------------------------------------
// LoadSQL
//
// Reads a migration and applies the placeholder replacement.
// -----------------------------------------------------------------------------
func (m *ResolvedMigration) LoadSQL(replacer *PlaceholderReplacer) (string, error) {
	content, err := os.ReadFile(m.PhysicalLocation)
	if err != nil {
		return "", err
	}

	sql := strings.TrimPrefix(string(content), "\ufeff")

	return replacer.Replace(sql)
}

// -----------------------------------------------------------------------------
// sortByVersion
// -----------------------------------------------------------------------------
func sortByVersion(migrations []*ResolvedMigration) {
	sort.SliceStable(migrations, func(i, j int) bool {
		return migrations[i].Version.Compare(migrations[j].Version) < 0
	})
}

// -----------------------------------------------------------------------------
// hasMigrationSuffix
// -----------------------------------------------------------------------------
func hasMigrationSuffix(fileName string, suffixes []string) bool {
	upperName := strings.ToUpper(fileName)

	for _, suffix := range suffixes {
		if suffix != "" && strings.HasSuffix(upperName, strings.ToUpper(suffix)) {
			return true
		}
	}

	return false
}

// -----------------------------------------------------------------------------
// locationPath
//
// Resolves a Flyway location into a directory. Only filesystem locations make
// sense here, classpath ones exist solely for the Java edition. The
// filesystem: prefix is optional: a bare path is read as one.
// -----------------------------------------------------------------------------
func locationPath(location string) (string, error) {
	trimmed := strings.TrimSpace(location)
	if trimmed == "" {
		return "", nil
	}

	if rest, found := strings.CutPrefix(trimmed, "filesystem:"); found {
		return rest, nil
	}
	if strings.HasPrefix(trimmed, "classpath:") {
		return "", fmt.Errorf("classpath locations are not supported by gofly, use filesystem:<dir> instead of %s", trimmed)
	}
	if strings.Contains(trimmed, ":") && !filepath.IsAbs(trimmed) && !strings.HasPrefix(trimmed, ".") {
		return "", fmt.Errorf("unsupported location prefix in %s, use filesystem:<dir> or a plain directory path", trimmed)
	}

	return trimmed, nil
}
