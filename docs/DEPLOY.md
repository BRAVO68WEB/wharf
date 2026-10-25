# Deploy

This page is how you run the process. The YAML file is in
[Configuration](CONFIG.md). `docker login` and publish are in
[Registry](REGISTRY.md).

## Source build

A source build needs Go 1.25.4 or later, Node.js 24, and pnpm 9.
`make build` installs the dashboard dependencies, builds the React
app, and compiles `bin/wharf` with those files embedded.

```bash
export REGISTRY_SESSION_SECRET='replace-with-a-secret-of-32-bytes'
export REGISTRY_ADMIN_USER='admin'
export REGISTRY_ADMIN_PASSWORD='replace-with-a-long-password'
make build
./bin/wharf --config config.yml
```

`--config` is required. The session secret must be at least 32 bytes.
Set both admin variables, or neither. The process exits when only one
is set. An existing user is never rewritten. Account rules are in
[Accounts](ACCOUNTS.md).

The listen address is `:<server.port>`. `--addr` replaces it.

```bash
./bin/wharf --config config.yml --addr '[::1]:5000'
```

A missing signing key is created at `auth.jwt.private_key_path`, mode
`0600`. Put your own RSA PEM there when issued tokens must stay valid
across a new data directory.

The dashboard is at `http://localhost:5000/dashboard/`.

## Docker

The image build does not need Go or Node on the host.

```bash
docker build -t wharf .
docker run --rm -p 5000:5000 \
  -e REGISTRY_SESSION_SECRET='replace-with-a-secret-of-32-bytes' \
  -e REGISTRY_ADMIN_USER='admin' \
  -e REGISTRY_ADMIN_PASSWORD='replace-with-a-long-password' \
  -v wharf-data:/var/lib/registry \
  wharf
```

The baked-in config is `/etc/registry/config.yml`. Blobs and SQLite
are under `/var/lib/registry`. Without the volume, that data goes away
with the container. The process runs as uid 65532 and checks
`/healthz`.

Mount another file over `/etc/registry/config.yml`, or pass `--config`
after the image name when the file lives somewhere else.

## Compose

```bash
docker compose -f deploy/docker-compose.yml up --build
```

That file starts MinIO and the registry for local development. The
environment values in it are development-only. Change them before the
published port is reachable by anyone else.

```bash
WHARF_HOST_PORT=5001 docker compose -f deploy/docker-compose.yml up --build
```

`docker compose -f deploy/docker-compose.yml down -v` deletes the
named volumes, including the registry database and the MinIO data.

## TLS

The process does not terminate TLS. An `http://` host belongs in
Docker's `insecure-registries` list. Behind a TLS proxy, set
`server.public_url` to the `https` origin clients use. What that
origin changes is in [Configuration](CONFIG.md).
