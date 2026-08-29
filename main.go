// Copyright (C) 2026 Pau Sanchez
//
// gofly, a small single binary reimplementation of the essential Flyway
// commands, checksum compatible with Flyway itself.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pausan/gofly/lib"
)

// Version of gofly itself
const Version = "0.1.0"

// -----------------------------------------------------------------------------
// main
// -----------------------------------------------------------------------------
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// -----------------------------------------------------------------------------
// run
//
// Parses the command line, runs the requested command and returns the exit
// code. Kept apart from main so that the tests can drive it.
// -----------------------------------------------------------------------------
func run(args []string, stdout io.Writer, stderr io.Writer) int {
	commands, config, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %s\n", err)
		return 1
	}

	if len(commands) == 0 {
		printUsage(stdout)
		return 0
	}

	for _, command := range commands {
		switch strings.ToLower(command) {
		case "help", "-help", "--help", "-h", "-?":
			printUsage(stdout)
			return 0
		case "version", "-v", "--version":
			fmt.Fprintf(stdout, "gofly %s\n", Version)
			return 0
		}
	}

	if !config.Quiet {
		fmt.Fprintf(stdout, "gofly %s\n\n", Version)
	}

	gofly, err := lib.New(config)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %s\n", err)
		return 1
	}
	defer gofly.Close()

	gofly.Output = stdout

	for _, command := range commands {
		if err := runCommand(gofly, strings.ToLower(command), stdout); err != nil {
			fmt.Fprintf(stderr, "ERROR: %s\n", err)
			return 1
		}
	}

	return 0
}

// -----------------------------------------------------------------------------
// runCommand
// -----------------------------------------------------------------------------
func runCommand(gofly *lib.Gofly, command string, stdout io.Writer) error {
	switch command {
	case "migrate":
		_, err := gofly.Migrate()
		return err

	case "undo":
		_, err := gofly.Undo()
		return err

	case "info":
		info, err := gofly.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Schema version: %s\n\n", info.Current)
		fmt.Fprint(stdout, lib.DumpInfoTable(info))
		return nil

	case "validate":
		result, err := gofly.Validate()
		if err != nil {
			return err
		}
		if !result.Valid() {
			return result.Error()
		}
		fmt.Fprintf(stdout, "Successfully validated %d migration(s)\n", result.ValidationCount)
		return nil

	case "baseline":
		return gofly.Baseline()

	case "repair":
		_, err := gofly.Repair()
		return err

	case "clean":
		return gofly.Clean()
	}

	return fmt.Errorf("unknown command: %s (try migrate, undo, info, validate, baseline, repair)", command)
}

// -----------------------------------------------------------------------------
// parseArgs
//
// Reads the command line the way the Flyway command line does: commands come
// first, options are -key=value, and -configFiles is honoured before anything
// else so that the command line always wins over the files.
// -----------------------------------------------------------------------------
func parseArgs(args []string) ([]string, *lib.Config, error) {
	config := lib.NewConfig()

	commands := []string{}
	options := []string{}
	configFiles := []string{}

	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			commands = append(commands, arg)
			continue
		}

		key, value, found := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !found {
			switch strings.ToLower(key) {
			case "q", "quiet":
				config.Quiet = true
			case "x", "verbose":
				config.Verbose = true
			case "help", "h", "?":
				commands = append(commands, "help")
			case "v", "version":
				commands = append(commands, "version")
			default:
				return nil, nil, fmt.Errorf("expected -%s=<value>", key)
			}
			continue
		}

		if strings.EqualFold(key, "configFiles") {
			configFiles = append(configFiles, splitCommaList(value)...)
			continue
		}

		options = append(options, key+"="+value)
	}

	if len(configFiles) == 0 {
		configFiles = lib.DefaultConfigFiles()
	}
	for _, path := range configFiles {
		if err := config.LoadConfigFile(path); err != nil {
			return nil, nil, err
		}
	}

	if err := config.LoadEnvironment(os.Environ()); err != nil {
		return nil, nil, err
	}

	for _, option := range options {
		key, value, _ := strings.Cut(option, "=")
		if err := config.Set(key, value); err != nil {
			return nil, nil, err
		}
	}

	return commands, config, nil
}

// -----------------------------------------------------------------------------
// splitCommaList
// -----------------------------------------------------------------------------
func splitCommaList(value string) []string {
	items := []string{}

	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}

	return items
}

// -----------------------------------------------------------------------------
// printUsage
// -----------------------------------------------------------------------------
func printUsage(out io.Writer) {
	fmt.Fprintf(out, `gofly %s - a small, single binary Flyway clone

Usage
  gofly [options] command [command ...]

Commands
  migrate     Apply every pending migration
  undo        Undo the most recently applied versioned migration
  info        Print the status of every migration
  validate    Check the applied migrations against the ones on disk
  baseline    Mark an existing database as migrated up to baselineVersion
  repair      Fix the schema history: drop failed rows and realign checksums
  clean       Not implemented on purpose, see the README

Connection
  -url=...                 jdbc:postgresql://host:port/db, jdbc:mysql://...,
                           jdbc:sqlserver://host:port;databaseName=db,
                           jdbc:sqlite:/path/to.db
  -user=...                Database user
  -password=...            Database password
  -connectRetries=0        Retries, one second apart, before giving up

Migrations
  -locations=...           Comma separated filesystem:<dir> locations
  -sqlMigrationPrefix=V    Prefix of the versioned migrations
  -undoSqlMigrationPrefix=U
  -repeatableSqlMigrationPrefix=R
  -sqlMigrationSeparator=__
  -sqlMigrationSuffixes=.sql
  -encoding=UTF-8

Behaviour
  -target=...              Stop at this version (or latest, current, next)
  -group=false             Run every pending migration in one transaction
  -outOfOrder=false        Apply migrations older than the current version
  -validateOnMigrate=true  Validate before migrating
  -baselineVersion=1
  -baselineDescription="<< Flyway Baseline >>"
  -baselineOnMigrate=false
  -ignoreMissingMigrations=false
  -ignoreFutureMigrations=true
  -installedBy=...
  -placeholders.name=value

Schema history
  -table=gofly_schema_history   Name of the gofly history table
  -goflySchema=gofly            Schema holding it (PostgreSQL and SQL Server)
  -defaultSchema=...            Schema the migrations run against
  -flywayTable=flyway_schema_history
  -importFromFlyway=true        Import an existing Flyway history on first run

Other
  -configFiles=a.conf,b.conf    Flyway style properties files
  -q                            Quiet
  -X                            Verbose

Every option can also be given as flyway.<name> in a config file, or as
FLYWAY_<NAME> / GOFLY_<NAME> in the environment.
`, Version)
}
