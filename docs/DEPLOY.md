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

## Release

Pushing a `v*` tag is the only publish step. A tag such as `v1.2.3`
creates a GitHub Release and a container image. Pushes to `main` do
not publish either one.

```bash
git tag v1.2.3
git push origin v1.2.3
```

Each archive contains the `wharf` binary, `LICENSE`, and `README.md`.
Linux and macOS archives are `.tar.gz`. Windows archives are `.zip`.
The names look like `wharf_1.2.3_linux_amd64.tar.gz`.

| OS | amd64 | arm64 |
| --- | --- | --- |
| linux | yes | yes |
| darwin | yes | yes |
| windows | yes | yes |

The same tag is pushed to `bravo68web/wharf` and
`ghcr.io/bravo68web/wharf`. One tag covers `linux/amd64` and
`linux/arm64`. A stable tag also publishes `1.2.3`, `1.2`, `1`, and
`latest`. A prerelease tag such as `v1.2.3-rc.1` publishes
`1.2.3-rc.1` and leaves `latest` where it is.

```bash
docker run --rm -p 5000:5000 \
  -e REGISTRY_SESSION_SECRET='replace-with-a-secret-of-32-bytes' \
  -e REGISTRY_ADMIN_USER='admin' \
  -e REGISTRY_ADMIN_PASSWORD='replace-with-a-long-password' \
  -v wharf-data:/var/lib/registry \
  bravo68web/wharf:1.2.3
```

`ghcr.io/bravo68web/wharf:1.2.3` takes the same flags. The image still
reads `/etc/registry/config.yml` and still requires
`REGISTRY_SESSION_SECRET`.

Create the public Docker Hub repository `bravo68web/wharf` before the
first tag. Set the GitHub Actions secrets `DOCKERHUB_USERNAME` and
`DOCKERHUB_TOKEN`. The token is a read-write access token. The first
GHCR push creates a private package. Open that package, link it to
this repository, and set the visibility to public before other people
can pull it.
