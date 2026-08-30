// Copyright (C) 2026 Pau Sanchez
//
// Configuration, gathered from the same places Flyway looks at and with the
// same precedence: built in defaults, then config files, then environment
// variables, then the command line.
package lib

import (
	"bufio"
	"errors"
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

	// Warnings collects the deprecation notices raised while reading the
	// configuration, for the caller to print
	Warnings []string

	// namespaceOrigin remembers where the first flyway.* and the first gofly.*
	// property came from, so that mixing them can be reported precisely
	namespaceOrigin map[string]namespaceUse

	// warnedOrigins keeps the deprecation warning down to one per source
	warnedOrigins map[string]bool

	// warnedMixing keeps the namespace mixing warning down to one per run,
	// since every later property in the losing namespace would repeat it
	warnedMixing bool
}

// Origin says where a setting came from. Source identifies the file or channel
// as a whole, so a deprecation warns once for a config file rather than once per
// line, while Location pins down the exact spot for the error messages.
type Origin struct {
	Source   string
	Location string
}

// -----------------------------------------------------------------------------
// FileOrigin
// -----------------------------------------------------------------------------
func FileOrigin(path string, line int) Origin {
	return Origin{Source: path, Location: fmt.Sprintf("%s:%d", path, line)}
}

// -----------------------------------------------------------------------------
// EnvironmentOrigin
// -----------------------------------------------------------------------------
func EnvironmentOrigin(name string) Origin {
	return Origin{Source: "the environment", Location: "the environment variable " + name}
}

// -----------------------------------------------------------------------------
// CommandLineOrigin
// -----------------------------------------------------------------------------
func CommandLineOrigin() Origin {
	return Origin{Source: "the command line", Location: "the command line"}
}

// namespaceUse records the first property seen in a namespace and where it came
// from, which is all the mixing error needs to be actionable
type namespaceUse struct {
	property string
	location string
}

// ErrUnknownProperty is what SetFrom returns when the property name matches
// nothing gofly understands. A config file or the command line names a property
// on purpose, so there a typo stays fatal; the environment is shared with the
// rest of the machine and is checked against this, since plenty of variables
// merely start with FLYWAY_ without being settings.
var ErrUnknownProperty = errors.New("unknown configuration property")

// Property namespaces a setting may be written in
const (
	// NamespaceFlyway is the deprecated flyway.* / FLYWAY_* namespace
	NamespaceFlyway = "flyway"

	// NamespaceGofly is the gofly.* / GOFLY_* namespace
	NamespaceGofly = "gofly"

	// namespaceNone is a bare property name, which belongs to neither and is
	// what the command line uses
	namespaceNone = ""
)

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
		namespaceOrigin:        map[string]namespaceUse{},
		warnedOrigins:          map[string]bool{},
	}
}

// -----------------------------------------------------------------------------
// Set
//
// Applies a single `key=value` setting coming from the command line, where
// property names are always bare.
// -----------------------------------------------------------------------------
func (c *Config) Set(key string, value string) error {
	return c.SetFrom(key, value, CommandLineOrigin())
}

// -----------------------------------------------------------------------------
// SetFrom
//
// Applies a single `key=value` setting, remembering where it came from so that
// deprecations and namespace clashes can be reported usefully.
//
// A property may be written bare (url), in the gofly namespace (gofly.url) or
// in the deprecated flyway one (flyway.url). The flyway namespace still works,
// so an existing flyway.conf needs no editing, but it warns, and so does a
// configuration that uses both namespaces at once.
//
// The namespace is recorded only once the property has turned out to be a real
// one, so that an unrelated FLYWAY_HOME sitting in the environment never counts
// as use of the flyway namespace.
// -----------------------------------------------------------------------------
func (c *Config) SetFrom(key string, value string, origin Origin) error {
	namespace, key := splitNamespace(strings.TrimSpace(key))

	if err := c.applyProperty(key, value); err != nil {
		return err
	}

	c.recordNamespace(namespace, key, origin)

	return nil
}

// -----------------------------------------------------------------------------
// applyProperty
//
// Writes one bare property into the configuration. A name that matches nothing
// comes back as ErrUnknownProperty with the configuration untouched, which is
// what lets LoadEnvironment skip the variables that are not ours.
// -----------------------------------------------------------------------------
func (c *Config) applyProperty(key string, value string) error {
	// placeholder names keep their case, only the prefix is matched loosely
	if len(key) > len("placeholders.") && strings.EqualFold(key[:len("placeholders.")], "placeholders.") {
		c.Placeholders[key[len("placeholders."):]] = value
		return nil
	}

	switch strings.ToLower(key) {
	case "url":
		c.URL = value
	case "user":
		c.User = value
	// -pass is not a Flyway property, it is accepted because everybody types it
	case "password", "pass":
		c.Password = value
	case "connectretries":
		return setInt(&c.ConnectRetries, key, value)
	case "schemas":
		c.Schemas = splitList(value)
	case "defaultschema":
		c.DefaultSchema = value
	case "goflyschema":
		c.GoflySchema = value
	// table is Flyway's name for it; goflyTable reads better next to
	// goflySchema and flywayTable, so both spellings set the same thing
	case "table", "goflytable":
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
	case "driver", "jardirs", "resolvers", "callbacks", "skipdefaultresolvers",
		"skipdefaultcallbacks", "cleanonvalidationerror", "configfiles", "errorhandlers",
		"dryrunoutput":
		return nil

	default:
		return fmt.Errorf("%w: %s", ErrUnknownProperty, key)
	}

	return nil
}

// -----------------------------------------------------------------------------
// splitNamespace
//
// Separates an optional flyway/gofly namespace from the property name. Both the
// dot and the underscore are accepted as the separator, since config files in
// the wild use either.
// -----------------------------------------------------------------------------
func splitNamespace(key string) (string, string) {
	for _, namespace := range []string{NamespaceFlyway, NamespaceGofly} {
		for _, separator := range []string{".", "_"} {
			prefix := namespace + separator
			if len(key) > len(prefix) && strings.EqualFold(key[:len(prefix)], prefix) {
				return namespace, key[len(prefix):]
			}
		}
	}

	return namespaceNone, key
}

// -----------------------------------------------------------------------------
// recordNamespace
//
// Notes which namespace a property was written in, warns the first time the
// deprecated one shows up, and warns again the first time both turn out to be
// in use. Mixing is worth saying out loud, because a half renamed configuration
// is hard to reason about, but it is never ambiguous: precedence decides, the
// same as it would within one namespace. Refusing to run over it only stranded
// people whose real problem was a stray FLYWAY_* variable they did not set.
// -----------------------------------------------------------------------------
func (c *Config) recordNamespace(namespace string, key string, origin Origin) {
	if namespace == namespaceNone {
		return
	}

	other := NamespaceGofly
	if namespace == NamespaceGofly {
		other = NamespaceFlyway
	}

	if clash, mixed := c.namespaceOrigin[other]; mixed && !c.warnedMixing {
		c.warnedMixing = true
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"the %s.* and %s.* property namespaces are both in use: %s.%s comes from %s, "+
				"while %s was already set from %s. Both still apply, and the usual precedence "+
				"decides between them, but pick one namespace and use it throughout, "+
				"%s.* is the one to move to",
			other, namespace, namespace, key, origin.Location, clash.property, clash.location, NamespaceGofly))
	}

	if _, seen := c.namespaceOrigin[namespace]; !seen {
		c.namespaceOrigin[namespace] = namespaceUse{
			property: namespace + "." + key,
			location: origin.Location,
		}
	}

	if namespace == NamespaceFlyway && !c.warnedOrigins[origin.Source] {
		c.warnedOrigins[origin.Source] = true
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"%s still uses the deprecated %s.* namespace; rename those properties to %s.* "+
				"(%s.url becomes %s.url, and so on). They keep working for now, but mixing "+
				"the two namespaces warns as well",
			origin.Source, NamespaceFlyway, NamespaceGofly, NamespaceFlyway, NamespaceGofly))
	}
}

// -----------------------------------------------------------------------------
// LoadConfigFile
//
// Reads a properties file in the Flyway syntax. Blank lines and lines starting
// with # or ! are skipped. Properties may use the gofly.* namespace, the
// deprecated flyway.* one or no namespace at all, but not a mixture.
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

		origin := FileOrigin(path, lineNumber)
		if err := c.SetFrom(strings.TrimSpace(key), strings.TrimSpace(value), origin); err != nil {
			return fmt.Errorf("%s: %w", origin.Location, err)
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
//
// Variables that name no known property are ignored. The environment is not
// written for gofly alone: a machine with the Java edition installed exports
// FLYWAY_DIR and FLYWAY_HOME, which point at the install and are not settings
// at all, and refusing to run because of one is no help to anybody.
// -----------------------------------------------------------------------------
func (c *Config) LoadEnvironment(environment []string) error {
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}

		var namespace, suffix string
		switch {
		case strings.HasPrefix(name, "FLYWAY_"):
			namespace, suffix = NamespaceFlyway, strings.TrimPrefix(name, "FLYWAY_")
		case strings.HasPrefix(name, "GOFLY_"):
			namespace, suffix = NamespaceGofly, strings.TrimPrefix(name, "GOFLY_")
		default:
			continue
		}

		key, ok := environmentKeyToProperty(suffix)
		if !ok {
			continue
		}

		// the environment carries the namespace in the variable name itself, so
		// FLYWAY_URL counts as flyway.url for the deprecation and mixing rules
		if err := c.SetFrom(namespace+"."+key, value, EnvironmentOrigin(name)); err != nil {
			if errors.Is(err, ErrUnknownProperty) {
				continue
			}
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
	goflyCandidates := []string{}
	flywayCandidates := []string{}

	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		goflyCandidates = append(goflyCandidates, filepath.Join(directory, "conf", "gofly.conf"))
		flywayCandidates = append(flywayCandidates, filepath.Join(directory, "conf", "flyway.conf"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		goflyCandidates = append(goflyCandidates, filepath.Join(home, "gofly.conf"))
		flywayCandidates = append(flywayCandidates, filepath.Join(home, "flyway.conf"))
	}
	goflyCandidates = append(goflyCandidates, "gofly.conf")
	flywayCandidates = append(flywayCandidates, "flyway.conf")

	// a gofly.conf anywhere wins outright: picking up a leftover flyway.conf as
	// well would mix the two namespaces through no fault of the user
	if existing := existingFiles(goflyCandidates); len(existing) > 0 {
		return existing
	}

	return existingFiles(flywayCandidates)
}

// -----------------------------------------------------------------------------
// existingFiles
// -----------------------------------------------------------------------------
func existingFiles(candidates []string) []string {
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
