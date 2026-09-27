# Compatibility with Flyway

gofly is not "Flyway-inspired". The algorithms are ported from the Flyway source
and checked against real Flyway on every supported database by the
[compatibility harness](../test/e2e/README.md), which fails if the two ever
drift apart.

## What matches exactly

### Checksums

The CRC-32 in `lib/checksum.go` reproduces
`org.flywaydb.core.internal.resolver.ChecksumCalculator` including its quirks:
the file is read one line at a time, so line endings, trailing newlines and
blank lines contribute nothing, and a BOM is stripped from the first line only.

A migration Flyway recorded as `641276544` is `641276544` to gofly.

### The schema history table

Column names, order, types, the primary key and the index name are copied from
each `XxxDatabase.getRawCreateScript` in the Flyway source:

```sql
CREATE TABLE gofly_schema_history (
    installed_rank INT NOT NULL,
    version        VARCHAR(50),
    description    VARCHAR(200) NOT NULL,
    type           VARCHAR(20) NOT NULL,
    script         VARCHAR(1000) NOT NULL,
    checksum       INTEGER,
    installed_by   VARCHAR(100) NOT NULL,
    installed_on   TIMESTAMP NOT NULL DEFAULT now(),
    execution_time INTEGER NOT NULL,
    success        BOOLEAN NOT NULL
);
```

Real Flyway can be pointed at it and reads it without complaint:

```sh
flyway -table=gofly_schema_history -defaultSchema=gofly info
```

The `type` values are Flyway's: `SQL`, `UNDO_SQL`, `BASELINE`, `SCHEMA`,
`DELETE`. Descriptions are truncated at 200 characters with an ellipsis and
scripts at 1000, as Flyway's `AbbreviationUtils` does, because the truncated
form is what ends up in the table on both sides.

Versions are stored normalized, so `V1_2__x.sql` is recorded as `1.2`.

### Versions

`lib/version.go` follows `MigrationVersion`: underscores become dots, the string
splits on every dot followed by a digit, parts are arbitrary-precision integers,
and trailing zeroes are trimmed so `1` equals `1.0` equals `1.0.0`.

### File names

`lib/resource_name.go` follows `ResourceNameParser`, including matching prefixes
longest-first and replacing underscores but not dashes in descriptions.

### Validation

`lib/validate.go` follows `MigrationInfoImpl.validate()`: the same checks, in the
same order, with the same messages and the same error codes
(`CHECKSUM_MISMATCH`, `APPLIED_VERSIONED_MIGRATION_NOT_RESOLVED`,
`FAILED_VERSIONED_MIGRATION`, …). Given the same database and the same files,
both tools refuse for the same reason and print the same text.

### Command line and configuration

The flags, the `flyway.conf` properties syntax and the `FLYWAY_*` environment
variables all work. See [configuration.md](configuration.md); note that the
`flyway.*` namespace is deprecated in favour of `gofly.*`.

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

## Where gofly differs on purpose

| | Flyway | gofly | Why |
|---|---|---|---|
| History table | `flyway_schema_history` in the default schema | Reuses an existing Flyway history; otherwise creates `gofly_schema_history` | Sharing one history prevents migration replay during handover |
| Undo | Teams edition only | included | It is a small feature and a useful one |
| `--group` | Teams edition only | included | All-or-nothing is the behaviour most people expect |
| Encoding | `--encoding`, `--detectEncoding` | always UTF-8 | Anything else in a migration in 2026 is a bug worth fixing at the source |

## Deliberately left out

The point of gofly is a small tool that does the essentials well. These are not
oversights:

### Commands

- **`clean`** — wiping a schema is not a migration, and every database we
  support already has a better tool for it. The command is accepted and refuses
  with an explanation, so a script that calls it fails loudly rather than
  quietly doing nothing.
- **`snapshot`, `check`, `diff`, `dryRunOutput`, `cherryPick`** — Teams and
  Enterprise features, well outside "the essentials".

### Migration types

- **Java and Spring JDBC migrations** — there is no JVM to load them into. That
  is the entire reason this project exists.
- **Script migrations** (`.ps1`, `.sh`, …) — running arbitrary executables as
  part of a migration is a security surface we would rather not have.
- **Callbacks** (`beforeMigrate.sql`, `afterMigrate.sql`, …) — recognised by the
  name parser so they are never mistaken for migrations, but not executed. Run
  them yourself around the `gofly` call.

### Options

- **`ignoreMigrationPatterns`** — the modern, expressive replacement for
  `ignoreMissingMigrations` and friends. gofly implements the older, blunter
  `--ignoreMissingMigrations` and `--ignoreFutureMigrations`, which cover what
  people actually use.
- **`--cleanOnValidationError`** — depends on `clean`.
- **`--errorOverrides`, `--batch`, `--outputQueryResults`, `--lockRetryCount`** —
  Teams features or tuning knobs for scale gofly is not aiming at.
- **`--driver`, `--jarDirs`, `--resolvers`, `--callbacks`, `--skipDefaultResolvers`,
  `--skipDefaultCallbacks`, `--errorHandlers`, `--dryRunOutput`** — accepted and
  ignored, since they only ever meant anything to the Java edition and old
  command lines should keep working.

### Databases

PostgreSQL, MySQL / MariaDB, SQL Server and SQLite. Flyway supports around
twenty more (Oracle, DB2, Snowflake, BigQuery, Redshift, CockroachDB, H2,
Derby, HSQLDB, SAP HANA, Sybase, Firebird, Informix, Spanner, Databricks,
Cassandra, Couchbase, SingleStore, Synapse …). Each one is a dialect, a driver
and a DDL to get exactly right, and none of them is on the list this project set
out to cover.

Adding one means implementing `lib.Dialect`, taking the history table DDL from
the matching `XxxDatabase.getRawCreateScript` in the Flyway source, and adding
the database to the compatibility harness. It is a contained piece of work if
someone needs it.

### Other

- **Locking outside PostgreSQL** — PostgreSQL mutating commands take session
  advisory locks compatible with Flyway, before reading or creating history.
  Other engines still require deployment-level serialization.
- **Baseline migrations** (`B1__…sql`, Teams) — not supported.
- **Custom `MigrationResolver` / `Callback` implementations** — Java interfaces,
  so there is nothing to plug in.

If you need one of these, it should be added because someone asked for it, not
because Flyway has it.
