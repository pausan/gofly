# gofly is a drop-in replacement for Flyway

**SQL migrations in a single static binary, without the JVM.**

gofly is written in Go and supports **PostgreSQL, MySQL / MariaDB, SQL Server
and SQLite**. To switch from Flyway, change `flyway` to `gofly` in your
migration command. Your SQL files and configuration can stay as they are.

## Install and run

Download a binary from [Releases](https://github.com/pausan/gofly/releases), or
install with Go:

```sh
go install github.com/pausan/gofly@latest
```

Put a migration such as `V1__Create_users.sql` in `./sql`, then run:

```sh
gofly -url=jdbc:postgresql://localhost:5432/mydb \
      -user=admin -password=secret \
      -locations=filesystem:./sql migrate
```

Options accept both `-key=value` and `--key=value`; JDBC URLs also work without
the `jdbc:` prefix. You can use a config file, environment variables or command
line options.

Use `info` to see migration status. Other commands: `validate`, `undo`,
`baseline` and `repair`. Run `gofly` without arguments for help.

## Switching from Flyway

gofly supports versioned (`V1__Create_users.sql`), repeatable (`R__Views.sql`)
and undo (`U1__Create_users.sql`) migrations. **It uses Flyway's checksums and
history layout**, and reuses an existing Flyway history table by default.

Your `flyway.conf` properties and `FLYWAY_*` environment variables still work.
They print a deprecation warning; use `gofly.*` and `GOFLY_*` for new setups.

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
make test-e2e          # compare against real Flyway on all four databases
make test-integration  # PostgreSQL, MySQL and SQL Server in Docker
make lint              # gofmt and go vet
```

See the [compatibility harness](test/e2e/README.md) for test details.

MIT License. See [LICENSE](LICENSE).
