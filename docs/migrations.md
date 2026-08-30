# Migrations

## Naming

Versioned and undo migrations are named

```
prefix VERSION separator DESCRIPTION suffix
```

and repeatable ones leave the version out:

```
prefix separator DESCRIPTION suffix
```

With the defaults that gives:

| File | Kind |
|---|---|
| `V1__Create_users.sql` | versioned |
| `V2.1__Add_email.sql` | versioned |
| `U2.1__Add_email.sql` | undo for version 2.1 |
| `R__Refresh_views.sql` | repeatable |

The description is the part after the separator with underscores turned into
spaces, so `V1__Create_users.sql` is described as "Create users". Dashes are
left alone: `V1__add-email.sql` is "add-email".

Every part is configurable, and a project that has always used a single
underscore keeps working:

```properties
gofly.sqlMigrationPrefix=V
gofly.undoSqlMigrationPrefix=U
gofly.repeatableSqlMigrationPrefix=R
gofly.sqlMigrationSeparator=_
gofly.sqlMigrationSuffixes=.sql,.pkg
```

Prefixes are matched longest first, so a custom `VV` prefix can coexist with the
`U` and `R` ones.

Files that do not end in one of the configured suffixes are not migrations at
all, so a `README.md` can sit happily in the same directory. A file that *does*
carry the suffix but whose name makes no sense (`V__Nothing.sql`) is an error
rather than a silent skip.

## Version schemes

A version is one or more numbers separated by dots or underscores. Every scheme
Flyway documents works:

| Version | Notes |
|---|---|
| `1` | |
| `001` | leading zeroes are kept in the history but do not affect ordering |
| `5.2` | |
| `5.2.1.3` | any depth |
| `1_2` | underscores are read as dots, and **recorded as `1.2`** |
| `20260101120000` | timestamps |

Comparison is numeric, part by part, not lexicographic: `10` is newer than `9`,
and `005` equals `5`. Parts are arbitrary-precision, so a version longer than a
64-bit integer still sorts correctly.

**Trailing zeroes are trimmed**, so `1`, `1.0` and `1.0.0` are the same version.
Having two migration files whose versions compare equal is an error.

### Ordering

Versioned migrations run in version order, then repeatable ones in alphabetical
order of their description. Repeatable migrations therefore always see the
schema the versioned ones have just produced.

Several locations are merged and sorted together, which is how a project keeps
its schema and its test data apart while interleaving them by version:

```properties
gofly.locations=filesystem:./sql/schema,filesystem:./sql/testdata
```

## Checksums

The checksum is a CRC-32 over the file, read one line at a time. It is Flyway's
algorithm, reproduced exactly, and the consequences are worth knowing:

- **line endings do not matter** — `\n`, `\r\n` and `\r` all give the same value
- **a trailing newline does not matter**
- **blank lines do not matter**, since they contribute no bytes
- indentation and trailing whitespace *do* matter
- a byte order mark is stripped, but only from the first line
- the value is a signed 32-bit integer, which is why it can be negative

The checksum is taken from the file **as written**, before placeholders are
replaced. The same migration therefore has the same checksum in every
environment, whatever the placeholder values are.

Once a migration has been applied, its checksum is recorded. Change the file
afterwards and gofly refuses to do anything until you either put it back or run
`repair`. That is the whole safety mechanism, and it is not optional short of
`--validateOnMigrate=false`.

## Placeholders

```sql
CREATE TABLE ${schema_name}.users (id INT);
```

```sh
gofly ... --placeholders.schema_name=app migrate
```

Longer placeholder names are substituted first, so `${db}` never eats the start
of `${db_name}`. The delimiters are configurable with `--placeholderPrefix` and
`--placeholderSuffix`.

## Transactions

Each migration runs in its own transaction by default, exactly like Flyway.

With `--group=true` the whole pending batch runs in a single transaction: either
every migration is applied or the database is untouched and the history stays
empty.

This depends on the engine. PostgreSQL, SQL Server and SQLite roll DDL back.
**MySQL and MariaDB commit implicitly on every DDL statement**, so a grouped
migration cannot be fully rolled back there; gofly warns rather than pretending
otherwise.

### What a failure leaves behind

When a migration fails on an engine that can roll DDL back, the changes are gone
and **no failed row is recorded** — there is nothing to repair, and the
migration simply stays pending. Where DDL commits implicitly, half the migration
is still there, so the failure *is* recorded and blocks the next run until
`repair` clears it.

Both behaviours are Flyway's.

## Statement splitting

A migration is split into statements before being sent to the database, because
the drivers take one statement per round trip and because a failure should point
at a line number.

The splitter understands string literals, quoted identifiers, `--` and `/* */`
comments, and per dialect:

- **PostgreSQL** — `$$ … $$` and `$tag$ … $tag$` dollar-quoted bodies, so
  functions and `DO` blocks survive intact
- **MySQL** — backtick identifiers, backslash escapes, and the `DELIMITER`
  directive for triggers and procedures
- **SQL Server** — `GO` as a batch separator, when alone on its line

## Undo migrations

An undo migration is a `U`-prefixed file whose version matches the migration it
reverses:

```
V3__Create_orders.sql     CREATE TABLE orders (...);
U3__Create_orders.sql     DROP TABLE orders;
```

`gofly undo` runs them newest first. Each one records a row of type `UNDO_SQL`;
the original row stays in the history, marked `Undone`, and the migration
becomes pending again so `migrate` re-applies it.

There is no undo for repeatable migrations. Edit the repeatable migration to
describe the state you want and run `migrate`.

Undo is a Flyway Teams feature, so the community edition cannot be compared
against here. gofly implements it for everyone.
