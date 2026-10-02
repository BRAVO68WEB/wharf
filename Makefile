BIN_DIR := bin
BINARY  := $(BIN_DIR)/registry
CONFIG  ?= testdata/config.yml

.PHONY: build test lint run clean sqlc

build:
	go build -o $(BINARY) ./cmd/registry

test:
	go test ./...

# Regenerate internal/db/*.sql.go from internal/db/queries/*.sql.
sqlc:
	sqlc generate

lint:
	go vet ./...

run: build
	$(BINARY) --config $(CONFIG)

clean:
	rm -rf $(BIN_DIR)
