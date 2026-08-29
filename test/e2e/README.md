# Compatibility harness

This suite is the regression net for the one property gofly sells: that it and
Flyway agree.

Every scenario runs the same migrations twice against the same engine, once with
**real Flyway in a container** and once with gofly, then compares the schema
history tables the two produced, row by row and checksum by checksum. If a
change ever makes gofly drift away from Flyway, a scenario here fails with a
side by side diff.

## Running it

```sh
make test-e2e          # postgres, mysql, sql server and sqlite
make test-e2e-sqlite   # sqlite only, no database servers needed
make db-down           # stop the throwaway containers again
```

`make test-e2e` starts throwaway PostgreSQL, MySQL and SQL Server containers on
ports of their own (55433, 33307, 14434), pulls `flyway/flyway:10` and runs
everything. Nothing else on the machine is touched.

## Running it by hand

The suite is behind the `e2e` build tag and picks its targets up from the
environment. SQLite is always included; each server appears only if its url is
exported.

Two urls are needed per server, because gofly runs on the host and reaches the
database through a published port, while Flyway runs inside a container and
reaches it by name on the docker network:

```sh
export GOFLY_E2E_DOCKER_NETWORK=gofly-test-net
export GOFLY_E2E_FLYWAY_IMAGE=flyway/flyway:10

export GOFLY_E2E_PG_URL="jdbc:postgresql://127.0.0.1:55433/goflytest"
export GOFLY_E2E_PG_FLYWAY_URL="jdbc:postgresql://gofly-test-pg:5432/goflytest"
export GOFLY_E2E_PG_USER=gofly
export GOFLY_E2E_PG_PASSWORD=goflypass

go test -tags e2e ./test/e2e/ -v
```

`GOFLY_E2E_SKIP_SQLITE=1` leaves SQLite out. `GOFLY_E2E_FLYWAY_IMAGE` pins a
different Flyway version, which is how you check gofly against a newer release.

## What is covered

| Scenario | Compared against Flyway |
|---|---|
| migrate from scratch | yes |
| incremental migrate | yes |
| migrate up to a target | yes |
| every documented version scheme, and their ordering | yes |
| repeatable migrations, re-applied when they change | yes |
| baseline, then migrate above it | yes |
| checksum mismatch refused, with the same checksums | yes |
| a migration removed after being applied | yes |
| out of order, refused and then allowed | yes |
| placeholders, and the checksum staying stable | yes |
| a failed migration and what it leaves behind | yes |
| repair, then migrating the fixed migration | yes |
| handover: Flyway migrates, gofly imports and continues | yes |
| Flyway reading the history gofly wrote | yes |
| validate against a database still managed by Flyway | yes |
| undo, and re-applying afterwards | gofly only, undo is a Teams feature |
| `-group`, all or nothing | gofly only, Flyway has no `-group` in OSS |

## Adding a scenario

Add a function to `compat_test.go`. `bothHistories` does the usual shape for
you: it runs Flyway with the arguments you give it, runs gofly with the callback
you give it, both from a clean database, and hands back the two histories for
`AssertSameHistory`.

Scenarios that need a different shape build two `Workspace` values directly; see
`TestCompatIncrementalMigrate`.

The fixtures live in `fixtures.go`, written per dialect, because the point is to
run real SQL against real engines rather than a lowest common denominator.

## What is deliberately not compared

`installed_on` and `execution_time` differ by construction and are left out of
the comparison. Everything else in the history table has to match exactly:
`installed_rank`, `version`, `description`, `type`, `script`, `checksum` and
`success`.
