// Copyright (C) 2026 Pau Sanchez
//
// Configuration, gathered from the same places Flyway looks at and with the
// same precedence: built in defaults, then config files, then environment
// variables, then the command line.
package lib

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// DefaultGoflySchema is where gofly keeps its own history table on the
	// databases that have real schemas
	DefaultGoflySchema = "gofly"

	// DefaultGoflyTable is the gofly history table name
	DefaultGoflyTable = "gofly_schema_history"

	// FlywayTable is the table gofly imports from on a first run
	FlywayTable = "flyway_schema_history"
)

// Config holds every setting that drives a gofly run
type Config struct {
	// connection
	URL            string
	User           string
	Password       string
	ConnectRetries int

	// where the history lives
	Schemas       []string
	DefaultSchema string
	GoflySchema   string
	Table         string

	// importing an existing Flyway history
	FlywayTable      string
	ImportFromFlyway bool

	// migration discovery
	Locations []string
	Encoding  string
	Naming

	// behaviour
	BaselineVersion         string
	BaselineDescr           string
	BaselineOnMigrate       bool
	ValidateOnMigrate       bool
	CleanDisabled           bool
	OutOfOrder              bool
	Target                  string
	Group                   bool
	Mixed                   bool
	InstalledBy             string
	IgnoreMissing           bool
	IgnoreFuture            bool
	SkipExecutingMigrations bool

	// placeholders
	PlaceholderReplacement bool
	Placeholders           map[string]string
	PlaceholderPrefix      string
	PlaceholderSuffix      string

	// output
	Quiet   bool
	Verbose bool
}

// -----------------------------------------------------------------------------
// NewConfig
//
// Returns a configuration preloaded with Flyway's defaults.
// -----------------------------------------------------------------------------
func NewConfig() *Config {
	return &Config{
		ConnectRetries:         0,
		GoflySchema:            "",
		Table:                  DefaultGoflyTable,
		FlywayTable:            FlywayTable,
		ImportFromFlyway:       true,
		Locations:              []string{"filesystem:sql"},
		Encoding:               "UTF-8",
		Naming:                 DefaultNaming(),
		BaselineVersion:        "1",
		BaselineDescr:          "<< Flyway Baseline >>",
		ValidateOnMigrate:      true,
		CleanDisabled:          true,
		IgnoreFuture:           true,
		PlaceholderReplacement: true,
		Placeholders:           map[string]string{},
		PlaceholderPrefix:      "${",
		PlaceholderSuffix:      "}",
	}
}

// -----------------------------------------------------------------------------
// Set
//
// Applies a single `key=value` setting. Keys may be given bare (url), with the
// flyway prefix (flyway.url) or with the gofly one (gofly.url), so existing
// Flyway config files keep working untouched.
// -----------------------------------------------------------------------------
func (c *Config) Set(key string, value string) error {
	key = strings.TrimSpace(key)
	key = strings.TrimPrefix(key, "flyway.")
	key = strings.TrimPrefix(key, "gofly.")

	if rest, found := strings.CutPrefix(key, "placeholders."); found {
		c.Placeholders[rest] = value
		return nil
	}

	switch strings.ToLower(key) {
	case "url":
		c.URL = value
	case "user":
		c.User = value
	case "password":
		c.Password = value
	case "connectretries":
		return setInt(&c.ConnectRetries, key, value)
	case "schemas":
		c.Schemas = splitList(value)
	case "defaultschema":
		c.DefaultSchema = value
	case "goflyschema":
		c.GoflySchema = value
	case "table":
		c.Table = value
	case "flywaytable":
		c.FlywayTable = value
	case "importfromflyway":
		return setBool(&c.ImportFromFlyway, key, value)
	case "locations":
		c.Locations = splitList(value)
	case "encoding":
		c.Encoding = value
	case "sqlmigrationprefix":
		c.SQLMigrationPrefix = value
	case "undosqlmigrationprefix":
		c.UndoSQLMigrationPrefix = value
	case "repeatablesqlmigrationprefix":
		c.RepeatableSQLMigrationPrefix = value
	case "sqlmigrationseparator":
		c.SQLMigrationSeparator = value
	case "sqlmigrationsuffixes":
		c.SQLMigrationSuffixes = splitList(value)
	case "baselineversion":
		c.BaselineVersion = value
	case "baselinedescription":
		c.BaselineDescr = value
	case "baselineonmigrate":
		return setBool(&c.BaselineOnMigrate, key, value)
	case "validateonmigrate":
		return setBool(&c.ValidateOnMigrate, key, value)
	case "cleandisabled":
		return setBool(&c.CleanDisabled, key, value)
	case "outoforder":
		return setBool(&c.OutOfOrder, key, value)
	case "target":
		c.Target = value
	case "group":
		return setBool(&c.Group, key, value)
	case "mixed":
		return setBool(&c.Mixed, key, value)
	case "installedby":
		c.InstalledBy = value
	case "ignoremissingmigrations":
		return setBool(&c.IgnoreMissing, key, value)
	case "ignorefuturemigrations":
		return setBool(&c.IgnoreFuture, key, value)
	case "skipexecutingmigrations":
		return setBool(&c.SkipExecutingMigrations, key, value)
	case "placeholderreplacement":
		return setBool(&c.PlaceholderReplacement, key, value)
	case "placeholderprefix":
		c.PlaceholderPrefix = value
	case "placeholdersuffix":
		c.PlaceholderSuffix = value

	// accepted and ignored, they only make sense for the Java edition
	case "driver", "jarDirs", "resolvers", "callbacks", "skipdefaultresolvers",
		"skipdefaultcallbacks", "cleanonvalidationerror", "configfiles", "errorhandlers",
		"dryrunoutput":
		return nil

	default:
		return fmt.Errorf("unknown configuration property: %s", key)
	}

	return nil
}

// -----------------------------------------------------------------------------
// LoadConfigFile
//
// Reads a Flyway style properties file. Blank lines and lines starting with #
// are skipped.
// -----------------------------------------------------------------------------
func (c *Config) LoadConfigFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++

		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("%s:%d: expected key=value, got %q", path, lineNumber, line)
		}

		if err := c.Set(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
	}

	return scanner.Err()
}

// -----------------------------------------------------------------------------
// LoadEnvironment
//
// Applies FLYWAY_* and GOFLY_* environment variables. FLYWAY_SQL_MIGRATION_PREFIX
// maps onto sqlMigrationPrefix, and FLYWAY_PLACEHOLDERS_MY_KEY onto the
// placeholder my_key, exactly like the Flyway command line.
// -----------------------------------------------------------------------------
func (c *Config) LoadEnvironment(environment []string) error {
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}

		var suffix string
		switch {
		case strings.HasPrefix(name, "FLYWAY_"):
			suffix = strings.TrimPrefix(name, "FLYWAY_")
		case strings.HasPrefix(name, "GOFLY_"):
			suffix = strings.TrimPrefix(name, "GOFLY_")
		default:
			continue
		}

		key, ok := environmentKeyToProperty(suffix)
		if !ok {
			continue
		}

		if err := c.Set(key, value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// environmentKeyToProperty
//
// Turns SQL_MIGRATION_PREFIX into sqlMigrationPrefix and PLACEHOLDERS_FOO into
// placeholders.FOO. Unknown names are reported so the caller can ignore them.
// -----------------------------------------------------------------------------
func environmentKeyToProperty(suffix string) (string, bool) {
	if suffix == "" {
		return "", false
	}

	if rest, found := strings.CutPrefix(suffix, "PLACEHOLDERS_"); found {
		if rest == "" {
			return "", false
		}
		return "placeholders." + strings.ToLower(rest), true
	}

	words := strings.Split(strings.ToLower(suffix), "_")
	property := strings.Builder{}
	for index, word := range words {
		if word == "" {
			continue
		}
		if index == 0 {
			property.WriteString(word)
			continue
		}
		property.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}

	return property.String(), true
}

// -----------------------------------------------------------------------------
// DefaultConfigFiles
//
// Returns the config files Flyway loads implicitly: the one next to the
// executable, the one in the user's home and the one in the working directory.
// Missing files are simply not returned.
// -----------------------------------------------------------------------------
func DefaultConfigFiles() []string {
	candidates := []string{}

	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "conf", "gofly.conf"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "gofly.conf"))
		candidates = append(candidates, filepath.Join(home, "flyway.conf"))
	}
	candidates = append(candidates, "gofly.conf", "flyway.conf")

	existing := []string{}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			existing = append(existing, candidate)
		}
	}

	return existing
}

// -----------------------------------------------------------------------------
// ResolveInstalledBy
//
// Returns the name to record in the installed_by column.
// -----------------------------------------------------------------------------
func (c *Config) ResolveInstalledBy() string {
	if c.InstalledBy != "" {
		return c.InstalledBy
	}
	if c.User != "" {
		return c.User
	}
	if current, err := user.Current(); err == nil {
		return current.Username
	}

	return "gofly"
}

// -----------------------------------------------------------------------------
// TargetVersion
//
// Parses the configured target, defaulting to "latest".
// -----------------------------------------------------------------------------
func (c *Config) TargetVersion() (*Version, error) {
	if c.Target == "" {
		return VersionLatest, nil
	}

	return VersionFromString(c.Target)
}

// -----------------------------------------------------------------------------
// NewPlaceholderReplacer
// -----------------------------------------------------------------------------
func (c *Config) NewPlaceholderReplacer() *PlaceholderReplacer {
	return &PlaceholderReplacer{
		Enabled: c.PlaceholderReplacement,
		Prefix:  c.PlaceholderPrefix,
		Suffix:  c.PlaceholderSuffix,
		Values:  c.Placeholders,
	}
}

// -----------------------------------------------------------------------------
// splitList
// -----------------------------------------------------------------------------
func splitList(value string) []string {
	items := []string{}

	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}

	return items
}

// -----------------------------------------------------------------------------
// setBool
// -----------------------------------------------------------------------------
func setBool(target *bool, key string, value string) error {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s expects true or false, got %q", key, value)
	}

	*target = parsed

	return nil
}

// -----------------------------------------------------------------------------
// setInt
// -----------------------------------------------------------------------------
func setInt(target *int, key string, value string) error {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s expects a number, got %q", key, value)
	}

	*target = parsed

	return nil
}
