# Command line

```
gofly [options] command [command ...]
```

Commands run in the order they are given, so `gofly ... info migrate info` is a
perfectly reasonable thing to type. Anything that does not start with `-` is a
command; everything else is an option, always in `-key=value` form.

The exit code is `0` when every command succeeded and `1` otherwise, which is
what a deployment script wants.

## Commands

### `migrate`

Applies every pending migration, in version order, then the repeatable ones in
alphabetical order.

Before doing anything it validates (unless `-validateOnMigrate=false`), so a
migration that was edited after being applied stops the run. Pending migrations
are of course not an error here, they are the point.

The first time it runs against a database it creates its own history table and,
if a Flyway history is already there, imports it. See
[compatibility.md](compatibility.md#taking-over-from-flyway).

```sh
gofly -url=jdbc:postgresql://localhost:5432/mydb -user=admin -password=secret \
      -locations=filesystem:./sql migrate
```

### `undo`

Undoes the most recently applied versioned migration, provided a `U`-prefixed
undo script exists for it.

With `-target` it keeps going, newest first, until it reaches a version below
the target or hits a migration with no undo script. Without one it stops after a
single migration.

Undoing records a new row of type `UNDO_SQL`; the original row stays, marked as
undone, and the migration becomes pending again so `migrate` re-applies it.

```sh
gofly ... undo                # the last migration
gofly ... -target=2 undo      # everything down to, but not including, version 2
```

### `info`

Prints the state of every migration. Read-only: it creates nothing.

On a database still managed by Flyway it reads `flyway_schema_history` and says
so, rather than reporting everything as pending.

```
Schema version: 2

+-----------+---------+---------------+------+----------------------+---------+----------+
| Category  | Version | Description   | Type | Installed On         | State   | Undoable |
+-----------+---------+---------------+------+----------------------+---------+----------+
| Versioned | 1       | Create users  | SQL  | 2026-08-29T06:05:24Z | Success | Yes      |
| Versioned | 2       | Add email     | SQL  | 2026-08-29T06:05:24Z | Success | No       |
| Versioned | 3       | Create orders | SQL  |                      | Pending | No       |
+-----------+---------+---------------+------+----------------------+---------+----------+
```

Undo migrations get no row of their own; they show up in the `Undoable` column
of the migration they undo.

The states are Flyway's: `Pending`, `Success`, `Failed`, `Out of Order`,
`Missing`, `Future`, `Ignored`, `Above Target`, `Below Baseline`, `Baseline`,
`Undone`, `Outdated`, `Superseded`, `Deleted`, `Available`.

### `validate`

Checks the migrations on disk against the schema history and fails on the first
discrepancy. Read-only: like `info`, it creates nothing, and on a database still
managed by Flyway it validates against `flyway_schema_history`.

Unlike `migrate`, a pending migration *is* an error here: `validate` asks whether
the database matches the code, and a migration that has not been applied means
it does not.

```sh
gofly ... validate
```

### `baseline`

Marks an existing database as already migrated up to `-baselineVersion`, so that
only migrations above it are ever applied. Use it when adopting gofly on a
database whose schema was built by hand.

Refuses to run if the history table already holds migrations.

```sh
gofly ... -baselineVersion=10 -baselineDescription="Legacy schema" baseline
```

### `repair`

Fixes a schema history that no longer lines up with the files:

- removes the rows of migrations that failed
- realigns the stored checksum, description and type with the file on disk
- marks as deleted the applied migrations whose file is gone

Repair never touches your tables, only the history.

```sh
gofly ... repair
```

### `clean`

Not implemented. See [compatibility.md](compatibility.md#deliberately-left-out).

### `help`, `version`

```sh
gofly help
gofly version
```

## Options

Every option below can equally be set in a config file or through the
environment; see [configuration.md](configuration.md).

### Connection

| Option | Default | Meaning |
|---|---|---|
| `-url` | — | Database url, required; the `jdbc:` prefix is optional |
| `-user` | — | Database user |
| `-password` | — | Database password (`-pass` is accepted too) |
| `-connectRetries` | `0` | Retries, one second apart, before giving up |

Url formats are listed in [configuration.md](configuration.md#database-urls).

### Finding migrations

| Option | Default | Meaning |
|---|---|---|
| `-locations` | `filesystem:sql` | Comma separated `filesystem:<dir>` locations, scanned recursively |
| `-sqlMigrationPrefix` | `V` | Prefix of versioned migrations |
| `-undoSqlMigrationPrefix` | `U` | Prefix of undo migrations |
| `-repeatableSqlMigrationPrefix` | `R` | Prefix of repeatable migrations |
| `-sqlMigrationSeparator` | `__` | Between the version and the description |
| `-sqlMigrationSuffixes` | `.sql` | Comma separated, matched case insensitively |
| `-encoding` | `UTF-8` | Accepted for compatibility; gofly always reads UTF-8 |

### Behaviour

| Option | Default | Meaning |
|---|---|---|
| `-target` | `latest` | Stop here. Also accepts `current`, `next`, `latest` |
| `-group` | `false` | Run every pending migration in one transaction |
| `-outOfOrder` | `false` | Apply migrations older than the current version |
| `-validateOnMigrate` | `true` | Validate before migrating |
| `-baselineVersion` | `1` | Version the `baseline` command records |
| `-baselineDescription` | `<< Flyway Baseline >>` | Description it records |
| `-baselineOnMigrate` | `false` | Baseline automatically on the first migrate |
| `-ignoreMissingMigrations` | `false` | Tolerate applied migrations whose file is gone |
| `-ignoreFutureMigrations` | `true` | Tolerate history rows newer than anything local |
| `-installedBy` | the connecting user | What to record in `installed_by` |
| `-skipExecutingMigrations` | `false` | Record migrations as applied without running them |
| `-mixed` | `false` | Accepted for compatibility, has no effect |
| `-cleanDisabled` | `true` | `clean` is not implemented either way |

### Schema history

| Option | Default | Meaning |
|---|---|---|
| `-table` | `gofly_schema_history` | Name of gofly's history table |
| `-goflySchema` | `gofly` on PostgreSQL and SQL Server, none elsewhere | Schema holding it |
| `-defaultSchema` | the connection's own | Schema the migrations run against |
| `-schemas` | — | Comma separated; the first is the default schema |
| `-flywayTable` | `flyway_schema_history` | The table to import from and validate against |
| `-importFromFlyway` | `true` | Import an existing Flyway history on the first run |

### Placeholders

| Option | Default | Meaning |
|---|---|---|
| `-placeholders.NAME` | — | Sets the placeholder `NAME` |
| `-placeholderPrefix` | `${` | |
| `-placeholderSuffix` | `}` | |
| `-placeholderReplacement` | `true` | Turn substitution off entirely |

Placeholders are replaced *after* the checksum is computed, so the checksum
depends on the file as written and not on the values passed in. That is what
lets the same migration be deployed to several environments.

### Other

| Option | Default | Meaning |
|---|---|---|
| `-configFiles` | discovered, see [configuration.md](configuration.md) | Comma separated properties files |
| `-q` | off | Quiet |
| `-X` | off | Verbose |

## Migrating from the Flyway command line

The options above are Flyway's, so an existing invocation usually needs no
editing at all:

```sh
# before
flyway -url=jdbc:postgresql://db:5432/app -user=admin -connectRetries=10 \
       -locations=filesystem:/db/schema migrate

# after
gofly  -url=jdbc:postgresql://db:5432/app -user=admin -connectRetries=10 \
       -locations=filesystem:/db/schema migrate
```

Options gofly does not implement are listed in
[compatibility.md](compatibility.md#deliberately-left-out). Ones that only ever
mattered to the Java edition (`-driver`, `-jarDirs`, `-resolvers`, `-callbacks`,
`-dryRunOutput`, …) are accepted and ignored, so a long-standing command line
keeps working.
