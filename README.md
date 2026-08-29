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
- **All-or-nothing migrations** — `-group=true` runs the whole batch in one
  transaction.
- **Its own history schema**, and a one-off import of an existing Flyway history
  the first time it runs against a database.
- **PostgreSQL, MySQL / MariaDB, SQL Server and SQLite.**

## Install

```sh
go install github.com/pausan/gofly@latest
```

or build it yourself:

```sh
make build          # ./build/gofly
make build-all      # linux, macos and windows, amd64 and arm64
```

The binary is built with `CGO_ENABLED=0`, so it is fully static and the SQLite
support needs no system library.

## Use

```sh
gofly -url=jdbc:postgresql://localhost:5432/mydb \
      -user=admin -password=secret \
      -locations=filesystem:./sql \
      migrate
```

Commands: `migrate`, `undo`, `info`, `validate`, `baseline`, `repair`.
Run `gofly` with no arguments for the full list of options.

```
$ gofly -url=jdbc:sqlite:app.db -locations=filesystem:./sql info

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

Settings come from four places, each overriding the one before it:

1. built-in defaults, which are Flyway's defaults
2. config files: `-configFiles=a.conf,b.conf`, or `gofly.conf` / `flyway.conf`
   picked up from the working directory and the home directory
3. environment: `FLYWAY_URL`, `GOFLY_URL`, `FLYWAY_SQL_MIGRATION_SEPARATOR`,
   `FLYWAY_PLACEHOLDERS_MYKEY` …
4. the command line: `-url=…`

Config files use the Flyway properties syntax, and an existing `flyway.conf`
works unchanged:

```properties
flyway.url=jdbc:mysql://db:3306/artypistdb
flyway.user=root
flyway.password=secret
flyway.locations=filesystem:./setup/sql/artypist/db
flyway.sqlMigrationSeparator=_
flyway.baselineVersion=10
```

Keys may be written bare (`url`), with the Flyway prefix (`flyway.url`) or with
the gofly one (`gofly.url`).

### Database urls

gofly speaks JDBC urls, so the connection strings from an existing Flyway setup
can be copied across verbatim:

| Database   | URL |
|------------|-----|
| PostgreSQL | `jdbc:postgresql://host:5432/database` |
| MySQL      | `jdbc:mysql://host:3306/database` |
| MariaDB    | `jdbc:mariadb://host:3306/database` |
| SQL Server | `jdbc:sqlserver://host:1433;databaseName=database` |
| SQLite     | `jdbc:sqlite:/path/to/file.db` |

## Taking over from Flyway

Point gofly at a database Flyway has been migrating and run `migrate`. On that
first run gofly:

1. creates its own history table, `gofly_schema_history`, in its own schema
   (`gofly`) where the database has schemas;
2. copies every row of `flyway_schema_history` into it, checksums, timestamps,
   `installed_by` and all;
3. carries on from there.

`flyway_schema_history` is only ever read, never written to and never dropped,
so going back to Flyway stays possible. Pass `-importFromFlyway=false` to skip
the import.

Because the checksums are identical, a migration that was edited after Flyway
applied it still fails validation afterwards. The import moves the history
across, it does not paper over what is wrong with it.

### Where the history lives

| Database   | Default location |
|------------|------------------|
| PostgreSQL | schema `gofly`, table `gofly_schema_history` |
| SQL Server | schema `gofly`, table `gofly_schema_history` |
| MySQL      | alongside your tables, table `gofly_schema_history` |
| SQLite     | alongside your tables, table `gofly_schema_history` |

On MySQL a schema *is* a database, so putting the history in one of its own
would mean `CREATE DATABASE` and privileges the migration user rarely has. Set
`-goflySchema=gofly` if you want it anyway. SQLite has no schemas at all.

Both names are configurable with `-goflySchema` and `-table`.

## Transactions

By default each migration runs in its own transaction, exactly like Flyway: a
failure leaves the migrations before it applied and records the failed one, so
the next run refuses to start until you `repair`.

With `-group=true` the whole batch runs inside a single transaction: either
every pending migration is applied, or the database is left untouched and the
history stays empty.

This depends on the engine. PostgreSQL, SQL Server and SQLite roll DDL back.
**MySQL and MariaDB commit implicitly on every DDL statement**, so a grouped
migration cannot be fully rolled back there; gofly prints a warning rather than
pretending otherwise.

## What it deliberately does not do

The point is a small tool that does the essentials well, so a number of Flyway
features are out of scope: Java and script migrations, callbacks, cherry-pick,
dry runs, `flyway.ignoreMigrationPatterns`, and every database beyond the four
listed above.

`clean` is not implemented. Wiping a schema is not a migration, and each of
these databases already has a better tool for it.

## Development

```sh
make test              # the unit and sqlite tests
make test-coverage     # ... with a coverage report
make test-integration  # brings up postgres, mysql and sql server in docker
make lint              # gofmt and go vet
```

The integration tests are behind the `integration` build tag and only run
against the databases whose url is exported. See `lib/integration_test.go`.

## Licence

Apache 2.0, the same licence Flyway's community edition uses.
