#
# gofly - a small, single binary Flyway clone
#
BINARY      ?= gofly
BUILD_DIR   ?= build
VERSION_IN_SOURCE ?= $(shell grep -oP 'const Version = "\K[^"]+' main.go)
VERSION           ?=
LDFLAGS     ?= -s -w

# CGO stays off so the binary is fully static and cross compiles anywhere
export CGO_ENABLED = 0

# throwaway databases for the integration tests
NETWORK        ?= gofly-test-net
PG_CONTAINER   ?= gofly-test-pg
MYSQL_CONTAINER ?= gofly-test-mysql
MSSQL_CONTAINER ?= gofly-test-mssql

PG_PORT    ?= 55433
MYSQL_PORT ?= 33307
MSSQL_PORT ?= 14434

DB_NAME  ?= goflytest
DB_USER  ?= gofly
DB_PASS  ?= goflypass
SA_PASS  ?= _asdfASDF123

FLYWAY_IMAGE ?= flyway/flyway:10

# the environment the compatibility harness reads. Two urls per server: gofly
# runs here and reaches the database through a published port, flyway runs in a
# container and reaches it by name on the docker network.
E2E_RUN = go test -tags e2e ./test/e2e/ -count=1 -v -timeout 30m

E2E_ENV = GOFLY_E2E_FLYWAY_IMAGE="$(FLYWAY_IMAGE)" \
          GOFLY_E2E_DOCKER_NETWORK="$(NETWORK)"

E2E_PG = GOFLY_E2E_PG_URL="jdbc:postgresql://127.0.0.1:$(PG_PORT)/$(DB_NAME)" \
         GOFLY_E2E_PG_FLYWAY_URL="jdbc:postgresql://$(PG_CONTAINER):5432/$(DB_NAME)" \
         GOFLY_E2E_PG_USER="$(DB_USER)" \
         GOFLY_E2E_PG_PASSWORD="$(DB_PASS)"

E2E_MYSQL = GOFLY_E2E_MYSQL_URL="jdbc:mysql://127.0.0.1:$(MYSQL_PORT)/$(DB_NAME)" \
            GOFLY_E2E_MYSQL_FLYWAY_URL="jdbc:mysql://$(MYSQL_CONTAINER):3306/$(DB_NAME)?allowPublicKeyRetrieval=true&useSSL=false" \
            GOFLY_E2E_MYSQL_USER="root" \
            GOFLY_E2E_MYSQL_PASSWORD="$(DB_PASS)"

E2E_MSSQL = GOFLY_E2E_MSSQL_URL="jdbc:sqlserver://127.0.0.1:$(MSSQL_PORT);databaseName=$(DB_NAME);encrypt=disable" \
            GOFLY_E2E_MSSQL_FLYWAY_URL="jdbc:sqlserver://$(MSSQL_CONTAINER):1433;databaseName=$(DB_NAME);encrypt=false;trustServerCertificate=true" \
            GOFLY_E2E_MSSQL_USER="sa" \
            GOFLY_E2E_MSSQL_PASSWORD="$(SA_PASS)"

.PHONY: all build build-all release test test-coverage test-integration \
        test-e2e test-e2e-sqlite test-e2e-postgres test-e2e-mysql test-e2e-mssql \
        lint fmt clean db-up db-up-postgres db-up-mysql db-up-mssql db-down help

all: lint test build

## build: compile for the host platform
build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .
	@echo "built $(BUILD_DIR)/$(BINARY) $(VERSION_IN_SOURCE)"

## build-all: cross compile for linux, macos and windows
build-all:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe .
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-arm64.exe .
	@ls -lh $(BUILD_DIR)/

## release: publish a github release from this commit (needs the gh cli)
##          usage: make release VERSION=v0.2.0
release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=v0.2.0"; exit 1; }
	@declared="v$(VERSION_IN_SOURCE)"; \
	if [ "$$declared" != "$(VERSION)" ]; then \
	  echo "main.go declares $$declared but you asked for $(VERSION)."; \
	  echo "Update 'const Version' in main.go, commit, then try again."; \
	  exit 1; \
	fi
	gh workflow run release.yml -f version=$(VERSION)
	@echo "release workflow started; watch it with: gh run watch"

## test: run the unit tests and the sqlite backed end to end ones
test:
	go test ./... -count=1

## test-coverage: same, with a coverage report
test-coverage:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1
	@echo "run 'go tool cover -html=coverage.out' for the annotated source"

## test-integration: run everything against real postgres, mysql and sql server
test-integration: db-up
	GOFLY_TEST_PG_URL="jdbc:postgresql://127.0.0.1:$(PG_PORT)/$(DB_NAME)" \
	GOFLY_TEST_PG_USER="$(DB_USER)" \
	GOFLY_TEST_PG_PASSWORD="$(DB_PASS)" \
	GOFLY_TEST_MYSQL_URL="jdbc:mysql://127.0.0.1:$(MYSQL_PORT)/$(DB_NAME)" \
	GOFLY_TEST_MYSQL_USER="root" \
	GOFLY_TEST_MYSQL_PASSWORD="$(DB_PASS)" \
	GOFLY_TEST_MSSQL_URL="jdbc:sqlserver://127.0.0.1:$(MSSQL_PORT);databaseName=master;encrypt=disable" \
	GOFLY_TEST_MSSQL_USER="sa" \
	GOFLY_TEST_MSSQL_PASSWORD="$(SA_PASS)" \
	go test -tags integration ./lib/ -count=1 -v

## test-e2e: compare gofly against real flyway on every supported database
test-e2e: db-up flyway-image
	$(E2E_ENV) $(E2E_PG) $(E2E_MYSQL) $(E2E_MSSQL) $(E2E_RUN)

## test-e2e-sqlite: the same comparison against sqlite only, no servers needed
test-e2e-sqlite: flyway-image
	$(E2E_ENV) $(E2E_RUN)

## test-e2e-postgres: the comparison against postgres only
test-e2e-postgres: flyway-image
	$(E2E_ENV) GOFLY_E2E_SKIP_SQLITE=1 $(E2E_PG) $(E2E_RUN)

## test-e2e-mysql: the comparison against mysql only
test-e2e-mysql: flyway-image
	$(E2E_ENV) GOFLY_E2E_SKIP_SQLITE=1 $(E2E_MYSQL) $(E2E_RUN)

## test-e2e-mssql: the comparison against sql server only
test-e2e-mssql: flyway-image
	$(E2E_ENV) GOFLY_E2E_SKIP_SQLITE=1 $(E2E_MSSQL) $(E2E_RUN)

flyway-image:
	@docker image inspect $(FLYWAY_IMAGE) >/dev/null 2>&1 || docker pull $(FLYWAY_IMAGE)

## lint: gofmt and go vet
lint:
	@unformatted=$$(gofmt -l . | grep -v '^tmp-' || true); \
	if [ -n "$$unformatted" ]; then echo "not gofmt'ed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go vet -tags integration ./lib/
	go vet -tags e2e ./test/e2e/

## fmt: reformat everything
fmt:
	gofmt -w main.go main_test.go lib/ test/e2e/

## db-up: start every throwaway database the compatibility harness needs
db-up: db-up-postgres db-up-mysql db-up-mssql

db-network:
	@docker network inspect $(NETWORK) >/dev/null 2>&1 || docker network create $(NETWORK)

## db-up-postgres: start a throwaway postgres
db-up-postgres: db-network
	@docker inspect $(PG_CONTAINER) >/dev/null 2>&1 || docker run -d --rm --name $(PG_CONTAINER) \
	  --network $(NETWORK) \
	  -e POSTGRES_DB=$(DB_NAME) -e POSTGRES_USER=$(DB_USER) -e POSTGRES_PASSWORD=$(DB_PASS) \
	  -p $(PG_PORT):5432 postgres:16-alpine
	@echo "waiting for postgres..."
	@for i in $$(seq 1 120); do \
	  docker exec $(PG_CONTAINER) pg_isready -U $(DB_USER) -d $(DB_NAME) >/dev/null 2>&1 && \
	    { echo "postgres ready after $${i}s"; exit 0; }; \
	  sleep 1; \
	done; echo "postgres did not come up in time"; exit 1

## db-up-mysql: start a throwaway mysql
db-up-mysql: db-network
	@docker inspect $(MYSQL_CONTAINER) >/dev/null 2>&1 || docker run -d --rm --name $(MYSQL_CONTAINER) \
	  --network $(NETWORK) \
	  -e MYSQL_DATABASE=$(DB_NAME) -e MYSQL_ROOT_PASSWORD=$(DB_PASS) \
	  -p $(MYSQL_PORT):3306 mysql:8
	@echo "waiting for mysql..."
	@for i in $$(seq 1 180); do \
	  docker exec $(MYSQL_CONTAINER) mysqladmin ping -uroot -p$(DB_PASS) >/dev/null 2>&1 && \
	    { echo "mysql ready after $${i}s"; exit 0; }; \
	  sleep 1; \
	done; echo "mysql did not come up in time"; exit 1

## db-up-mssql: start a throwaway sql server
db-up-mssql: db-network
	@docker inspect $(MSSQL_CONTAINER) >/dev/null 2>&1 || docker run -d --rm --name $(MSSQL_CONTAINER) \
	  --network $(NETWORK) \
	  -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD=$(SA_PASS) \
	  -p $(MSSQL_PORT):1433 mcr.microsoft.com/mssql/server:2022-latest
	@echo "waiting for sql server..."
	@for i in $$(seq 1 180); do \
	  docker exec $(MSSQL_CONTAINER) /opt/mssql-tools18/bin/sqlcmd \
	    -S localhost -U sa -P '$(SA_PASS)' -C -Q "SELECT 1" >/dev/null 2>&1 && break; \
	  sleep 1; \
	  if [ $$i -eq 180 ]; then echo "sql server did not come up in time"; exit 1; fi; \
	done
	@docker exec $(MSSQL_CONTAINER) /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P '$(SA_PASS)' -C \
	  -Q "IF DB_ID('$(DB_NAME)') IS NULL CREATE DATABASE [$(DB_NAME)]" >/dev/null
	@echo "sql server ready"

## db-down: stop them again
db-down:
	-docker stop $(PG_CONTAINER) $(MYSQL_CONTAINER) $(MSSQL_CONTAINER)
	-docker network rm $(NETWORK)

## clean: remove the build output
clean:
	rm -rf $(BUILD_DIR) coverage.out

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
