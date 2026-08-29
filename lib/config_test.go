// Copyright (C) 2026 Pau Sanchez
package lib

import (
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
// TestConfigAcceptsFlywayAndGoflyPrefixes
// -----------------------------------------------------------------------------
func TestConfigAcceptsFlywayAndGoflyPrefixes(t *testing.T) {
	config := NewConfig()

	for _, key := range []string{"url", "flyway.url", "gofly.url"} {
		config.URL = ""
		if err := config.Set(key, "jdbc:sqlite:x.db"); err != nil {
			t.Fatalf("%s was rejected: %v", key, err)
		}
		if config.URL != "jdbc:sqlite:x.db" {
			t.Errorf("%s did not set the url", key)
		}
	}
}

// -----------------------------------------------------------------------------
// TestConfigRejectsUnknownProperties
// -----------------------------------------------------------------------------
func TestConfigRejectsUnknownProperties(t *testing.T) {
	config := NewConfig()

	if err := config.Set("flyway.notAThing", "x"); err == nil {
		t.Error("an unknown property should have been rejected")
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
		"FLYWAY_URL=jdbc:sqlite:x.db",
		"FLYWAY_PASSWORD=fromenv",
		"FLYWAY_SQL_MIGRATION_SEPARATOR=_",
		"FLYWAY_BASELINE_ON_MIGRATE=true",
		"GOFLY_TABLE=custom_history",
		"FLYWAY_PLACEHOLDERS_DBNAME=mydb",
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
