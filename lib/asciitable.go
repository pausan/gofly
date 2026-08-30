// Copyright (C) 2026 Pau Sanchez
//
// The ascii table gofly prints for `info`, laid out like Flyway's.
package lib

import (
	"strconv"
	"strings"
	"time"
)

// -----------------------------------------------------------------------------
// DumpInfoTable
//
// Renders the migrations as the table the `info` command prints. Undo
// migrations are not listed on their own, they show up as the Undoable column
// of the migration they undo.
// -----------------------------------------------------------------------------
func DumpInfoTable(service *MigrationInfoService) string {
	undoableVersions := map[string]bool{}
	for _, info := range service.Infos {
		if info.Type().IsUndo() && info.Version() != nil {
			undoableVersions[info.Version().String()] = true
		}
	}

	columns := []string{"Category", "Version", "Description", "Type", "Installed On", "State", "Undoable"}
	rows := [][]string{}

	for _, info := range service.Infos {
		if info.Type().IsUndo() {
			continue
		}

		rows = append(rows, []string{
			infoCategory(info),
			versionText(info.Version()),
			info.Description(),
			string(info.Type()),
			installedOnText(info),
			string(info.State),
			undoableText(info, undoableVersions),
		})
	}

	return renderAsciiTable(columns, rows, "No migrations found")
}

// -----------------------------------------------------------------------------
// infoCategory
// -----------------------------------------------------------------------------
func infoCategory(info *MigrationInfo) string {
	if info.Type().IsSynthetic() && !info.Type().IsBaseline() {
		return ""
	}
	if info.Version() == nil {
		return "Repeatable"
	}
	if info.Type().IsBaseline() {
		return "Baseline"
	}

	return "Versioned"
}

// -----------------------------------------------------------------------------
// installedOnText
// -----------------------------------------------------------------------------
func installedOnText(info *MigrationInfo) string {
	if info.Applied == nil || info.Applied.InstalledOn.IsZero() {
		return ""
	}

	return info.Applied.InstalledOn.Format(time.RFC3339)
}

// -----------------------------------------------------------------------------
// undoableText
// -----------------------------------------------------------------------------
func undoableText(info *MigrationInfo, undoableVersions map[string]bool) string {
	version := info.Version()
	if version == nil || info.Type() == MigrationTypeDelete ||
		info.State == StateDeleted || info.State == StateUndone {
		return ""
	}

	if !info.State.IsFailed() && undoableVersions[version.String()] {
		return "Yes"
	}

	return "No"
}

// -----------------------------------------------------------------------------
// renderAsciiTable
// -----------------------------------------------------------------------------
func renderAsciiTable(columns []string, rows [][]string, emptyMessage string) string {
	widths := make([]int, len(columns))
	for index, column := range columns {
		widths[index] = len([]rune(column))
	}
	for _, row := range rows {
		for index, cell := range row {
			if width := len([]rune(cell)); width > widths[index] {
				widths[index] = width
			}
		}
	}

	separator := strings.Builder{}
	separator.WriteString("+")
	for _, width := range widths {
		separator.WriteString(strings.Repeat("-", width+2) + "+")
	}

	output := strings.Builder{}
	output.WriteString(separator.String() + "\n")
	output.WriteString(renderRow(columns, widths) + "\n")
	output.WriteString(separator.String() + "\n")

	if len(rows) == 0 {
		total := 0
		for _, width := range widths {
			total += width + 3
		}
		total -= 3
		output.WriteString("| " + padRight(emptyMessage, total) + " |\n")
	}

	for _, row := range rows {
		output.WriteString(renderRow(row, widths) + "\n")
	}
	output.WriteString(separator.String() + "\n")

	return output.String()
}

// -----------------------------------------------------------------------------
// renderRow
// -----------------------------------------------------------------------------
func renderRow(cells []string, widths []int) string {
	row := strings.Builder{}
	row.WriteString("|")

	for index, width := range widths {
		cell := ""
		if index < len(cells) {
			cell = cells[index]
		}
		row.WriteString(" " + padRight(cell, width) + " |")
	}

	return row.String()
}

// -----------------------------------------------------------------------------
// padRight
// -----------------------------------------------------------------------------
func padRight(text string, width int) string {
	padding := width - len([]rune(text))
	if padding <= 0 {
		return text
	}

	return text + strings.Repeat(" ", padding)
}

// -----------------------------------------------------------------------------
// DumpValidationTable
//
// Renders what validation actually compared: the file found on disk, the row it
// was paired with in the schema history, and the checksums of both. This is
// what `validate --verbose` prints, so that a mismatch can be traced back to a
// concrete file without guessing.
// -----------------------------------------------------------------------------
func DumpValidationTable(service *MigrationInfoService) string {
	columns := []string{"Category", "Version", "Description", "Local file", "History script", "Checksum (local/db)", "State"}
	rows := [][]string{}

	for _, info := range service.Infos {
		rows = append(rows, []string{
			validationCategory(info),
			versionText(info.Version()),
			info.Description(),
			localFileText(info),
			historyScriptText(info),
			checksumPairText(info),
			string(info.State),
		})
	}

	return renderAsciiTable(columns, rows, "No migrations found")
}

// -----------------------------------------------------------------------------
// validationCategory
//
// Same as the info table, except that undo migrations are listed on their own
// here since they are validated on their own too.
// -----------------------------------------------------------------------------
func validationCategory(info *MigrationInfo) string {
	if info.Type().IsUndo() {
		return "Undo"
	}

	return infoCategory(info)
}

// -----------------------------------------------------------------------------
// localFileText
// -----------------------------------------------------------------------------
func localFileText(info *MigrationInfo) string {
	if info.Resolved == nil {
		return "<not on disk>"
	}

	return info.Resolved.PhysicalLocation
}

// -----------------------------------------------------------------------------
// historyScriptText
// -----------------------------------------------------------------------------
func historyScriptText(info *MigrationInfo) string {
	if info.Applied == nil {
		return "<not in history>"
	}

	return info.Applied.Script
}

// -----------------------------------------------------------------------------
// checksumPairText
//
// The local checksum next to the one recorded in the history table, which is
// the comparison a CHECKSUM_MISMATCH complains about.
// -----------------------------------------------------------------------------
func checksumPairText(info *MigrationInfo) string {
	local := "-"
	if info.Resolved != nil {
		local = strconv.FormatInt(int64(info.Resolved.Checksum), 10)
	}

	applied := "-"
	if info.Applied != nil && info.Applied.Checksum != nil {
		applied = strconv.FormatInt(int64(*info.Applied.Checksum), 10)
	}

	return local + " / " + applied
}
