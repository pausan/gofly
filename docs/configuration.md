# Configuration

Every setting can be given in four places. Each overrides the one before it:

1. **built-in defaults** — Flyway's defaults
2. **config files** — `--configFiles=…`, or the ones gofly discovers
3. **environment variables** — `GOFLY_*`, or the deprecated `FLYWAY_*`
4. **the command line** — `--url=…`, or Flyway's `-url=…`

So a `gofly.conf` checked into the repository can hold the locations and the
naming conventions, while CI supplies the password through the environment and a
one-off run overrides the target on the command line.

Command line options are written `--key=value`; Flyway's single dash works
everywhere too, so `-url=…` and `--url=…` are the same option. See
[cli.md](cli.md).

## Config files

The syntax is Java properties, the same one Flyway uses:

```properties
# lines starting with # or ! are comments, blank lines are ignored
gofly.url=jdbc:postgresql://db:5432/app
gofly.user=admin
gofly.password=secret

gofly.locations=filesystem:./sql,filesystem:./testdata
gofly.sqlMigrationSeparator=__
gofly.baselineVersion=10

gofly.placeholders.env=production
```

Values are taken verbatim after the first `=`, with the surrounding whitespace
trimmed. There is no quoting and no escaping: a value containing `=` is fine,
one containing a newline is not.

An unknown property is an error, not a shrug. A typo in a config file should not
silently do nothing.

### Which files are read

`--configFiles=a.conf,b.conf` reads exactly those, in order.

Without it, gofly looks for `gofly.conf` in three places and reads all of them,
in this order:

1. `<directory of the gofly binary>/conf/gofly.conf`
2. `$HOME/gofly.conf`
3. `./gofly.conf`

If it finds no `gofly.conf` anywhere, it looks for `flyway.conf` in the same
places instead. It never reads both: picking up a leftover `flyway.conf`
alongside a `gofly.conf` would mix the two namespaces through no fault of yours.

## Property namespaces

A property may be written three ways:

| Written as | Namespace | Status |
|---|---|---|
| `url` | none | fine, and what the command line uses |
| `gofly.url` | gofly | **preferred** |
| `flyway.url` | flyway | works, but deprecated and warns |

An underscore works as the separator too, so `gofly_url` and `flyway_url` are
read the same as `gofly.url` and `flyway.url`.

### The flyway namespace is deprecated

An existing `flyway.conf` needs no editing. It is read as it stands, and gofly
prints one warning per file:

```
WARNING: /etc/flyway.conf still uses the deprecated flyway.* namespace; rename
those properties to gofly.* (flyway.url becomes gofly.url, and so on). They keep
working for now, but the two namespaces cannot be mixed
```

Renaming is a search and replace of `flyway.` to `gofly.`; nothing else changes.

### The two namespaces cannot be mixed

Once a `flyway.*` property has been read, a `gofly.*` one is an error, and the
other way round:

```
ERROR: /etc/app.conf:5: cannot mix the flyway.* and gofly.* property namespaces:
gofly.user comes from /etc/app.conf:5, while flyway.url was already set from
/etc/app.conf:2. Pick one namespace and use it throughout, gofly.* is the one to
move to
```

This holds across every source gofly reads, so a `flyway.conf` plus a
`GOFLY_PASSWORD` in the environment is refused as well. A half-renamed
configuration is one nobody can reason about, and the whole point of the rule is
that the migration is finished rather than perpetual.

Bare property names belong to neither namespace and mix with anything, which is
why the command line never trips the rule.

## Environment variables

The variable name carries the namespace, and the rest is the property name in
`SCREAMING_SNAKE_CASE`:

```sh
export GOFLY_URL="jdbc:postgresql://db:5432/app"
export GOFLY_PASSWORD="$DB_PASSWORD"
export GOFLY_SQL_MIGRATION_SEPARATOR="_"
export GOFLY_BASELINE_ON_MIGRATE=true
export GOFLY_PLACEHOLDERS_ENV=production      # sets the placeholder "env"
```

`FLYWAY_URL`, `FLYWAY_PASSWORD` and friends work identically, warn, and count as
the flyway namespace for the mixing rule above.

Variables that match no known property are ignored, so an unrelated
`FLYWAY_HOME` or `GOFLY_DEBUG` in the environment does no harm.

## Database urls

gofly speaks JDBC urls, so connection strings copy across from an existing
Flyway setup unchanged. The `jdbc:` prefix is optional: strip it and the url is
the native Go DSN, parsed exactly the same way.

| Database | URL | Also accepted |
|---|---|---|
| PostgreSQL | `jdbc:postgresql://host:5432/database` | `postgresql://host:5432/database`<br>`postgres://user:pass@host:5432/database`<br>`pg://host:5432/database` |
| MySQL | `jdbc:mysql://host:3306/database` | `mysql://host:3306/database` |
| MariaDB | `jdbc:mariadb://host:3306/database` | `mariadb://host:3306/database` |
| SQL Server | `jdbc:sqlserver://host:1433;databaseName=database` | `sqlserver://host:1433;databaseName=database` |
| SQLite | `jdbc:sqlite:/path/to/file.db` | `sqlite:/path/to/file.db`, `file:...` |

Query parameters are passed through to the driver, so
`jdbc:postgresql://host/db?sslmode=require` works. On SQL Server the JDBC
`;key=value` properties are translated, and `databaseName` becomes the driver's
`database`.

The port may be left out and defaults to 5432, 3306 or 1433. Credentials in the
url are used when `--user` and `--password` are not given. `--pass` is accepted
as a shorthand for `--password`, so a run needs no config file at all:

```bash
gofly info --url=mysql://localhost:3306/artypistdb --user=myuser --pass=mypass
```

## Where the schema history lives

| Database | Default location |
|---|---|
| PostgreSQL | schema `gofly`, table `gofly_schema_history` |
| SQL Server | schema `gofly`, table `gofly_schema_history` |
| MySQL / MariaDB | alongside your tables, table `gofly_schema_history` |
| SQLite | alongside your tables, table `gofly_schema_history` |

On MySQL a schema *is* a database, so giving the history one of its own would
mean `CREATE DATABASE` and privileges the migration user rarely has. Set
`--goflySchema=gofly` if you want it anyway. SQLite has no schemas at all.

Both names are configurable, with `--goflySchema` and `--goflyTable`. Flyway's
`--table` sets the same thing as `--goflyTable`.

### A note on PostgreSQL and search_path

PostgreSQL's stock `search_path` is `"$user", public`. That means a schema named
after the connecting user silently becomes the current schema — and gofly
creates a schema called `gofly`. If the login is *also* called `gofly`,
migrations would quietly start landing in the history schema.

gofly resolves the migration schema by walking the search path and skipping its
own, then pins the session to the result before anything runs. You do not need
to do anything about this; it is documented because the failure mode is subtle
and the fix is easy to undo by accident.

## A worked example

An application that ships its own `gofly.conf`:

```properties
gofly.locations=filesystem:/app/sql
gofly.sqlMigrationSeparator=_
gofly.baselineVersion=10
gofly.baselineOnMigrate=true
gofly.group=true
```

with the connection supplied by the environment at deploy time:

```sh
export GOFLY_URL="jdbc:mysql://${DB_HOST}:3306/${DB_NAME}"
export GOFLY_USER="$DB_USER"
export GOFLY_PASSWORD="$DB_PASSWORD"

gofly migrate
```

and a developer overriding the target to reproduce a bug:

```sh
gofly --target=14 migrate
```
