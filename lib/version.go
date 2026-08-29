// Copyright (C) 2026 Pau Sanchez
//
// Flyway-compatible migration version handling.
//
// A version is a list of big integers, obtained by replacing '_' with '.' and
// splitting on every dot that is followed by a digit. Trailing zero parts are
// dropped, so 1.0 == 1 == 1.0.0, exactly like Flyway's MigrationVersion.
package lib

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// VersionKind tells apart real versions from the predefined markers Flyway uses
// for `target` and friends.
type VersionKind int

const (
	VersionKindReal VersionKind = iota
	VersionKindEmpty
	VersionKindLatest
	VersionKindCurrent
	VersionKindNext
)

// Version mirrors org.flywaydb.core.api.MigrationVersion
type Version struct {
	kind VersionKind

	// parts holds the tokenized version, e.g. 1.2.3 -> [1, 2, 3]
	parts []*big.Int

	// displayText is the normalized text ('_' already replaced by '.')
	displayText string

	// rawVersion is the version exactly as it appeared in the file name
	rawVersion string
}

var (
	// VersionEmpty represents an empty schema (no version at all)
	VersionEmpty = &Version{kind: VersionKindEmpty, displayText: "<< Empty Schema >>"}

	// VersionLatest represents the latest available version
	VersionLatest = &Version{kind: VersionKindLatest, displayText: "<< Latest Version >>"}

	// VersionCurrent represents the version currently applied to the database
	VersionCurrent = &Version{kind: VersionKindCurrent, displayText: "<< Current Version >>"}

	// VersionNext represents the next version to be applied
	VersionNext = &Version{kind: VersionKindNext, displayText: "<< Next Version >>"}
)

// -----------------------------------------------------------------------------
// NewVersion
//
// Parses a version string like 6, 6.0, 005, 1.2.3.4, 20100420002 or 1_2_3.
// Returns an error when the version contains anything but digits and separators.
// -----------------------------------------------------------------------------
func NewVersion(version string) (*Version, error) {
	normalized := strings.ReplaceAll(version, "_", ".")

	parts, err := tokenizeVersion(normalized)
	if err != nil {
		return nil, err
	}

	return &Version{
		kind:        VersionKindReal,
		parts:       parts,
		displayText: normalized,
		rawVersion:  version,
	}, nil
}

// -----------------------------------------------------------------------------
// VersionFromString
//
// Same as NewVersion but also understands the predefined markers Flyway accepts
// on the command line: current, next, latest and the empty string.
// -----------------------------------------------------------------------------
func VersionFromString(version string) (*Version, error) {
	switch strings.ToLower(version) {
	case "current":
		return VersionCurrent, nil
	case "next":
		return VersionNext, nil
	case "latest":
		return VersionLatest, nil
	case "":
		return VersionEmpty, nil
	}

	return NewVersion(version)
}

// -----------------------------------------------------------------------------
// tokenizeVersion
//
// Splits on every '.' followed by a digit (Flyway's SPLIT_REGEX) and then drops
// trailing zeroes so that 1.0.0 and 1 compare as equal.
// -----------------------------------------------------------------------------
func tokenizeVersion(version string) ([]*big.Int, error) {
	if version == "" {
		return nil, errors.New("version may only contain 0..9 and . (dot). Invalid version: <empty>")
	}

	// split on '.' only when the next character is a digit
	rawParts := []string{}
	current := strings.Builder{}
	runes := []rune(version)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '.' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9' {
			rawParts = append(rawParts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(runes[i])
	}
	rawParts = append(rawParts, current.String())

	parts := make([]*big.Int, 0, len(rawParts))
	for _, raw := range rawParts {
		value, ok := new(big.Int).SetString(raw, 10)
		if !ok || raw == "" || strings.ContainsAny(raw, "+-") {
			return nil, fmt.Errorf("version may only contain 0..9 and . (dot). Invalid version: %s", version)
		}
		parts = append(parts, value)
	}

	// drop trailing zeroes, but always keep at least the major part
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i].Sign() != 0 {
			break
		}
		parts = parts[:i]
	}

	return parts, nil
}

// -----------------------------------------------------------------------------
// Kind
// -----------------------------------------------------------------------------
func (v *Version) Kind() VersionKind {
	return v.kind
}

// -----------------------------------------------------------------------------
// IsPredefined
// -----------------------------------------------------------------------------
func (v *Version) IsPredefined() bool {
	return v != nil && v.kind != VersionKindReal
}

// -----------------------------------------------------------------------------
// String
//
// Returns the printable representation of the version.
// -----------------------------------------------------------------------------
func (v *Version) String() string {
	if v == nil {
		return ""
	}
	return v.displayText
}

// -----------------------------------------------------------------------------
// RawVersion
//
// Returns the version exactly as written in the migration file name.
// -----------------------------------------------------------------------------
func (v *Version) RawVersion() string {
	if v == nil {
		return ""
	}
	if v.kind != VersionKindReal {
		return v.displayText
	}
	return v.rawVersion
}

// -----------------------------------------------------------------------------
// CanonicalKey
//
// Returns a key that is identical for every spelling of the same version, so
// that 1, 1.0 and 1_0 all collide. Version.String cannot be used for this: it
// keeps the text as written.
// -----------------------------------------------------------------------------
func (v *Version) CanonicalKey() string {
	if v == nil {
		return ""
	}
	if v.kind != VersionKindReal {
		return v.displayText
	}

	parts := make([]string, 0, len(v.parts))
	for _, part := range v.parts {
		parts = append(parts, part.String())
	}

	return strings.Join(parts, ".")
}

// -----------------------------------------------------------------------------
// Compare
//
// Implements the same total ordering as MigrationVersion.compareTo, including
// the placement of the predefined markers.
// -----------------------------------------------------------------------------
func (v *Version) Compare(other *Version) int {
	if other == nil {
		return 1
	}
	if v == nil {
		return -1
	}

	if v.kind == VersionKindEmpty {
		if other.kind == VersionKindEmpty {
			return 0
		}
		return -1
	}
	if v.kind == VersionKindCurrent {
		if other.kind == VersionKindCurrent {
			return 0
		}
		return -1
	}
	if v.kind == VersionKindLatest {
		if other.kind == VersionKindLatest {
			return 0
		}
		return 1
	}

	switch other.kind {
	case VersionKindEmpty, VersionKindCurrent:
		return 1
	case VersionKindNext, VersionKindLatest:
		return -1
	}

	longest := len(v.parts)
	if len(other.parts) > longest {
		longest = len(other.parts)
	}

	for i := 0; i < longest; i++ {
		compared := versionPartOrZero(v.parts, i).Cmp(versionPartOrZero(other.parts, i))
		if compared != 0 {
			return compared
		}
	}

	return 0
}

// -----------------------------------------------------------------------------
// versionPartOrZero
// -----------------------------------------------------------------------------
func versionPartOrZero(parts []*big.Int, index int) *big.Int {
	if index < len(parts) {
		return parts[index]
	}
	return big.NewInt(0)
}

// -----------------------------------------------------------------------------
// Equals
// -----------------------------------------------------------------------------
func (v *Version) Equals(other *Version) bool {
	return v.Compare(other) == 0
}

// -----------------------------------------------------------------------------
// IsNewerThan
// -----------------------------------------------------------------------------
func (v *Version) IsNewerThan(other *Version) bool {
	return v.Compare(other) > 0
}

// -----------------------------------------------------------------------------
// IsAtLeast
// -----------------------------------------------------------------------------
func (v *Version) IsAtLeast(other *Version) bool {
	return v.Compare(other) >= 0
}
