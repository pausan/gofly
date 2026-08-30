// Copyright (C) 2026 Pau Sanchez
//
// Command line parsing and a full run of the binary against a real database.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// TestParseArgsSplitsCommandsFromOptions
// -----------------------------------------------------------------------------
func TestParseArgsSplitsCommandsFromOptions(t *testing.T) {
	withoutDefaultConfigFiles(t)

	commands, config, err := parseArgs([]string{
		"-url=jdbc:sqlite:test.db",
		"-user=root",
		"migrate",
		"info",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Join(commands, ",") != "migrate,info" {
		t.Errorf("commands parsed as %v", commands)
	}
	if config.URL != "jdbc:sqlite:test.db" || config.User != "root" {
		t.Errorf("options parsed as %q / %q", config.URL, config.User)
	}
}

// -----------------------------------------------------------------------------
// TestParseArgsAcceptsFlywayStyleOptions
//
// artypist and ultirent both call Flyway with these, so they have to work
// unchanged.
// -----------------------------------------------------------------------------
func TestParseArgsAcceptsFlywayStyleOptions(t *testing.T) {
	withoutDefaultConfigFiles(t)

	_, config, err := parseArgs([]string{
		"-url=jdbc:postgresql://postgres:5432/ultirent",
		"-user=admin",
		"-connectRetries=10",
		"-locations=filesystem:/db/schema,filesystem:/db/testdata",
		"migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.ConnectRetries != 10 {
		t.Errorf("connectRetries parsed as %d", config.ConnectRetries)
	}
	if len(config.Locations) != 2 {
		t.Errorf("locations parsed as %v", config.Locations)
	}
}

// -----------------------------------------------------------------------------
// TestParseArgsCommandLineBeatsTheConfigFile
// -----------------------------------------------------------------------------
func TestParseArgsCommandLineBeatsTheConfigFile(t *testing.T) {
	withoutDefaultConfigFiles(t)

	path := filepath.Join(t.TempDir(), "gofly.conf")
	content := "flyway.url=jdbc:sqlite:from-file.db\nflyway.user=fromfile\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("cannot write the config file: %v", err)
	}

	_, config, err := parseArgs([]string{
		"-configFiles=" + path,
		"-url=jdbc:sqlite:from-cli.db",
		"info",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.URL != "jdbc:sqlite:from-cli.db" {
		t.Errorf("the command line should win, got %q", config.URL)
	}
	if config.User != "fromfile" {
		t.Errorf("the config file should still provide the rest, got %q", config.User)
	}
}

// -----------------------------------------------------------------------------
// TestParseArgsFlags
// -----------------------------------------------------------------------------
func TestParseArgsFlags(t *testing.T) {
	withoutDefaultConfigFiles(t)

	_, config, err := parseArgs([]string{"-q", "info"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !config.Quiet {
		t.Error("-q should turn on quiet mode")
	}

	if _, _, err := parseArgs([]string{"-url"}); err == nil {
		t.Error("an option without a value should have been rejected")
	}
	if _, _, err := parseArgs([]string{"-notAnOption=1"}); err == nil {
		t.Error("an unknown option should have been rejected")
	}
}

// -----------------------------------------------------------------------------
// TestParseArgsAcceptsDoubleDashOptions
// -----------------------------------------------------------------------------
func TestParseArgsAcceptsDoubleDashOptions(t *testing.T) {
	withoutDefaultConfigFiles(t)

	commands, config, err := parseArgs([]string{
		"--url=jdbc:sqlite:/tmp/x.db",
		"--locations=filesystem:./sql",
		"--placeholders.env=production",
		"--goflyTable=my_history",
		"--q",
		"migrate",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commands) != 1 || commands[0] != "migrate" {
		t.Errorf("commands = %v, want [migrate]", commands)
	}
	if config.URL != "jdbc:sqlite:/tmp/x.db" {
		t.Errorf("url = %q", config.URL)
	}
	if len(config.Locations) != 1 || config.Locations[0] != "filesystem:./sql" {
		t.Errorf("locations = %v", config.Locations)
	}
	if config.Placeholders["env"] != "production" {
		t.Errorf("placeholders = %v", config.Placeholders)
	}
	if config.Table != "my_history" {
		t.Errorf("table = %q, want my_history", config.Table)
	}
	if !config.Quiet {
		t.Error("--q should turn on quiet mode")
	}
}

// -----------------------------------------------------------------------------
// TestParseArgsRejectsMalformedOptionPrefixes
//
// Only one or two dashes make an option, so that a typo is reported instead of
// being read as something else entirely.
// -----------------------------------------------------------------------------
func TestParseArgsRejectsMalformedOptionPrefixes(t *testing.T) {
	withoutDefaultConfigFiles(t)

	for _, arg := range []string{"-", "--", "---url=x", "--=x"} {
		if _, _, err := parseArgs([]string{arg}); err == nil {
			t.Errorf("%q should have been rejected", arg)
		}
	}

	if _, _, err := parseArgs([]string{"--url"}); err == nil {
		t.Error("an option without a value should have been rejected")
	}
	if _, _, err := parseArgs([]string{"--notAnOption=1"}); err == nil {
		t.Error("an unknown option should have been rejected")
	}
}

// -----------------------------------------------------------------------------
// TestStandaloneCommand
// -----------------------------------------------------------------------------
func TestStandaloneCommand(t *testing.T) {
	standalone := []struct {
		args []string
		want string
	}{
		{[]string{}, "help"},
		{[]string{"help"}, "help"},
		{[]string{"-h"}, "help"},
		{[]string{"--help"}, "help"},
		{[]string{"version"}, "version"},
		{[]string{"-v"}, "version"},
		{[]string{"--version"}, "version"},
		{[]string{"--version", "--help"}, "help"},
	}

	for _, test := range standalone {
		got, ok := standaloneCommand(test.args)
		if !ok || got != test.want {
			t.Errorf("standaloneCommand(%v) = %q, %v; want %q, true", test.args, got, ok, test.want)
		}
	}

	for _, args := range [][]string{{"migrate"}, {"--url=x"}, {"---help"}, {"-"}, {"help", "migrate"}} {
		if _, ok := standaloneCommand(args); ok {
			t.Errorf("standaloneCommand(%v) should not have answered on its own", args)
		}
	}
}

// -----------------------------------------------------------------------------
// TestRunMigratesEndToEnd
// -----------------------------------------------------------------------------
func TestRunMigratesEndToEnd(t *testing.T) {
	withoutDefaultConfigFiles(t)

	root := t.TempDir()
	locations := filepath.Join(root, "sql")
	if err := os.MkdirAll(locations, 0o755); err != nil {
		t.Fatalf("cannot create %s: %v", locations, err)
	}

	migration := filepath.Join(locations, "V1__Create_users.sql")
	if err := os.WriteFile(migration, []byte("CREATE TABLE users (id INTEGER);\n"), 0o644); err != nil {
		t.Fatalf("cannot write the migration: %v", err)
	}

	dbPath := filepath.Join(root, "test.db")
	args := []string{
		"-url=jdbc:sqlite:" + dbPath,
		"-locations=filesystem:" + locations,
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := run(append(args, "migrate"), stdout, stderr); code != 0 {
		t.Fatalf("migrate exited with %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Successfully applied 1 migration") {
		t.Errorf("unexpected output:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := run(append(args, "info"), stdout, stderr); code != 0 {
		t.Fatalf("info exited with %d: %s", code, stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"Schema version: 1", "Versioned", "Create users", "Success"} {
		if !strings.Contains(output, expected) {
			t.Errorf("the info output is missing %q:\n%s", expected, output)
		}
	}

	stdout.Reset()
	if code := run(append(args, "validate"), stdout, stderr); code != 0 {
		t.Fatalf("validate exited with %d: %s", code, stderr.String())
	}
}

// -----------------------------------------------------------------------------
// TestRunReportsFailuresWithANonZeroExitCode
// -----------------------------------------------------------------------------
func TestRunReportsFailuresWithANonZeroExitCode(t *testing.T) {
	withoutDefaultConfigFiles(t)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := run([]string{"-url=jdbc:oracle:thin:@//h:1521/s", "migrate"}, stdout, stderr); code == 0 {
		t.Error("an unsupported database should fail")
	}
	if !strings.Contains(stderr.String(), "ERROR:") {
		t.Errorf("the error was not reported: %q", stderr.String())
	}

	stderr.Reset()
	if code := run([]string{"-url=jdbc:sqlite:x.db", "notacommand"}, stdout, stderr); code == 0 {
		t.Error("an unknown command should fail")
	}
}

// -----------------------------------------------------------------------------
// TestRunPrintsUsage
// -----------------------------------------------------------------------------
func TestRunPrintsUsage(t *testing.T) {
	withoutDefaultConfigFiles(t)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := run([]string{}, stdout, stderr); code != 0 {
		t.Errorf("running without arguments exited with %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Error("the usage was not printed")
	}

	stdout.Reset()
	run([]string{"version"}, stdout, stderr)
	if !strings.Contains(stdout.String(), "gofly "+Version) {
		t.Errorf("the version was not printed: %q", stdout.String())
	}
}

// -----------------------------------------------------------------------------
// TestRunAnswersHelpAndVersionWhateverTheEnvironmentSays
//
// FLYWAY_DIR is a real Flyway property that gofly does not implement, so it is
// enough to make any other command fail. Asking for help has to work anyway.
// -----------------------------------------------------------------------------
func TestRunAnswersHelpAndVersionWhateverTheEnvironmentSays(t *testing.T) {
	withoutDefaultConfigFiles(t)
	t.Setenv("FLYWAY_DIR", "/some/place")

	if code := run([]string{"info"}, &bytes.Buffer{}, &bytes.Buffer{}); code == 0 {
		t.Error("an unknown environment property should still fail a real command")
	}

	for _, args := range [][]string{{}, {"help"}, {"--help"}, {"-h"}, {"-?"}} {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		if code := run(args, stdout, stderr); code != 0 {
			t.Errorf("gofly %v exited with %d: %s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage") {
			t.Errorf("gofly %v did not print the usage: %q", args, stdout.String())
		}
	}

	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		if code := run(args, stdout, stderr); code != 0 {
			t.Errorf("gofly %v exited with %d: %s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "gofly "+Version) {
			t.Errorf("gofly %v did not print the version: %q", args, stdout.String())
		}
	}
}

// -----------------------------------------------------------------------------
// withoutDefaultConfigFiles
//
// Runs the test from an empty directory and an empty home, so that a gofly.conf
// or flyway.conf lying around cannot change the outcome.
// -----------------------------------------------------------------------------
func withoutDefaultConfigFiles(t *testing.T) {
	t.Helper()

	empty := t.TempDir()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot read the working directory: %v", err)
	}
	if err := os.Chdir(empty); err != nil {
		t.Fatalf("cannot enter %s: %v", empty, err)
	}
	t.Cleanup(func() { os.Chdir(previous) })

	t.Setenv("HOME", empty)
}
