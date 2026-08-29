# AGENTS.md

Notes for anyone, human or otherwise, working on this codebase.

## What this project is

gofly is a Flyway clone: a single static Go binary that applies SQL migrations
to PostgreSQL, MySQL/MariaDB, SQL Server and SQLite. It exists to avoid dragging
a JVM into every container that needs to migrate a database.

**Compatibility with Flyway is the point, not a nice-to-have.** A database that
Flyway has been migrating must be usable by gofly and vice versa. Anything that
would break that is a bug, however convenient it looks.

## Layout

```
main.go              command line: argument parsing, command dispatch, usage
lib/
  version.go         Flyway's MigrationVersion: big integer parts, comparison
  checksum.go        Flyway's CRC-32 over the file read line by line
  resource_name.go   V1.1__Description.sql -> prefix, version, description
  migration.go       scanning the locations, duplicate detection
  placeholder.go     ${placeholder} replacement
  sqlscript.go       splitting a script into statements, per dialect
  config.go          defaults, config files, environment, precedence
  db.go              the Dialect interface, JDBC url parsing, connecting
  db_postgres.go     one file per dialect
  db_mysql.go
  db_mssql.go
  db_sqlite.go
  history.go         the schema history table, and the Flyway import
  info.go            pairing resolved with applied migrations, states
  validate.go        the validation rules, with Flyway's error codes
  gofly.go           migrate, undo, baseline, repair
  asciitable.go      the info table
test/e2e/            the Flyway compatibility harness
docs/                cli, configuration, migrations, compatibility
```

## The rules that matter

### 1. Never change the checksum algorithm

`lib/checksum.go` reproduces
`org.flywaydb.core.internal.resolver.ChecksumCalculator`: a CRC-32 fed one line
at a time, where a line is what `java.io.BufferedReader.readLine` returns. The
consequences are load bearing and every one of them is deliberate:

- line endings do not affect the checksum (`\n`, `\r\n` and `\r` all work)
- a trailing newline does not affect it either
- **blank lines contribute nothing**, since they add zero bytes to the CRC
- a BOM is stripped, but only from the first line
- the result is a *signed* 32 bit integer, which is why the column is `INT`

If a test asserts a specific checksum number, that number came from Flyway.
Do not "fix" it.

### 2. The history table layout is Flyway's

Column names, order, types and index names in `CreateHistoryTableSQL` are copied
from each `*Database.getRawCreateScript` in the Flyway source. Real Flyway can
be pointed at gofly's table with `-table=gofly_schema_history` and reads it
without complaint. Keep it that way.

`type` values are Flyway's too: `SQL`, `UNDO_SQL`, `BASELINE`, `SCHEMA`,
`DELETE`.

### 3. Validation must fail for the same reasons

`lib/validate.go` follows `MigrationInfoImpl.validate()` in order, including the
wording of the messages and the error codes. The order matters: a failed
migration is reported before a missing one, a missing one before a checksum
mismatch. Tests compare the messages against real Flyway output.

### 4. Watch out for PostgreSQL's search_path

The stock `search_path` is `"$user", public`. Creating a schema named after the
connecting user silently makes it the current schema, and gofly creates a schema
called `gofly`. If the login is also called `gofly`, migrations would quietly
land in the history schema.

`postgresDialect.DefaultSchema` therefore walks `current_schemas(false)` and
skips the history schema, and `NewWithConnection` pins the session to the
resolved schema before anything runs. There is a regression test for this in
`lib/integration_test.go`; it cannot be reproduced on SQLite.

### 5. Failures are only recorded where the DDL could not be rolled back

`recordFailure` writes a failed row only when `SupportsDDLTransactions()` is
false. This is Flyway's behaviour (`DbMigrate`, the `else` branch of
`supportsDdlTransactions()`), and the reasoning holds: if everything rolled
back, there is nothing to repair, and a leftover failed row would block the next
run over changes that no longer exist.

Recording the failure unconditionally seems more helpful and is wrong. The
compatibility harness catches it.

### 6. The history stores the normalized version

`V1_2__x.sql` is recorded as `1.2`, because Flyway stores
`MigrationVersion.toString()`, which is the text with underscores already turned
into dots. `Version.RawVersion()` keeps the original spelling and must not be
used for the `version` column.

### 7. Version equality is not string equality

`1`, `1.0`, `1.0.0` and `1_0` are all the same version to Flyway, because
trailing zero parts are trimmed. Use `Version.CanonicalKey()` for map keys and
duplicate detection, never `Version.String()`, which keeps the text as written.

### 8. flyway.* is deprecated, and the namespaces never mix

Config properties and environment variables come in three forms: bare (`url`),
`gofly.*` (preferred) and `flyway.*` (deprecated, warns once per source). Once
one namespace has been used, the other is an error. Bare names belong to
neither, which is why the command line never trips the rule.

`Config.SetFrom` takes an `Origin`, whose `Source` deduplicates the warning per
file and whose `Location` pins the exact line for the error. Anything new that
reads configuration must go through `SetFrom` with a real origin.

## Testing

```sh
make test              # unit tests plus real migrations against sqlite
make test-e2e          # the compatibility harness, all four databases
make test-integration  # postgres, mysql and sql server in docker
```

### The compatibility harness is the important one

`test/e2e/` runs every scenario twice against the same engine, once with real
Flyway in a container and once with gofly, then compares the resulting schema
history tables. It has already caught three real incompatibilities that the unit
tests happily agreed with, because the unit tests only ever asserted what I
believed Flyway did.

**Run it before claiming anything about compatibility.** If a unit test and the
harness disagree, the harness is right and the unit test is encoding an
assumption.

Adding a database, or touching the checksum, the DDL, the version handling or
the validation rules, means adding to or re-running that suite.

The unit tests run real migrations against real SQLite files rather than mocks,
so they exercise the actual SQL. Coverage sits around 80%.

Integration tests are behind the `integration` build tag and skip themselves
unless `GOFLY_TEST_PG_URL` and friends are exported.

### Checking against real Flyway by hand

`make test-e2e` automates all of this, but the round trip is worth doing by hand
when debugging a specific disagreement:

```sh
docker network create gofly-test-net
docker run -d --rm --name gofly-test-pg --network gofly-test-net \
  -e POSTGRES_DB=goflytest -e POSTGRES_USER=gofly -e POSTGRES_PASSWORD=goflypass \
  -p 55433:5432 postgres:16-alpine

# let real Flyway migrate up to V2
docker run --rm --network gofly-test-net -v "$PWD/sql:/flyway/sql:ro" flyway/flyway:10 \
  -url=jdbc:postgresql://gofly-test-pg:5432/goflytest -user=gofly -password=goflypass \
  -locations=filesystem:/flyway/sql -target=2 migrate

# gofly imports the history and carries on
./build/gofly -url=jdbc:postgresql://127.0.0.1:55433/goflytest \
  -user=gofly -password=goflypass -locations=filesystem:./sql migrate

# and real Flyway can still read what gofly wrote
docker run --rm --network gofly-test-net -v "$PWD/sql:/flyway/sql:ro" flyway/flyway:10 \
  -url=jdbc:postgresql://gofly-test-pg:5432/goflytest -user=gofly -password=goflypass \
  -locations=filesystem:/flyway/sql -defaultSchema=gofly -table=gofly_schema_history info
```

The checksums in both history tables must be identical.

## Consulting the Flyway source

When a behaviour is unclear, read Flyway rather than guessing:

```sh
git clone --depth 1 https://github.com/flyway/flyway.git tmp-flyway
```

`tmp-*` is gitignored. The classes worth knowing:

| Question | Class |
|---|---|
| how is the checksum computed | `internal/resolver/ChecksumCalculator.java` |
| how is a version parsed and compared | `api/MigrationVersion.java` |
| how is a file name parsed | `internal/resource/ResourceNameParser.java` |
| what does validation check | `internal/info/MigrationInfoImpl.java` |
| how are states worked out | `internal/info/MigrationInfoServiceImpl.java` |
| what does the history table look like | `<db>/…/XxxDatabase.getRawCreateScript` |
| how is the info table rendered | `internal/info/MigrationInfoDumper.java` |

## Style

Follow what is already there, it is the same style as `pausan/syncdbdocs`:

- a `// Copyright (C) <year> Pau Sanchez` header, then a comment saying what the
  file is for
- every function preceded by a banner comment:

```go
// -----------------------------------------------------------------------------
// FunctionName
//
// What it does, and why, when the why is not obvious.
// -----------------------------------------------------------------------------
func FunctionName() {
```

- `gofmt`, tabs, no linter beyond `go vet`
- comments explain the reasoning, especially where the code looks odd because
  Flyway does something odd. Do not narrate what the code plainly says.
- test names read as sentences: `TestMigrateRefusesOnChecksumMismatch`

## Deliberately out of scope

See [docs/compatibility.md](docs/compatibility.md#deliberately-left-out) for the
full list and the reasoning. In short: Java and script migrations, callbacks,
cherry-pick, dry runs, `ignoreMigrationPatterns`, locking, `clean`, and every
database beyond the four supported.

If you add one of these, it should be because someone asked for it, not because
Flyway has it. And if you do add a database, it goes in the compatibility
harness in the same change.

## Documentation

`docs/` is written for users, `AGENTS.md` for whoever maintains this. When
behaviour changes, both need updating: `docs/cli.md` for options and commands,
`docs/configuration.md` for anything about config sources,
`docs/migrations.md` for naming, checksums and transactions,
`docs/compatibility.md` for anything that matches or diverges from Flyway.
