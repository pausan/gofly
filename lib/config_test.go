// Copyright (C) 2026 Pau Sanchez
package lib

import (
	"errors"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// TestConfigDefaultsMatchFlyway
// -----------------------------------------------------------------------------
func TestConfigDefaultsMatchFlyway(t *testing.T) {
	config := NewConfig()

	if config.SQLMigrationPrefix != "V" || config.UndoSQLMigrationPrefix != "U" ||
		config.RepeatableSQLMigrationPrefix != "R" {
		t.Error("the default prefixes do not match Flyway's")
	}
	if config.SQLMigrationSeparator != "__" {
		t.Errorf("the default separator is %q, want __", config.SQLMigrationSeparator)
	}
	if !config.ValidateOnMigrate {
		t.Error("validateOnMigrate defaults to true in Flyway")
	}
	if config.OutOfOrder || config.Group || config.BaselineOnMigrate {
		t.Error("outOfOrder, group and baselineOnMigrate all default to false")
	}
	if !config.IgnoreFuture {
		t.Error("ignoreFutureMigrations defaults to true in Flyway")
	}
	if config.BaselineVersion != "1" {
		t.Errorf("baselineVersion defaults to %q, want 1", config.BaselineVersion)
	}
}

// -----------------------------------------------------------------------------
// TestConfigAcceptsEitherNamespace
// -----------------------------------------------------------------------------
func TestConfigAcceptsEitherNamespace(t *testing.T) {
	// each namespace works on its own, including the underscore spelling some
	// config files in the wild use
	for _, key := range []string{"url", "flyway.url", "gofly.url", "flyway_url", "GOFLY_URL"} {
		config := NewConfig()
		if err := config.Set(key, "jdbc:sqlite:x.db"); err != nil {
			t.Fatalf("%s was rejected: %v", key, err)
		}
		if config.URL != "jdbc:sqlite:x.db" {
			t.Errorf("%s did not set the url", key)
		}
	}
}

// -----------------------------------------------------------------------------
// TestConfigWarnsAboutTheFlywayNamespace
// -----------------------------------------------------------------------------
func TestConfigWarnsAboutTheFlywayNamespace(t *testing.T) {
	config := NewConfig()

	if err := config.SetFrom("flyway.url", "jdbc:sqlite:x.db", FileOrigin("old.conf", 1)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := config.SetFrom("flyway.user", "root", FileOrigin("old.conf", 2)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(config.Warnings) != 1 {
		t.Fatalf("got %d warnings, want exactly one per source: %v", len(config.Warnings), config.Warnings)
	}
	for _, expected := range []string{"old.conf", "deprecated", "gofly.*"} {
		if !strings.Contains(config.Warnings[0], expected) {
			t.Errorf("the warning should mention %q: %s", expected, config.Warnings[0])
		}
	}
}

// -----------------------------------------------------------------------------
// TestConfigDoesNotWarnAboutTheGoflyNamespace
// -----------------------------------------------------------------------------
func TestConfigDoesNotWarnAboutTheGoflyNamespace(t *testing.T) {
	config := NewConfig()

	config.SetFrom("gofly.url", "jdbc:sqlite:x.db", FileOrigin("gofly.conf", 1))
	config.Set("user", "root")

	if len(config.Warnings) != 0 {
		t.Errorf("no warning was expected: %v", config.Warnings)
	}
}

// -----------------------------------------------------------------------------
// TestConfigWarnsAboutMixedNamespacesButAppliesThem
//
// Mixing the two namespaces is worth saying out loud, but it is not ambiguous,
// so both properties have to land.
// -----------------------------------------------------------------------------
func TestConfigWarnsAboutMixedNamespacesButAppliesThem(t *testing.T) {
	// flyway first, then gofly
	config := NewConfig()
	if err := config.SetFrom("flyway.url", "x", FileOrigin("a.conf", 1)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := config.SetFrom("gofly.user", "root", FileOrigin("a.conf", 2)); err != nil {
		t.Fatalf("mixing the namespaces should warn, not fail: %v", err)
	}

	if config.URL != "x" || config.User != "root" {
		t.Errorf("both properties should have been applied: %q / %q", config.URL, config.User)
	}

	mixing := findWarning(config.Warnings, "both in use")
	if mixing == "" {
		t.Fatalf("mixing the namespaces should have warned: %v", config.Warnings)
	}
	for _, expected := range []string{"flyway.url", "a.conf:1", "a.conf:2"} {
		if !strings.Contains(mixing, expected) {
			t.Errorf("the warning should mention %q: %s", expected, mixing)
		}
	}

	// and the other way round
	config = NewConfig()
	if err := config.SetFrom("gofly.url", "x", FileOrigin("b.conf", 1)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := config.SetFrom("flyway.user", "root", FileOrigin("b.conf", 2)); err != nil {
		t.Fatalf("mixing the namespaces should warn, not fail: %v", err)
	}
	if config.URL != "x" || config.User != "root" {
		t.Errorf("both properties should have been applied: %q / %q", config.URL, config.User)
	}
	if findWarning(config.Warnings, "both in use") == "" {
		t.Errorf("mixing the namespaces should have warned: %v", config.Warnings)
	}

	// however many properties follow, the mixing is only worth saying once
	config.SetFrom("flyway.table", "t", FileOrigin("b.conf", 3))
	config.SetFrom("flyway.encoding", "UTF-8", FileOrigin("b.conf", 4))
	if count := countWarnings(config.Warnings, "both in use"); count != 1 {
		t.Errorf("got %d mixing warnings, want exactly one: %v", count, config.Warnings)
	}
}

// -----------------------------------------------------------------------------
// findWarning
//
// Returns the first warning containing needle, or "" when there is none.
// -----------------------------------------------------------------------------
func findWarning(warnings []string, needle string) string {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return warning
		}
	}

	return ""
}

// -----------------------------------------------------------------------------
// countWarnings
// -----------------------------------------------------------------------------
func countWarnings(warnings []string, needle string) int {
	count := 0
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			count++
		}
	}

	return count
}

// -----------------------------------------------------------------------------
// TestConfigBareNamesMixWithAnything
//
// The command line has no namespace, so it must never trip the mixing rule.
// -----------------------------------------------------------------------------
func TestConfigBareNamesMixWithAnything(t *testing.T) {
	config := NewConfig()

	config.SetFrom("flyway.url", "x", FileOrigin("old.conf", 1))
	if err := config.Set("user", "root"); err != nil {
		t.Errorf("a bare property should always be accepted: %v", err)
	}

	config = NewConfig()
	config.SetFrom("gofly.url", "x", FileOrigin("gofly.conf", 1))
	if err := config.Set("user", "root"); err != nil {
		t.Errorf("a bare property should always be accepted: %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestConfigRejectsUnknownProperties
// -----------------------------------------------------------------------------
func TestConfigRejectsUnknownProperties(t *testing.T) {
	config := NewConfig()

	err := config.Set("flyway.notAThing", "x")
	if err == nil {
		t.Fatal("an unknown property should have been rejected")
	}
	if !errors.Is(err, ErrUnknownProperty) {
		t.Errorf("the error should be an ErrUnknownProperty: %v", err)
	}

	// a file names its properties on purpose, so a typo stays fatal there too:
	// only the environment gets the benefit of the doubt
	path := writeTempFile(t, "typo.conf", "gofly.notAThing=x\n")
	if err := config.LoadConfigFile(path); err == nil {
		t.Error("an unknown property in a config file should have been rejected")
	}

	// and an unknown name never claims its namespace
	if _, claimed := config.namespaceOrigin[NamespaceFlyway]; claimed {
		t.Error("an unknown property should not count as use of a namespace")
	}
}

// -----------------------------------------------------------------------------
// TestConfigGoflyTableIsAnAliasForTable
// -----------------------------------------------------------------------------
func TestConfigGoflyTableIsAnAliasForTable(t *testing.T) {
	config := NewConfig()

	if err := config.Set("goflyTable", "my_history"); err != nil {
		t.Fatalf("goflyTable was rejected: %v", err)
	}
	if config.Table != "my_history" {
		t.Errorf("table = %q, want my_history", config.Table)
	}

	if err := config.Set("table", "other_history"); err != nil {
		t.Fatalf("table was rejected: %v", err)
	}
	if config.Table != "other_history" {
		t.Errorf("table = %q, want other_history", config.Table)
	}

	// the alias lives in every namespace the plain name does
	config = NewConfig()
	if err := config.Set("gofly.goflyTable", "namespaced"); err != nil {
		t.Fatalf("gofly.goflyTable was rejected: %v", err)
	}
	if config.Table != "namespaced" {
		t.Errorf("table = %q, want namespaced", config.Table)
	}

	// and it does not disturb the flyway table it sits next to
	if config.FlywayTable != FlywayTable {
		t.Errorf("flywayTable = %q, want %q", config.FlywayTable, FlywayTable)
	}
}

// -----------------------------------------------------------------------------
// TestConfigParsesLists
// -----------------------------------------------------------------------------
func TestConfigParsesLists(t *testing.T) {
	config := NewConfig()

	if err := config.Set("locations", "filesystem:a, filesystem:b ,,filesystem:c"); err != nil {
		t.Fatalf("locations was rejected: %v", err)
	}
	if strings.Join(config.Locations, "|") != "filesystem:a|filesystem:b|filesystem:c" {
		t.Errorf("locations parsed as %v", config.Locations)
	}
}

// -----------------------------------------------------------------------------
// TestConfigParsesPlaceholders
// -----------------------------------------------------------------------------
func TestConfigParsesPlaceholders(t *testing.T) {
	config := NewConfig()

	if err := config.Set("flyway.placeholders.myKey", "myValue"); err != nil {
		t.Fatalf("the placeholder was rejected: %v", err)
	}
	if config.Placeholders["myKey"] != "myValue" {
		t.Errorf("placeholders parsed as %v", config.Placeholders)
	}
}

// -----------------------------------------------------------------------------
// TestConfigRejectsMalformedBooleans
// -----------------------------------------------------------------------------
func TestConfigRejectsMalformedBooleans(t *testing.T) {
	config := NewConfig()

	if err := config.Set("outOfOrder", "yes please"); err == nil {
		t.Error("a malformed boolean should have been rejected")
	}
}

// -----------------------------------------------------------------------------
// TestConfigReadsAFlywayConfFile
//
// The file below is the one artypist has been feeding to Flyway for years.
// -----------------------------------------------------------------------------
func TestConfigReadsAFlywayConfFile(t *testing.T) {
	content := `# a comment
flyway.url=jdbc:mysql://db:3306/artypistdb
flyway.user=root
flyway.password=secret

flyway.locations=filesystem:/var/www/html/setup/sql/artypist/db
flyway.sqlMigrationSeparator=_
flyway.baselineVersion=10
# flyway.table=
`
	path := writeTempFile(t, "flyway.conf", content)

	config := NewConfig()
	if err := config.LoadConfigFile(path); err != nil {
		t.Fatalf("cannot read the config file: %v", err)
	}

	if config.URL != "jdbc:mysql://db:3306/artypistdb" {
		t.Errorf("url read as %q", config.URL)
	}
	if config.User != "root" || config.Password != "secret" {
		t.Errorf("credentials read as %q / %q", config.User, config.Password)
	}
	if config.SQLMigrationSeparator != "_" {
		t.Errorf("separator read as %q", config.SQLMigrationSeparator)
	}
	if config.BaselineVersion != "10" {
		t.Errorf("baselineVersion read as %q", config.BaselineVersion)
	}
	if len(config.Locations) != 1 {
		t.Errorf("locations read as %v", config.Locations)
	}
}

// -----------------------------------------------------------------------------
// TestConfigReportsTheOffendingLine
// -----------------------------------------------------------------------------
func TestConfigReportsTheOffendingLine(t *testing.T) {
	path := writeTempFile(t, "bad.conf", "flyway.url=x\nthis line has no equals sign\n")

	config := NewConfig()
	err := config.LoadConfigFile(path)
	if err == nil {
		t.Fatal("a malformed line should have been rejected")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error should point at line 2: %v", err)
	}
}

// -----------------------------------------------------------------------------
// TestConfigReadsTheEnvironment
// -----------------------------------------------------------------------------
func TestConfigReadsTheEnvironment(t *testing.T) {
	config := NewConfig()

	environment := []string{
		"GOFLY_URL=jdbc:sqlite:x.db",
		"GOFLY_PASSWORD=fromenv",
		"GOFLY_SQL_MIGRATION_SEPARATOR=_",
		"GOFLY_BASELINE_ON_MIGRATE=true",
		"GOFLY_TABLE=custom_history",
		"GOFLY_PLACEHOLDERS_DBNAME=mydb",
		"PATH=/usr/bin",
		"UNRELATED=1",
	}

	if err := config.LoadEnvironment(environment); err != nil {
		t.Fatalf("the environment was rejected: %v", err)
	}

	if config.URL != "jdbc:sqlite:x.db" || config.Password != "fromenv" {
		t.Errorf("url %q password %q", config.URL, config.Password)
	}
	if config.SQLMigrationSeparator != "_" {
		t.Errorf("separator read as %q", config.SQLMigrationSeparator)
	}
	if !config.BaselineOnMigrate {
		t.Error("baselineOnMigrate was not read from the environment")
	}
	if config.Table != "custom_history" {
		t.Errorf("table read as %q", config.Table)
	}
	if config.Placeholders["dbname"] != "mydb" {
		t.Errorf("placeholders read as %v", config.Placeholders)
	}
}

// -----------------------------------------------------------------------------
// TestConfigTargetVersion
// -----------------------------------------------------------------------------
func TestConfigTargetVersion(t *testing.T) {
	config := NewConfig()

	target, err := config.TargetVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != VersionLatest {
		t.Error("an unset target means latest")
	}

	config.Target = "2.1"
	target, err = config.TargetVersion()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.String() != "2.1" {
		t.Errorf("target parsed as %q", target)
	}
}

// -----------------------------------------------------------------------------
// TestPlaceholderReplacement
// -----------------------------------------------------------------------------
func TestPlaceholderReplacement(t *testing.T) {
	replacer := NewPlaceholderReplacer(map[string]string{
		"db":      "short",
		"db_name": "long",
	})

	// the longer name must win, otherwise ${db_name} would become "short_name"
	got, err := replacer.Replace("CREATE TABLE ${db_name}.${db} (id INT)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "CREATE TABLE long.short (id INT)" {
		t.Errorf("replaced into %q", got)
	}
}

// -----------------------------------------------------------------------------
// TestPlaceholderReplacementCanBeDisabled
// -----------------------------------------------------------------------------
func TestPlaceholderReplacementCanBeDisabled(t *testing.T) {
	replacer := NewPlaceholderReplacer(map[string]string{"x": "y"})
	replacer.Enabled = false

	got, _ := replacer.Replace("${x}")
	if got != "${x}" {
		t.Errorf("replaced into %q despite being disabled", got)
	}
}

// -----------------------------------------------------------------------------
// TestPlaceholderCustomDelimiters
// -----------------------------------------------------------------------------
func TestPlaceholderCustomDelimiters(t *testing.T) {
	replacer := NewPlaceholderReplacer(map[string]string{"x": "y"})
	replacer.Prefix = "@@"
	replacer.Suffix = "@@"

	got, _ := replacer.Replace("@@x@@ and ${x}")
	if got != "y and ${x}" {
		t.Errorf("replaced into %q", got)
	}
}

// -----------------------------------------------------------------------------
// TestPlaceholderErrorOnUnset
// -----------------------------------------------------------------------------
func TestPlaceholderErrorOnUnset(t *testing.T) {
	replacer := NewPlaceholderReplacer(map[string]string{"known": "1"})
	replacer.ErrorOnUnset = true

	if _, err := replacer.Replace("${known}"); err != nil {
		t.Errorf("a known placeholder should not error: %v", err)
	}
	if _, err := replacer.Replace("${unknown}"); err == nil {
		t.Error("an unset placeholder should have been reported")
	}
}

// -----------------------------------------------------------------------------
// TestConfigReadsTheDeprecatedEnvironmentNamespace
// -----------------------------------------------------------------------------
func TestConfigReadsTheDeprecatedEnvironmentNamespace(t *testing.T) {
	config := NewConfig()

	err := config.LoadEnvironment([]string{
		"FLYWAY_URL=jdbc:sqlite:x.db",
		"FLYWAY_USER=root",
	})
	if err != nil {
		t.Fatalf("the environment was rejected: %v", err)
	}

	if config.URL != "jdbc:sqlite:x.db" || config.User != "root" {
		t.Errorf("FLYWAY_* was not read: %q / %q", config.URL, config.User)
	}
	if len(config.Warnings) == 0 {
		t.Error("FLYWAY_* should have warned")
	}
}

// -----------------------------------------------------------------------------
// TestConfigWarnsAboutMixedNamespacesInTheEnvironment
// -----------------------------------------------------------------------------
func TestConfigWarnsAboutMixedNamespacesInTheEnvironment(t *testing.T) {
	config := NewConfig()

	err := config.LoadEnvironment([]string{
		"FLYWAY_URL=jdbc:sqlite:x.db",
		"GOFLY_USER=root",
	})
	if err != nil {
		t.Fatalf("mixing FLYWAY_* and GOFLY_* should warn, not fail: %v", err)
	}

	if config.URL != "jdbc:sqlite:x.db" || config.User != "root" {
		t.Errorf("both variables should have been read: %q / %q", config.URL, config.User)
	}
	if findWarning(config.Warnings, "both in use") == "" {
		t.Errorf("mixing the namespaces should have warned: %v", config.Warnings)
	}
}

// -----------------------------------------------------------------------------
// TestConfigIgnoresEnvironmentVariablesThatNameNoProperty
//
// The environment belongs to the whole machine. A box with the Java edition
// installed exports FLYWAY_DIR and FLYWAY_HOME pointing at the install, and
// neither is a setting: gofly has to walk past them rather than refuse to run.
// They must not claim the flyway namespace either, or the next GOFLY_* variable
// would warn about mixing that nobody asked for.
// -----------------------------------------------------------------------------
func TestConfigIgnoresEnvironmentVariablesThatNameNoProperty(t *testing.T) {
	config := NewConfig()

	err := config.LoadEnvironment([]string{
		"FLYWAY_DIR=/usr/local/lib/flyway-6.3.1",
		"FLYWAY_HOME=/usr/local/lib/flyway-6.3.1",
		"FLYWAY_VERSION=6.3.1",
		"GOFLY_DEBUG=1",
		"GOFLY_URL=jdbc:sqlite:x.db",
	})
	if err != nil {
		t.Fatalf("unrelated FLYWAY_*/GOFLY_* variables should be ignored: %v", err)
	}

	if config.URL != "jdbc:sqlite:x.db" {
		t.Errorf("the real property was not read: %q", config.URL)
	}
	if len(config.Warnings) != 0 {
		t.Errorf("ignored variables should warn about nothing: %v", config.Warnings)
	}
}

// -----------------------------------------------------------------------------
// TestConfigFileMixingNamespacesIsReported
// -----------------------------------------------------------------------------
func TestConfigFileMixingNamespacesIsReported(t *testing.T) {
	path := writeTempFile(t, "mixed.conf", "flyway.url=jdbc:sqlite:x.db\ngofly.user=root\n")

	config := NewConfig()
	if err := config.LoadConfigFile(path); err != nil {
		t.Fatalf("a config file mixing namespaces should warn, not fail: %v", err)
	}

	if config.URL != "jdbc:sqlite:x.db" || config.User != "root" {
		t.Errorf("both lines should have been applied: %q / %q", config.URL, config.User)
	}

	mixing := findWarning(config.Warnings, "both in use")
	if mixing == "" {
		t.Fatalf("mixing the namespaces should have warned: %v", config.Warnings)
	}
	if !strings.Contains(mixing, ":2") {
		t.Errorf("the warning should point at line 2: %s", mixing)
	}
}

// -----------------------------------------------------------------------------
// TestConfigFileInTheFlywayNamespaceStillWorks
// -----------------------------------------------------------------------------
func TestConfigFileInTheFlywayNamespaceStillWorks(t *testing.T) {
	path := writeTempFile(t, "flyway.conf", "flyway.url=jdbc:sqlite:x.db\nflyway.user=root\n")

	config := NewConfig()
	if err := config.LoadConfigFile(path); err != nil {
		t.Fatalf("an existing flyway.conf must keep working: %v", err)
	}
	if config.URL != "jdbc:sqlite:x.db" || config.User != "root" {
		t.Errorf("the file was not read: %q / %q", config.URL, config.User)
	}
	if len(config.Warnings) != 1 {
		t.Errorf("got %d warnings, want one: %v", len(config.Warnings), config.Warnings)
	}
}

// -----------------------------------------------------------------------------
// TestConfigAcceptsAndIgnoresJavaOnlyProperties
//
// These only ever meant anything to the Java edition, but a long-standing
// flyway.conf carries them and must keep working. The switch that handles them
// lowercases the key, so a mixed case entry in that list silently never matches:
// this test is here to catch exactly that.
// -----------------------------------------------------------------------------
func TestConfigAcceptsAndIgnoresJavaOnlyProperties(t *testing.T) {
	javaOnly := []string{
		"driver", "jarDirs", "resolvers", "callbacks", "skipDefaultResolvers",
		"skipDefaultCallbacks", "cleanOnValidationError", "errorHandlers", "dryRunOutput",
	}

	for _, key := range javaOnly {
		config := NewConfig()
		if err := config.Set(key, "anything"); err != nil {
			t.Errorf("%s should be accepted and ignored: %v", key, err)
		}
	}
}

// -----------------------------------------------------------------------------
// TestConfigPropertyNamesAreCaseInsensitive
// -----------------------------------------------------------------------------
func TestConfigPropertyNamesAreCaseInsensitive(t *testing.T) {
	for _, key := range []string{"outOfOrder", "outoforder", "OUTOFORDER", "OutOfOrder"} {
		config := NewConfig()
		if err := config.Set(key, "true"); err != nil {
			t.Fatalf("%s was rejected: %v", key, err)
		}
		if !config.OutOfOrder {
			t.Errorf("%s did not set outOfOrder", key)
		}
	}
}
