# Wharf

Wharf is a private OCI registry. One Go process serves
the distribution API and the dashboard at `/dashboard`.

## Goals

Run it with a SQLite file and blobs on disk. S3 is there when you set
it. `make build` embeds the React dashboard, so there is no second
process. A blob is stored once, under its digest. Repositories share a
layer by pointing at that digest.

## Features

- Push and pull: chunked uploads, cross-repository mounts, range
  requests, and referrers
- Dashboard for repositories, tags, manifests, layer history, retag,
  and delete
- Publish a repository so `docker pull` works without a login
- Password login, personal access tokens, and service-account tokens
- Garbage collection from the dashboard or on a timer

## Quick start

```bash
git clone https://github.com/bravo68web/wharf.git
cd wharf
```

`--config` is required. A starting file is in
[Configuration](docs/CONFIG.md).

```bash
export REGISTRY_SESSION_SECRET='replace-with-a-secret-of-32-bytes'
export REGISTRY_ADMIN_USER='admin'
export REGISTRY_ADMIN_PASSWORD='replace-with-a-long-password'
make build
./bin/wharf --config config.yml
```

The dashboard is at `http://localhost:5000/dashboard/`. Sign in with
the bootstrap user. Set both admin variables, or neither. An existing
user is never rewritten.

```bash
docker build -t wharf .
docker compose -f deploy/docker-compose.yml up --build
```

The compose stack is local development, with MinIO. Change its
environment values before the published port is reachable by anyone
else. `WHARF_HOST_PORT` moves the host port.

`dev` is the default branch. A preview tag on `dev` and a stable tag
on `main` publish the GitHub Release, `bravo68web/wharf`, and
`ghcr.io/bravo68web/wharf`. See [Deploy](docs/DEPLOY.md#release).

## Documentation

| Guide | What it covers |
| --- | --- |
| [Deploy](docs/DEPLOY.md) | Binary, image, and the local compose stack |
| [Configuration](docs/CONFIG.md) | The YAML file and the rules around it |
| [Registry](docs/REGISTRY.md) | `docker login`, push, pull, and publish |
| [Accounts](docs/ACCOUNTS.md) | Administrator, people, service accounts, tokens |
| [Dashboard](docs/DASHBOARD.md) | Pages at `/dashboard` |
| [Storage](docs/STORAGE.md) | Blobs, delete, and collection |
| [API](docs/API.md) | `/v2`, `/token`, and `/dashboard/api` |
| [Development](docs/DEVELOP.md) | Tests, Vite, and the conformance scripts |
| [Product](PRODUCT.md) | What this registry is for, and what it is not |
| [Design](DESIGN.md) | Dashboard visual system |

## License

MIT. See [LICENSE](LICENSE). Copyright (c) 2026 Jyotirmoy
Bandyopadhayaya.
