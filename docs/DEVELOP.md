# Development

Runtime setup is in [Deploy](DEPLOY.md). These commands are for a
checkout.

## Tests and the UI

`make test` runs `go test ./...` without embedding the dashboard, so
Node.js is not required. `make lint` runs `go vet ./...`.

Start the registry, then Vite. Vite serves
`http://localhost:5174/dashboard/` and proxies `/dashboard/api`,
`/healthz`, and `/readyz`.

```bash
export REGISTRY_SESSION_SECRET='replace-with-a-secret-of-32-bytes'
./bin/wharf --config config.yml
make dev
```

The proxy target is `[::1]:5000`. Override it with `REGISTRY_DEV_HOST`
and `REGISTRY_DEV_PORT`.

An untagged `go build` serves a stub page. Ship with `make build`.

## Generated SQL

`make sqlc` regenerates `internal/db` from `internal/db/queries` and
`sqlc.yaml`. Do not edit the generated `*.sql.go` files.

## Registry checks

```bash
scripts/run-conformance.sh filesystem
scripts/run-conformance.sh minio
scripts/e2e-docker.sh
```

`run-conformance.sh` runs the OCI Distribution Spec 1.1 suite.
`e2e-docker.sh` runs a Docker client round trip. Both build a binary
and a local config. They are not part of `make test`.

## Layout

| Path | What it is |
| --- | --- |
| `cmd/wharf` | Process startup |
| `internal/api` | `/v2` |
| `internal/auth` | Bearer tokens and dashboard sessions |
| `internal/dashboard` | `/dashboard/api` |
| `internal/storage` | Filesystem and S3 blobs |
| `internal/gc` | Collection |
| `internal/db` | SQLite migrations and queries |
| `site` | React dashboard |
| `deploy` | Image config and the MinIO compose file |
