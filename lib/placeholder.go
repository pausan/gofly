// Copyright (C) 2026 Pau Sanchez
//
// Placeholder replacement, compatible with Flyway's PlaceholderReplacer.
package lib

import (
	"fmt"
	"sort"
	"strings"
)

// PlaceholderReplacer substitutes ${name} style placeholders in a migration
type PlaceholderReplacer struct {
	Enabled      bool
	Prefix       string
	Suffix       string
	Values       map[string]string
	ErrorOnUnset bool
}

// -----------------------------------------------------------------------------
// NewPlaceholderReplacer
// -----------------------------------------------------------------------------
func NewPlaceholderReplacer(values map[string]string) *PlaceholderReplacer {
	if values == nil {
		values = map[string]string{}
	}

	return &PlaceholderReplacer{
		Enabled: true,
		Prefix:  "${",
		Suffix:  "}",
		Values:  values,
	}
}

// -----------------------------------------------------------------------------
// Replace
//
// Substitutes every known placeholder. Longer names are replaced first so that
// ${db} never eats the beginning of ${db_name}.
// -----------------------------------------------------------------------------
func (r *PlaceholderReplacer) Replace(sql string) (string, error) {
	if r == nil || !r.Enabled || len(r.Values) == 0 {
		return sql, r.checkUnset(sql)
	}

	names := make([]string, 0, len(r.Values))
	for name := range r.Values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})

	for _, name := range names {
		sql = strings.ReplaceAll(sql, r.Prefix+name+r.Suffix, r.Values[name])
	}

	return sql, r.checkUnset(sql)
}

// -----------------------------------------------------------------------------
// checkUnset
//
// Reports the first placeholder left behind when erroring on unset ones is
// enabled (Flyway's errorOnMissingPlaceholders).
// -----------------------------------------------------------------------------
func (r *PlaceholderReplacer) checkUnset(sql string) error {
	if r == nil || !r.ErrorOnUnset || !r.Enabled {
		return nil
	}

	start := strings.Index(sql, r.Prefix)
	if start < 0 {
		return nil
	}

	rest := sql[start+len(r.Prefix):]
	end := strings.Index(rest, r.Suffix)
	if end < 0 {
		return nil
	}

	return fmt.Errorf("no value provided for placeholder: %s%s%s", r.Prefix, rest[:end], r.Suffix)
}
