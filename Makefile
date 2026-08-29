#
# gofly - a small, single binary Flyway clone
#
BINARY      ?= gofly
BUILD_DIR   ?= build
VERSION     ?= $(shell grep -oP 'const Version = "\K[^"]+' main.go)
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

.PHONY: all build build-all test test-coverage test-integration lint fmt clean \
        db-up db-down help

all: lint test build

## build: compile for the host platform
build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .
	@echo "built $(BUILD_DIR)/$(BINARY) $(VERSION)"

## build-all: cross compile for linux, macos and windows
build-all:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe .
	@ls -lh $(BUILD_DIR)/

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

## lint: gofmt and go vet
lint:
	@unformatted=$$(gofmt -l . | grep -v '^tmp-' || true); \
	if [ -n "$$unformatted" ]; then echo "not gofmt'ed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go vet -tags integration ./lib/

## fmt: reformat everything
fmt:
	gofmt -w main.go main_test.go lib/

## db-up: start the throwaway databases the integration tests need
db-up:
	-docker network create $(NETWORK)
	-docker run -d --rm --name $(PG_CONTAINER) --network $(NETWORK) \
	  -e POSTGRES_DB=$(DB_NAME) -e POSTGRES_USER=$(DB_USER) -e POSTGRES_PASSWORD=$(DB_PASS) \
	  -p $(PG_PORT):5432 postgres:16-alpine
	-docker run -d --rm --name $(MYSQL_CONTAINER) --network $(NETWORK) \
	  -e MYSQL_DATABASE=$(DB_NAME) -e MYSQL_ROOT_PASSWORD=$(DB_PASS) \
	  -p $(MYSQL_PORT):3306 mysql:8
	-docker run -d --rm --name $(MSSQL_CONTAINER) --network $(NETWORK) \
	  -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD=$(SA_PASS) \
	  -p $(MSSQL_PORT):1433 mcr.microsoft.com/mssql/server:2022-latest
	@echo "waiting for the databases to accept connections..."
	@for i in $$(seq 1 120); do \
	  ready=0; \
	  docker exec $(PG_CONTAINER) pg_isready -U $(DB_USER) -d $(DB_NAME) >/dev/null 2>&1 && ready=$$((ready+1)); \
	  docker exec $(MYSQL_CONTAINER) mysqladmin ping -uroot -p$(DB_PASS) >/dev/null 2>&1 && ready=$$((ready+1)); \
	  docker exec $(MSSQL_CONTAINER) /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P '$(SA_PASS)' -C -Q "SELECT 1" >/dev/null 2>&1 && ready=$$((ready+1)); \
	  if [ $$ready -eq 3 ]; then echo "ready after $${i}s"; exit 0; fi; \
	  sleep 1; \
	done; \
	echo "the databases did not come up in time"; exit 1

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
