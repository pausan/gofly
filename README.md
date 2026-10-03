# gofly is a drop-in replacement for Flyway

**SQL migrations in a single static binary, without the JVM.**

gofly is written in Go and supports **PostgreSQL, MySQL / MariaDB, SQL Server
and SQLite**. To switch from Flyway, change `flyway` to `gofly` in your
migration command. Your SQL files and configuration can stay as they are.

## At a glance

| Migration | File example | Behaviour |
|---|---|---|
| Versioned | `V1__Create_users.sql` | Runs once, in version order |
| Repeatable | `R__Refresh_views.sql` | Runs after versioned migrations, and again when its checksum changes |
| Undo | `U1__Create_users.sql` | Reverses the matching versioned migration, newest first |

- **Flyway checksums and history layout**, so existing migration history stays valid.
- **Validation before migrating**, so editing an applied versioned migration stops the run.
- **Grouped transactions** with `--group=true`, when the engine and SQL support them.
- **One static executable**, with database drivers included and no system SQLite library needed.

## Install and run

Download a binary from [Releases](https://github.com/pausan/gofly/releases), or
install with Go:

```sh
go install github.com/pausan/gofly@latest
```

Put a migration such as `V1__Create_users.sql` in `./sql`, then run:

```sh
gofly --url=jdbc:postgresql://localhost:5432/mydb \
      --user=admin --password=secret \
      --locations=filesystem:./sql migrate
```

Options accept both `-key=value` and `--key=value`; JDBC URLs also work without
the `jdbc:` prefix. You can use a config file, environment variables or command
line options.

### Smaller builds

The full binary includes all four database drivers. Releases also include
Linux builds for amd64 and arm64 with just one driver:

| Build | Databases |
|---|---|
| `gofly` | All four |
| `gofly.pg` | PostgreSQL |
| `gofly.mysql` | MySQL / MariaDB |
| `gofly.mssql` | SQL Server |
| `gofly.sqlite` | SQLite |

Run `gofly version` to see which databases a binary supports. To build from source:

```sh
make build             # build/gofly, for your platform
make build-all         # Linux, macOS and Windows, amd64 and arm64
make build-single      # Linux builds with one database driver
make release-all       # cross-compile and pack supported targets with UPX
```

Custom combinations are also possible:

```sh
CGO_ENABLED=0 go build -tags goflymin,db_pg,db_sqlite .
```

## Commands

| Command | What it does |
|---|---|
| `migrate` | Validates and applies pending migrations |
| `info` | Shows migration status without changing the database |
| `validate` | Checks migration files against history without changing the database |
| `undo` | Runs the undo script for the latest versioned migration |
| `baseline` | Marks an existing database as migrated up to `--baselineVersion` |
| `repair` | Removes failed history rows and realigns history with migration files |

Run `gofly` without arguments for help. Commands can be combined:

```sh
gofly --url=sqlite:./app.db --locations=filesystem:./sql info migrate info
```

`info` shows applied and pending migrations together:

```text
Schema version: 2

+-----------+---------+---------------+------+----------------------+---------+----------+
| Category  | Version | Description   | Type | Installed On         | State   | Undoable |
+-----------+---------+---------------+------+----------------------+---------+----------+
| Versioned | 1       | Create users  | SQL  | 2026-08-29T06:05:24Z | Success | Yes      |
| Versioned | 2       | Add email     | SQL  | 2026-08-29T06:05:24Z | Success | No       |
| Versioned | 3       | Create orders | SQL  |                      | Pending | No       |
+-----------+---------+---------------+------+----------------------+---------+----------+
```

See [commands and options](docs/cli.md) for details, including undo targets
and what `repair` changes.

## Configuration

Settings apply in this order, with each source overriding the one before it:
**defaults → config files → environment variables → command line**.

For example, put connection details and migration locations in `gofly.conf`:

```properties
gofly.url=jdbc:postgresql://localhost:5432/mydb
gofly.user=admin
gofly.locations=filesystem:./sql
```

Supply the password through the environment, then run:

```sh
export GOFLY_PASSWORD="$DB_PASSWORD"
gofly migrate
```

Use `--configFiles=path/to/gofly.conf` to select a file explicitly. Existing
`flyway.conf` files and `FLYWAY_*` variables still work; see
[configuration](docs/configuration.md) for discovery rules and all settings.

### Database URLs

| Database | URL example |
|---|---|
| PostgreSQL | `jdbc:postgresql://host:5432/database` |
| MySQL | `jdbc:mysql://host:3306/database` |
| MariaDB | `jdbc:mariadb://host:3306/database` |
| SQL Server | `jdbc:sqlserver://host:1433;databaseName=database` |
| SQLite | `jdbc:sqlite:/path/to/file.db` |

The `jdbc:` prefix is optional for all of these. PostgreSQL also accepts
`postgres://` and `pg://`. Quote SQL Server URLs on the command line because
they contain semicolons.

## Switching from Flyway

gofly reuses an existing Flyway history table by default. Both tools then see
the same applied migrations, so switching back does not replay changes.

Your `flyway.conf` properties and `FLYWAY_*` environment variables still work.
They print a deprecation warning; use `gofly.*` and `GOFLY_*` for new setups.
Mixing the two namespaces also warns, but both still apply.

For a fresh PostgreSQL database that both tools will manage, configure one
shared history from the outset:

```sh
gofly --url=jdbc:postgresql://localhost:5432/mydb \
      --user=admin --password=secret \
      --goflySchema=public --goflyTable=flyway_schema_history migrate
```

This example assumes Flyway uses `public.flyway_schema_history` and migrations
are in `./sql`. Use the schema and table configured for your Flyway setup.

`--reuseFlywayHistory=false` requests a separate-history import, leaving the
Flyway source unchanged. That source becomes stale as gofly applies new
migrations, so alternating tools requires one shared authoritative table.
See [taking over from Flyway](docs/compatibility.md#taking-over-from-flyway)
for existing dual histories and switching back safely.

### Where history lives

For a fresh database without an existing Flyway history:

| Database | Default history location |
|---|---|
| PostgreSQL / SQL Server | `gofly.gofly_schema_history` |
| MySQL / MariaDB / SQLite | `gofly_schema_history`, alongside your tables |

Change the names with `--goflySchema` and `--goflyTable`; Flyway's `--table`
is an alias for `--goflyTable`. On MySQL a schema is a database; SQLite has
no schemas.

## Transactions

Each migration runs in its own transaction by default. `--group=true` runs a
fully transactional pending batch in one transaction.

PostgreSQL, SQL Server and SQLite can roll back DDL. **MySQL and MariaDB
implicitly commit DDL**, so grouping cannot guarantee a full rollback there.
Some SQL must run outside a transaction; a script setting can also request
this. Mixing transactional and nontransactional statements requires
`--mixed=true`, and the whole script or group then runs without a transaction.

Transactional failures that roll back leave no failed history row.
Nontransactional failures leave a failed row: inspect and fix partial changes
before running `repair`. See [migration transactions](docs/migrations.md#transactions).

## Limitations

Java and script migrations, callbacks, `clean` and some advanced Flyway features
aren't supported. Only PostgreSQL has migration locking; on other databases,
run one migration process at a time. See the [compatibility details](docs/compatibility.md)
for limitations and advice on sharing history between the two tools.

## Documentation

- [Commands and options](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Migration naming, checksums and transactions](docs/migrations.md)
- [Flyway compatibility and limitations](docs/compatibility.md)

## Development

```sh
make build             # build/gofly
make test              # unit tests and SQLite migrations
make test-coverage     # tests with a coverage report
make test-e2e          # compare against real Flyway on all four databases
make test-integration  # PostgreSQL, MySQL and SQL Server in Docker
make lint              # gofmt and go vet
make db-down           # stop the test database containers
```

See the [compatibility harness](test/e2e/README.md) for test details.

MIT License. See [LICENSE](LICENSE).
