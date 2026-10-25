BIN_DIR := bin
BINARY  := $(BIN_DIR)/wharf
CONFIG  ?= testdata/config.yml

.PHONY: build site test lint run dev clean sqlc

# build embeds the Vite dashboard (-tags embed). `go test` stays untagged,
# so the suite does not need Node. `make dev` serves the UI from Vite and
# proxies /dashboard/api to the registry (default http://[::1]:5000, override
# with REGISTRY_DEV_PROXY). Start the registry before `make dev`.
build: site
	go build -tags embed -o $(BINARY) ./cmd/wharf

site:
	cd site && pnpm install --frozen-lockfile && pnpm build

test:
	go test ./...

dev:
	cd site && pnpm dev

# Regenerate internal/db/*.sql.go from internal/db/queries/*.sql.
sqlc:
	sqlc generate

lint:
	go vet ./...

run: build
	$(BINARY) --config $(CONFIG)

clean:
	rm -rf $(BIN_DIR)
