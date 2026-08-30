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
const Version = "0.1.3"

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
	// answered before anything is parsed so that a broken environment or a
	// broken config file can never get in the way of asking for help
	if command, ok := standaloneCommand(args); ok {
		printHelpOrVersion(command, stdout)
		return 0
	}

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
			printVersion(stdout)
			return 0
		}
	}

	if !config.Quiet {
		fmt.Fprintf(stdout, "gofly %s\n\n", Version)
	}

	for _, warning := range config.Warnings {
		fmt.Fprintf(stderr, "WARNING: %s\n", warning)
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
// standaloneCommand
//
// Returns "help" or "version" when the command line asks for nothing else, an
// empty command line included. Both answers come from the binary alone, so they
// are served without reading the environment or the config files.
// -----------------------------------------------------------------------------
func standaloneCommand(args []string) (string, bool) {
	if len(args) == 0 {
		return "help", true
	}

	command := ""

	for _, arg := range args {
		name, isOption := trimOptionPrefix(arg)
		if !isOption {
			if strings.HasPrefix(arg, "-") {
				return "", false
			}
			name = arg
		}

		switch strings.ToLower(name) {
		case "help", "h", "?":
			command = "help"
		case "version", "v":
			if command == "" {
				command = "version"
			}
		default:
			return "", false
		}
	}

	return command, true
}

// -----------------------------------------------------------------------------
// printHelpOrVersion
// -----------------------------------------------------------------------------
func printHelpOrVersion(command string, stdout io.Writer) {
	if command == "version" {
		printVersion(stdout)
		return
	}

	printUsage(stdout)
}

// -----------------------------------------------------------------------------
// printVersion
// -----------------------------------------------------------------------------
func printVersion(out io.Writer) {
	// the databases are listed because the release also ships single database
	// builds, which are otherwise hard to tell apart
	fmt.Fprintf(out, "gofly %s\n", Version)
	fmt.Fprintf(out, "databases: %s\n", strings.Join(lib.CompiledInDialects(), ", "))
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
		info, source, err := gofly.InfoWithSource()
		if err != nil {
			return err
		}
		if source == lib.HistorySourceFlyway {
			fmt.Fprintf(stdout, "Reading the existing %s: gofly has not taken over this database yet\n\n",
				gofly.Config.FlywayTable)
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
// first, options are --key=value, and the single dash Flyway uses is accepted
// throughout. --configFiles is honoured before anything else so that the
// command line always wins over the files.
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

		name, isOption := trimOptionPrefix(arg)
		if !isOption {
			return nil, nil, fmt.Errorf("expected -key=value or --key=value, got %q", arg)
		}

		key, value, found := strings.Cut(name, "=")
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
				return nil, nil, fmt.Errorf("expected %s=<value>", arg)
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
// trimOptionPrefix
//
// Strips the leading dashes of an option and says whether the argument looked
// like one at all. Both the Flyway style -key=value and the usual --key=value
// are accepted; a lone dash, or a third one, is neither.
// -----------------------------------------------------------------------------
func trimOptionPrefix(arg string) (string, bool) {
	name, found := strings.CutPrefix(arg, "--")
	if !found {
		if name, found = strings.CutPrefix(arg, "-"); !found {
			return "", false
		}
	}

	if name == "" || strings.HasPrefix(name, "-") {
		return "", false
	}

	return name, true
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

  Options are --key=value. The single dash Flyway uses works too, so an existing
  Flyway command line runs unchanged:

    gofly migrate --url=sqlite:./app.db --locations=filesystem:./sql
    gofly migrate  -url=sqlite:./app.db  -locations=filesystem:./sql   (Flyway style)

Commands
  migrate     Apply every pending migration
  undo        Undo the most recently applied versioned migration
  info        Print the status of every migration
  validate    Check the applied migrations against the ones on disk
  baseline    Mark an existing database as migrated up to baselineVersion
  repair      Fix the schema history: drop failed rows and realign checksums
  clean       Not implemented on purpose, see the README

Connection
  --url=...                 Database url, see the examples below
  --user=...                Database user
  --password=...            Database password (--pass also works)
  --connectRetries=0        Retries, one second apart, before giving up

Database urls
  The jdbc: prefix Flyway needs is optional, mysql://... and jdbc:mysql://...
  are read exactly the same way.

  postgresql://host:5432/mydb              (postgres:// and pg:// too)
  mysql://host:3306/mydb
  mariadb://host:3306/mydb
  sqlserver://host:1433;databaseName=mydb
  sqlite:/path/to.db                       (file: too)

Examples
  gofly info --url=mysql://localhost:3306/mydb --user=root --pass=secret
  gofly migrate --url=postgresql://db:5432/mydb --user=admin --pass=secret \
        --locations=filesystem:./db/schema
  gofly migrate --configFiles=flyway.conf
  gofly validate info --url=sqlite:./local.db

Migrations
  --locations=...           Comma separated filesystem:<dir> locations
  --sqlMigrationPrefix=V    Prefix of the versioned migrations
  --undoSqlMigrationPrefix=U
  --repeatableSqlMigrationPrefix=R
  --sqlMigrationSeparator=__
  --sqlMigrationSuffixes=.sql
  --encoding=UTF-8

Behaviour
  --target=...              Stop at this version (or latest, current, next)
  --group=false             Run every pending migration in one transaction
  --outOfOrder=false        Apply migrations older than the current version
  --validateOnMigrate=true  Validate before migrating
  --baselineVersion=1
  --baselineDescription="<< Flyway Baseline >>"
  --baselineOnMigrate=false
  --ignoreMissingMigrations=false
  --ignoreFutureMigrations=true
  --installedBy=...
  --placeholders.name=value

Schema history
  --goflyTable=gofly_schema_history  Name of the gofly history table (--table too)
  --goflySchema=gofly                Schema holding it (PostgreSQL and SQL Server)
  --defaultSchema=...                Schema the migrations run against
  --flywayTable=flyway_schema_history
  --importFromFlyway=true            Import an existing Flyway history on first run

Other
  --configFiles=a.conf,b.conf    Flyway style properties files
  --quiet                        Quiet (-q for short)
  --verbose                      Verbose (-X for short)

Every option can also be given as gofly.<name> in a config file, or as
GOFLY_<NAME> in the environment; the flyway.<name> and FLYWAY_<NAME> spellings
still work and warn.
`, Version)
}
