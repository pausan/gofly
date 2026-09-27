# gofly

gofly is a small, single binary reimplementation of the essential
[Flyway](https://documentation.red-gate.com/fd) commands, written in Go.

It exists because Flyway drags a JVM along. `gofly` is one static executable of a
few megabytes, with no runtime to install, no `flyway.conf` template to render
and no JDBC drivers to download. It starts in milliseconds, which matters when
every container start-up runs a migration.

Compatibility with Flyway is not approximate. gofly computes **the same
checksums**, refuses the same databases for the same reasons, writes the same
schema history layout, and understands the same command line flags and config
files. A database migrated by Flyway can be handed over to gofly, and a database
migrated by gofly can still be read by Flyway.

## What it does

- **Versioned migrations** — `V1__Description.sql`, with every version scheme
  Flyway supports (`1`, `001`, `5.2`, `5.2.1.3`, `20260101120000`, `1_2` …).
- **Undo migrations** — `U1__Description.sql`, undone newest first.
- **Repeatable migrations** — `R__Description.sql`, re-run whenever they change.
- **Flyway checksums** — bit for bit identical, so existing history stays valid.
- **Checksum validation** — a migration edited after being applied stops
  everything, with the same message Flyway prints.
- **All-or-nothing migrations** — `--group=true` runs the whole batch in one
  transaction.
- **Shared Flyway history** when one exists, or a private history with an optional
  one-off import; PostgreSQL runners coordinate with advisory locks.
- **PostgreSQL, MySQL / MariaDB, SQL Server and SQLite.**

## Install

```sh
go install github.com/pausan/gofly@latest
```

or build it yourself:

```sh
make build          # ./build/gofly
make build-all      # linux, macos and windows, amd64 and arm64
make release-all    # the same, stripped and packed with upx
```

The binary is built with `CGO_ENABLED=0`, so it is fully static and the SQLite
support needs no system library.

### Single database builds

The full binary talks to all four databases and is around 4 MB packed. If you
only ever migrate one of them, the releases also carry Linux builds that leave
the other three drivers out, at roughly half the size:

| Build | Databases | Packed size (amd64) |
|---|---|---|
| `gofly` | all four | 4.0 MB |
| `gofly.pg` | PostgreSQL | 2.4 MB |
| `gofly.sqlite` | SQLite | 2.2 MB |
| `gofly.mssql` | SQL Server | 1.9 MB |
| `gofly.mysql` | MySQL / MariaDB | 1.7 MB |

They are the same tool with the same flags and behaviour, they simply refuse a
url for a database they were not built with. `gofly version` prints what a given
binary supports. Build them yourself with `make build-single`, or directly:

```sh
go build -tags goflymin,db_pg .            # postgres only
go build -tags goflymin,db_pg,db_sqlite .  # postgres and sqlite
```

## Use

```sh
gofly --url=jdbc:postgresql://localhost:5432/mydb \
      --user=admin --password=secret \
      --locations=filesystem:./sql \
      migrate
```

Commands: `migrate`, `undo`, `info`, `validate`, `baseline`, `repair`.
Run `gofly` with no arguments for the full list of options, or see
**[docs/cli.md](docs/cli.md)**.

Options are `--key=value`. Flyway's single dash works everywhere too, so an
existing `flyway -url=… -locations=… migrate` command line runs under gofly with
only the binary name changed.

```
$ gofly --url=jdbc:sqlite:app.db --locations=filesystem:./sql info

Schema version: 3

+-----------+---------+---------------+------+----------------------+---------+----------+
| Category  | Version | Description   | Type | Installed On         | State   | Undoable |
+-----------+---------+---------------+------+----------------------+---------+----------+
| Versioned | 1       | Create users  | SQL  | 2026-08-29T06:05:24Z | Success | Yes      |
| Versioned | 2       | Add email     | SQL  | 2026-08-29T06:05:24Z | Success | No       |
| Versioned | 3       | Create orders | SQL  |                      | Pending | No       |
+-----------+---------+---------------+------+----------------------+---------+----------+
```

### Configuration

Settings come from four places, each overriding the one before it: built-in
defaults, config files, environment variables, then the command line. Full
details in **[docs/configuration.md](docs/configuration.md)**.

```properties
gofly.url=jdbc:mysql://db:3306/artypistdb
gofly.user=root
gofly.password=secret
gofly.locations=filesystem:./setup/sql/artypist/db
gofly.sqlMigrationSeparator=_
gofly.baselineVersion=10
```

An existing `flyway.conf` works unchanged: the `flyway.*` properties and the
`FLYWAY_*` environment variables are still read. They warn that `gofly.*` is
the name to move to, and using both namespaces at once warns too — a half-renamed
configuration is one nobody enjoys reading — but everything still applies.
`FLYWAY_*` variables that name no property, such as the `FLYWAY_DIR` and
`FLYWAY_HOME` exported by a Java Flyway install, are ignored.

### Database urls

gofly speaks JDBC urls, so the connection strings from an existing Flyway setup
can be copied across verbatim. The `jdbc:` prefix is optional, both columns below
mean the same thing:

| Database   | URL | Also accepted |
|------------|-----|---------------|
| PostgreSQL | `jdbc:postgresql://host:5432/database` | `postgresql://...`, `postgres://...`, `pg://...` |
| MySQL      | `jdbc:mysql://host:3306/database` | `mysql://...` |
| MariaDB    | `jdbc:mariadb://host:3306/database` | `mariadb://...` |
| SQL Server | `jdbc:sqlserver://host:1433;databaseName=database` | `sqlserver://...` |
| SQLite     | `jdbc:sqlite:/path/to/file.db` | `sqlite:/path/to/file.db` |

So a full run needs no config file at all:

```bash
gofly info --url=mysql://localhost:3306/artypistdb --user=myuser --pass=mypass
```

## Taking over from Flyway

An existing Flyway history is now reused as the writable history by default.
Both tools then see the same applied migrations, so switching back does not replay
changes. On PostgreSQL they also use compatible advisory locks on that history.
The application schema and search path stay unchanged. For a brand-new database
where both tools may start concurrently, configure gofly with
`--goflySchema=public --goflyTable=flyway_schema_history` from the outset so both
tools create and use the same history.

Use `--reuseFlywayHistory=false` for a separate-history import. This copies
checksums, installation timestamps and other history fields into gofly's own
table while leaving the source unchanged. On PostgreSQL, SQLite and SQL Server,
history creation and import commit atomically. A failed import can be retried
without replaying applied migrations.

Separate histories are not safe for alternating or concurrent Flyway/gofly runs:
the source becomes stale after gofly applies anything new. Before switching back,
point both tools at the same authoritative table and preserve the application
schema. For example, a private history in `public.gofly_schema_history` is used
by gofly with `--goflySchema=public` and Flyway with
`-table=gofly_schema_history`; this avoids changing the schema for unqualified SQL.

If both private and Flyway histories already exist, the default refuses to guess
which is authoritative. Reconcile them and explicitly configure one shared table,
or retain solo private-history operation with `--reuseFlywayHistory=false`.
Disabling `--importFromFlyway` disables both reuse and import.

`info` and `validate` remain read-only: they do not create or import history and
can validate against an existing Flyway table before takeover.

### Where the history lives

For a fresh database without Flyway history:

| Database   | Default location |
|------------|------------------|
| PostgreSQL | schema `gofly`, table `gofly_schema_history` |
| SQL Server | schema `gofly`, table `gofly_schema_history` |
| MySQL      | alongside your tables, table `gofly_schema_history` |
| SQLite     | alongside your tables, table `gofly_schema_history` |

On MySQL a schema *is* a database, so putting the history in one of its own
would mean `CREATE DATABASE` and privileges the migration user rarely has. Set
`--goflySchema=gofly` if you want it anyway. SQLite has no schemas at all.

Both names are configurable, with `--goflySchema` and `--goflyTable`. Flyway's
`--table` sets the same thing as `--goflyTable`.

## Transactions

By default each migration runs in its own transaction. PostgreSQL transactional
failures roll back without a failed history row. Nontransactional failures leave
a failed row and require inspection and repair before retrying.

With `--group=true` a fully transactional batch runs inside a single transaction: either
every pending migration is applied, or the database is left untouched and the
history stays empty.

This depends on the engine. PostgreSQL, SQL Server and SQLite roll DDL back.
**MySQL and MariaDB commit implicitly on every DDL statement**, so a grouped
migration cannot be fully rolled back there; gofly prints a warning rather than
pretending otherwise.

## What it deliberately does not do

The point is a small tool that does the essentials well, so a number of Flyway
features are out of scope: Java and script migrations, callbacks, cherry-pick,
dry runs, `ignoreMigrationPatterns`, locking outside PostgreSQL, and every database beyond the four
listed above.

`clean` is not implemented. Wiping a schema is not a migration, and each of
these databases already has a better tool for it.

The full list, with the reasoning, is in
**[docs/compatibility.md](docs/compatibility.md#deliberately-left-out)**.

## Documentation

- **[docs/cli.md](docs/cli.md)** — the commands and every option
- **[docs/configuration.md](docs/configuration.md)** — config files, environment, namespaces
- **[docs/migrations.md](docs/migrations.md)** — naming, versions, checksums, transactions
- **[docs/compatibility.md](docs/compatibility.md)** — what matches Flyway, what does not, what is left out

## Development

```sh
make test              # the unit and sqlite tests
make test-coverage     # ... with a coverage report
make test-e2e          # compare against real Flyway on all four databases
make test-integration  # brings up postgres, mysql and sql server in docker
make lint              # gofmt and go vet
make db-down           # stop the throwaway containers
```

`make test-e2e` is the one that matters. It runs the same migrations twice
against each engine, once with real Flyway in a container and once with gofly,
and compares the schema history tables row by row. If gofly ever drifts away
from Flyway, that suite fails with a side by side diff. See
**[test/e2e/README.md](test/e2e/README.md)**.

## Releases

Pushing to `master` runs the tests, the compatibility suite and a cross
compile, keeping the binaries as build artifacts. Publishing a GitHub release
is always a manual decision:

```sh
make release VERSION=v0.2.0
```

or push a `v*` tag, or run the Release workflow from the Actions tab. There is
no version to bump in the source: `main.go` says `dev`, and the build stamps
the release version into the binary. Either way it re-runs
everything, then uploads binaries for Linux, macOS and Windows on x86-64 and
ARM64, with a `SHA256SUMS` file.

## Licence

MIT License. See [LICENSE](LICENSE).
