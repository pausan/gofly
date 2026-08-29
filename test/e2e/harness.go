//go:build e2e

// Copyright (C) 2026 Pau Sanchez
//
// The plumbing behind the compatibility harness: how a target database is
// described, how it is wiped between scenarios, how real Flyway is invoked and
// how two schema histories are compared.
package e2e

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pausan/gofly/lib"
)

// Target is one database the harness runs every scenario against.
//
// Two urls are needed because gofly runs on this machine and reaches the
// database through a published port, while Flyway runs inside a container and
// reaches it by its name on the docker network.
type Target struct {
	Name string

	// URL is what gofly connects with, from the host
	URL string

	// FlywayURL is what the Flyway container connects with
	FlywayURL string

	User     string
	Password string

	// Dialect is the gofly dialect name, used to pick the reset statements
	Dialect string

	// SQLDialect names the flavour the fixture migrations are written in
	SQLDialect string
}

// -----------------------------------------------------------------------------
// Targets
//
// Returns the databases configured through the environment. SQLite needs no
// server, so it is always included unless explicitly disabled.
// -----------------------------------------------------------------------------
func Targets(t *testing.T) []Target {
	t.Helper()

	targets := []Target{}

	if os.Getenv("GOFLY_E2E_SKIP_SQLITE") == "" {
		targets = append(targets, Target{
			Name:       "sqlite",
			Dialect:    lib.DialectSqlite,
			SQLDialect: "sqlite",
		})
	}

	servers := []struct {
		prefix     string
		name       string
		dialect    string
		sqlDialect string
	}{
		{"PG", "postgres", lib.DialectPostgres, "postgres"},
		{"MYSQL", "mysql", lib.DialectMysql, "mysql"},
		{"MSSQL", "mssql", lib.DialectMssql, "mssql"},
	}

	for _, server := range servers {
		url := os.Getenv("GOFLY_E2E_" + server.prefix + "_URL")
		if url == "" {
			continue
		}

		targets = append(targets, Target{
			Name:       server.name,
			URL:        url,
			FlywayURL:  os.Getenv("GOFLY_E2E_" + server.prefix + "_FLYWAY_URL"),
			User:       os.Getenv("GOFLY_E2E_" + server.prefix + "_USER"),
			Password:   os.Getenv("GOFLY_E2E_" + server.prefix + "_PASSWORD"),
			Dialect:    server.dialect,
			SQLDialect: server.sqlDialect,
		})
	}

	if len(targets) == 0 {
		t.Skip("no database configured, see test/e2e/README.md")
	}

	return targets
}

// -----------------------------------------------------------------------------
// IsSQLite
// -----------------------------------------------------------------------------
func (target Target) IsSQLite() bool {
	return target.Dialect == lib.DialectSqlite
}

// -----------------------------------------------------------------------------
// CanRunFlyway
//
// Reports whether real Flyway can be pointed at this target. Every scenario
// that compares the two tools needs this.
// -----------------------------------------------------------------------------
func (target Target) CanRunFlyway() bool {
	return target.FlywayURL != "" || target.IsSQLite()
}

// Workspace is one scenario's sandbox: a migrations directory, a database wiped
// clean, and the two tools pointed at them.
type Workspace struct {
	t      *testing.T
	target Target

	// Dir holds the migration files, mounted into the Flyway container
	Dir string

	// url is what gofly connects with, which for SQLite points inside Dir
	url string
}

// -----------------------------------------------------------------------------
// NewWorkspace
//
// Creates the migrations directory and leaves the database empty.
// -----------------------------------------------------------------------------
func NewWorkspace(t *testing.T, target Target) *Workspace {
	t.Helper()

	// docker has to be able to mount this, so it lives under the system
	// temporary directory rather than anywhere exotic
	dir, err := os.MkdirTemp("", "gofly-e2e-")
	if err != nil {
		t.Fatalf("cannot create the workspace: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("cannot open up %s for the flyway container: %v", dir, err)
	}

	workspace := &Workspace{t: t, target: target, Dir: dir, url: target.URL}

	if target.IsSQLite() {
		workspace.url = "jdbc:sqlite:" + filepath.Join(dir, "e2e.db")
	}

	workspace.Reset()

	return workspace
}

// -----------------------------------------------------------------------------
// Write
//
// Adds a migration to the workspace.
// -----------------------------------------------------------------------------
func (w *Workspace) Write(name string, content string) {
	w.t.Helper()

	path := filepath.Join(w.Dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatalf("cannot write %s: %v", path, err)
	}
}

// -----------------------------------------------------------------------------
// Remove
// -----------------------------------------------------------------------------
func (w *Workspace) Remove(name string) {
	w.t.Helper()

	if err := os.Remove(filepath.Join(w.Dir, name)); err != nil {
		w.t.Fatalf("cannot remove %s: %v", name, err)
	}
}

// -----------------------------------------------------------------------------
// Reset
//
// Returns the database to an empty state, so that each scenario starts from the
// same place whichever tool ran last.
// -----------------------------------------------------------------------------
func (w *Workspace) Reset() {
	w.t.Helper()

	if w.target.IsSQLite() {
		os.Remove(filepath.Join(w.Dir, "e2e.db"))
		return
	}

	connection, err := lib.Connect(w.url, w.target.User, w.target.Password, 0)
	if err != nil {
		w.t.Fatalf("cannot connect to %s: %v", w.target.Name, err)
	}
	defer connection.Close()

	for _, statement := range resetStatements(w.target.Dialect) {
		if _, err := connection.DB().Exec(statement); err != nil {
			w.t.Fatalf("cannot reset %s with %q: %v", w.target.Name, statement, err)
		}
	}
}

// -----------------------------------------------------------------------------
// resetStatements
//
// Drops everything the harness might have created. Each scenario only ever uses
// the e2e_* tables plus the two history tables, so a targeted drop is enough and
// leaves anything else in the database alone.
// -----------------------------------------------------------------------------
func resetStatements(dialect string) []string {
	tables := []string{
		"e2e_orders", "e2e_users", "e2e_audit", "e2e_extra",
		lib.FlywayTable, lib.DefaultGoflyTable,
	}

	statements := []string{}

	switch dialect {
	case lib.DialectPostgres:
		statements = append(statements, "DROP SCHEMA IF EXISTS "+lib.DefaultGoflySchema+" CASCADE")
		for _, table := range tables {
			statements = append(statements, `DROP TABLE IF EXISTS "`+table+`" CASCADE`)
		}
		statements = append(statements, `DROP VIEW IF EXISTS "e2e_user_names" CASCADE`)

	case lib.DialectMysql:
		statements = append(statements, "DROP DATABASE IF EXISTS `"+lib.DefaultGoflySchema+"`")
		statements = append(statements, "DROP VIEW IF EXISTS `e2e_user_names`")
		for _, table := range tables {
			statements = append(statements, "DROP TABLE IF EXISTS `"+table+"`")
		}

	case lib.DialectMssql:
		statements = append(statements,
			`IF OBJECT_ID('[`+lib.DefaultGoflySchema+`].[`+lib.DefaultGoflyTable+`]', 'U') IS NOT NULL`+
				` DROP TABLE [`+lib.DefaultGoflySchema+`].[`+lib.DefaultGoflyTable+`]`,
			`IF SCHEMA_ID('`+lib.DefaultGoflySchema+`') IS NOT NULL EXEC('DROP SCHEMA [`+lib.DefaultGoflySchema+`]')`,
			`IF OBJECT_ID('[e2e_user_names]', 'V') IS NOT NULL DROP VIEW [e2e_user_names]`,
		)
		for _, table := range tables {
			statements = append(statements, `IF OBJECT_ID('[`+table+`]', 'U') IS NOT NULL DROP TABLE [`+table+`]`)
		}
	}

	return statements
}

// -----------------------------------------------------------------------------
// Config
//
// Builds a gofly configuration for this workspace.
// -----------------------------------------------------------------------------
func (w *Workspace) Config() *lib.Config {
	config := lib.NewConfig()

	config.URL = w.url
	config.User = w.target.User
	config.Password = w.target.Password
	config.Locations = []string{"filesystem:" + w.Dir}
	config.Quiet = true

	return config
}

// -----------------------------------------------------------------------------
// Gofly
//
// Opens gofly against this workspace. The caller closes it.
// -----------------------------------------------------------------------------
func (w *Workspace) Gofly(config *lib.Config) *lib.Gofly {
	w.t.Helper()

	if config == nil {
		config = w.Config()
	}

	gofly, err := lib.New(config)
	if err != nil {
		w.t.Fatalf("cannot open gofly against %s: %v", w.target.Name, err)
	}
	gofly.Output = io.Discard
	w.t.Cleanup(func() { gofly.Close() })

	return gofly
}

// -----------------------------------------------------------------------------
// RunFlyway
//
// Runs real Flyway in a container against this workspace and returns its output
// together with whether it succeeded.
// -----------------------------------------------------------------------------
func (w *Workspace) RunFlyway(args ...string) (string, bool) {
	w.t.Helper()

	image := os.Getenv("GOFLY_E2E_FLYWAY_IMAGE")
	if image == "" {
		image = "flyway/flyway:10"
	}

	command := []string{"run", "--rm"}

	// without this the container writes as root, and the SQLite file it creates
	// is then unreadable to gofly running as us
	if os.Geteuid() > 0 {
		command = append(command, "--user", fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid()))
	}

	if network := os.Getenv("GOFLY_E2E_DOCKER_NETWORK"); network != "" {
		command = append(command, "--network", network)
	}

	url := w.target.FlywayURL
	if w.target.IsSQLite() {
		url = "jdbc:sqlite:/flyway/sql/e2e.db"
	}

	// the workspace is mounted read-write for SQLite, whose database file lives
	// in it, and read-only everywhere else
	mount := w.Dir + ":/flyway/sql"
	if !w.target.IsSQLite() {
		mount += ":ro"
	}

	command = append(command, "-v", mount, image,
		"-url="+url,
		"-locations=filesystem:/flyway/sql",
		"-cleanDisabled=false",
	)
	if w.target.User != "" {
		command = append(command, "-user="+w.target.User, "-password="+w.target.Password)
	}
	command = append(command, args...)

	output, err := exec.Command("docker", command...).CombinedOutput()

	return string(output), err == nil
}

// -----------------------------------------------------------------------------
// MustRunFlyway
// -----------------------------------------------------------------------------
func (w *Workspace) MustRunFlyway(args ...string) string {
	w.t.Helper()

	output, ok := w.RunFlyway(args...)
	if !ok {
		w.t.Fatalf("flyway %v failed:\n%s", args, output)
	}

	return output
}

// HistoryRow is one schema history row, reduced to the fields that have to
// agree between the two tools. Timestamps and execution times are left out on
// purpose: they differ by construction.
type HistoryRow struct {
	InstalledRank int
	Version       string
	Description   string
	Type          string
	Script        string
	Checksum      string
	Success       bool
}

// -----------------------------------------------------------------------------
// String
// -----------------------------------------------------------------------------
func (row HistoryRow) String() string {
	return fmt.Sprintf("%d | %-8s | %-24s | %-9s | %-28s | %12s | %t",
		row.InstalledRank, row.Version, row.Description, row.Type, row.Script, row.Checksum, row.Success)
}

// -----------------------------------------------------------------------------
// ReadHistory
//
// Reads a schema history table into the comparable form above.
// -----------------------------------------------------------------------------
func (w *Workspace) ReadHistory(schema string, table string) []HistoryRow {
	w.t.Helper()

	connection, err := lib.Connect(w.url, w.target.User, w.target.Password, 0)
	if err != nil {
		w.t.Fatalf("cannot connect to %s: %v", w.target.Name, err)
	}
	defer connection.Close()

	dialect := connection.Dialect()

	name := dialect.QuoteIdentifier(table)
	if schema != "" && dialect.SupportsSchemas() {
		name = dialect.QuoteIdentifier(schema, table)
	}

	query := `SELECT installed_rank, version, description, type, script, checksum, success FROM ` +
		name + ` ORDER BY installed_rank`

	rows, err := connection.DB().Query(query)
	if err != nil {
		w.t.Fatalf("cannot read %s: %v", name, err)
	}
	defer rows.Close()

	history := []HistoryRow{}
	for rows.Next() {
		var (
			rank        int
			version     sql.NullString
			description sql.NullString
			rowType     string
			script      string
			checksum    sql.NullInt64
			success     bool
		)

		if err := rows.Scan(&rank, &version, &description, &rowType, &script, &checksum, &success); err != nil {
			w.t.Fatalf("cannot scan %s: %v", name, err)
		}

		row := HistoryRow{
			InstalledRank: rank,
			Version:       version.String,
			Description:   description.String,
			Type:          rowType,
			Script:        script,
			Checksum:      "null",
			Success:       success,
		}
		if checksum.Valid {
			row.Checksum = fmt.Sprintf("%d", checksum.Int64)
		}

		history = append(history, row)
	}

	if err := rows.Err(); err != nil {
		w.t.Fatalf("cannot read %s: %v", name, err)
	}

	return history
}

// -----------------------------------------------------------------------------
// AssertSameHistory
//
// Fails with a readable side by side diff when the two histories disagree.
// -----------------------------------------------------------------------------
func AssertSameHistory(t *testing.T, what string, flyway []HistoryRow, gofly []HistoryRow) {
	t.Helper()

	if len(flyway) == len(gofly) {
		same := true
		for index := range flyway {
			if flyway[index] != gofly[index] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}

	t.Errorf("%s: the schema histories differ\n\nflyway:\n%s\n\ngofly:\n%s",
		what, renderHistory(flyway), renderHistory(gofly))
}

// -----------------------------------------------------------------------------
// renderHistory
// -----------------------------------------------------------------------------
func renderHistory(rows []HistoryRow) string {
	if len(rows) == 0 {
		return "  <empty>"
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, "  "+row.String())
	}

	return strings.Join(lines, "\n")
}
