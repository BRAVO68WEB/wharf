# better-docker-registry

better-docker-registry is a private OCI registry and a dashboard in one
Go process. `docker push` and `docker pull` talk to the registry API.
The same process serves the dashboard at `/dashboard`, so you can see
what is stored, how big it is, and what you can delete.

The dashboard is a React application. A production build compiles it
into the registry binary. There is no separate frontend process at
runtime. Blobs live on the local filesystem or in an S3-compatible
bucket. A blob is stored once, under its digest, and repositories
share a layer by pointing at that digest.

## What you can do

You can push and pull images with the Docker client, then open the
dashboard to browse them.

- Push and pull through the OCI Distribution API, including chunked
  uploads, cross-repository blob mounts, and range requests.
- Browse repositories, tags, manifests, and the full image-history
  command for each layer.
- Copy a tag to another repository and tag, or delete a tag or a
  manifest.
- See how much space unreferenced blobs use, preview a collection, and
  delete them. A collection can also run on a timer.
- Publish a repository so anyone can pull it, without a login.
- Sign in to the dashboard and to `docker login` with a password, a
  personal access token, or a service-account token.
- Choose a light theme, a dark theme, or the system theme. The browser
  remembers the choice.

## What it does not do

This is a registry for one person or a small server. It is not a
hosted multi-tenant product.

- It does not terminate TLS. Put a proxy in front of it when clients
  must use HTTPS.
- It has no per-repository roles, quotas, LDAP, or single sign-on.
- It does not cache pulls from another registry, replicate to another
  registry, scan images, or verify signatures.
- It does not clone the storage layout of the official registry. The
  metadata is SQLite. The blobs are content-addressed files or
  objects.

## Requirements

A source build needs Go 1.25.4 or later, Node.js 24, and pnpm 9. The
container build installs those toolchains itself. Docker is enough
when you only want the image.

`docker push` to an `http://` registry needs that host in Docker's
`insecure-registries` list. The registry process does not speak TLS.
Put a TLS proxy in front of it, and set `server.public_url` to that
`https` origin. The session cookie's `Secure` flag and the bearer
token realm both follow that origin, so `docker login` does not fall
back to `http`. An empty `public_url` keeps the local HTTP behavior.

## Run it on a machine

Copy the configuration below to `config.yml` and change the three
paths. `database.path` must be an absolute path. A relative path makes
the SQLite driver reject the file.

```yaml
server:
  port: 5000
  read_timeout: 0s
  write_timeout: 0s
  max_upload_size: 0
  public_url: ""

storage:
  type: filesystem
  path: /var/lib/registry/blobs

database:
  path: /var/lib/registry/registry.db

auth:
  jwt:
    private_key_path: /var/lib/registry/jwt_private.pem
    issuer: better-docker-registry
    service: registry.docker.io
    token_ttl: 15m
  session:
    secret_env: REGISTRY_SESSION_SECRET
    ttl: 24h

gc:
  batch_size: 256
  interval: 0s

logging:
  level: info
```

Create the directories, export a session secret, and build the binary.
`make build` installs the dashboard dependencies, builds the React
app, and compiles the registry with those files embedded.

```sh
mkdir -p /var/lib/registry/blobs
export REGISTRY_SESSION_SECRET='replace-with-a-long-random-secret'
export REGISTRY_ADMIN_USER='admin'
export REGISTRY_ADMIN_PASSWORD='replace-with-a-long-password'
make build
./bin/registry --config config.yml
```

Open `http://localhost:5000/dashboard/` and sign in with the bootstrap
username and password. The registry listens on `server.port` unless
you pass `--addr`. On macOS, AirPlay Receiver often already owns port
5000 on IPv4. Listen on another port, or on `[::1]:5000`:

```sh
./bin/registry --config config.yml --addr '[::1]:5000'
```

`testdata/config.yml` is a worked example of the same file. Its
`database.path` points at one developer's checkout. Change that path
before you use the file.

<!-- prettier-ignore -->
> [!IMPORTANT]
> Set `REGISTRY_ADMIN_USER` and `REGISTRY_ADMIN_PASSWORD` together, or
> leave both unset. The process exits when only one of them is set.
> An existing user is never rewritten on a later start.

The signing key is created on first start when
`auth.jwt.private_key_path` does not exist. The file mode is `0600`.
Mount your own PEM when issued tokens must stay valid across a new
data directory.

## Run it in Docker

Build from the repository root. The image is distroless, runs as a
non-root user, and includes `curl` only so the image health check can
call `/healthz`.

```sh
docker build -t bdr .
docker run --rm -p 5000:5000 \
  -e REGISTRY_SESSION_SECRET='replace-with-a-long-random-secret' \
  -e REGISTRY_ADMIN_USER='admin' \
  -e REGISTRY_ADMIN_PASSWORD='replace-with-a-long-password' \
  -v bdr-data:/var/lib/registry \
  bdr
```

The image reads `/etc/registry/config.yml`, which stores blobs and
SQLite under `/var/lib/registry`. Mount a volume there or the data
disappears with the container. Pass `--config` after the image name
when you mount a different file.

`deploy/docker-compose.yml` starts the registry against MinIO. From
`deploy/`:

```sh
docker compose up --build
```

That stack is for local development. Its environment values are
development-only. Change them before the published port is reachable
by anyone else. `BDR_HOST_PORT` changes the host port when 5000 is
taken:

```sh
BDR_HOST_PORT=5001 docker compose up --build
```

`docker compose down -v` deletes the named volumes, including the
registry database and the MinIO data.

## Sign in

The same account opens the dashboard and `docker login`. Until an
account exists, push and pull are refused. A credential-less pull is
the exception, and only for a published repository.

1. Add the registry host to Docker's `insecure-registries` when the
   URL is `http://`.
2. Sign in. The password is the account password, or a token you
   created in the dashboard.

```sh
docker login localhost:5000
```

A token from a script uses the account name and the token as the
password:

```sh
printf '%s' "$TOKEN" | docker login localhost:5000 \
  -u ci --password-stdin
```

`auth.jwt.service` is the `service` value Docker is told to send when
it asks for a bearer token. The sample configs use
`registry.docker.io`. That value does not have to match the host in
`docker login`. The challenge on `GET /v2/` names the configured
service, and the token endpoint rejects any other service.

`docker login` with no repository scope checks the password or token
and stores a bearer token. Push and pull then send that token.

## Publish a repository

A new repository is unpublished. A later push does not change that.
Signed-in users see every repository. Signed-out visitors see only
published names, and those names are not links into the tag pages.

1. Sign in to the dashboard.
2. On **Repositories**, choose **Publish** on the repository.
3. Leave `server.public_url` empty, or set it to the origin clients
   use, such as `https://registry.example.com`.

When `public_url` is set, the dashboard shows a `docker pull` line for
a repository the viewer is allowed to see. The host in that line is
the URL host and port, with no scheme. An empty `public_url` shows no
pull line and does not guess `localhost`.

Anyone can then pull a published repository without `docker login`:

```sh
docker pull registry.example.com/library/nginx:latest
```

Anonymous pull is only `pull` on that repository. It does not list the
catalog, push, delete, or read a blob the repository does not
reference. A request that sends a username and a bad password is
rejected even when the repository is published.

**Unpublish** hides the repository from the public page and stops
anonymous pull. People who are signed in can still pull it.

## Accounts and tokens

The bootstrap environment variables create the only administrator.
The dashboard cannot create another administrator. You cannot delete
the account you are signed in as, and you cannot delete the last
administrator.

On **Accounts**, an administrator creates people and service accounts.
**Create account** opens the form.

- A person signs in to the dashboard and to `docker login` with a
  password of at least 8 characters.
- A service account has no dashboard password. Creating it shows a
  token once. **New token** replaces that token, and the previous
  token stops working immediately. Use the token as the `docker login`
  password.

On **Tokens**, a signed-in person creates a personal access token.
Name it, and copy it when it is shown. The plaintext is not stored.
`docker login` accepts the token as the password. **Revoke** stops
that token. The password, and any other tokens, keep working.

A username starts with a letter or digit. The rest is letters,
digits, `.`, `_`, or `-`, up to 63 characters in total.

## Use the dashboard

`GET /` redirects to `/dashboard/`. Sign in from **Sign in**. The
theme button in the top bar switches between light and dark. Until
you pick one, the page follows the system theme.

**Repositories** lists names, tags, size, and the newest tag. Search
matches a substring of the name. Open a repository to see its tags.
Open a tag to see the manifest, the layer digests, and the full
history command. Copy a tag with the retag form. **Delete** on a tag
removes that tag. **Delete manifest** removes the manifest and every
tag that points at it. Blobs stay on disk until a collection runs.

**Storage** counts referenced and unreferenced blobs. **Dry run**
lists what a collection would delete. **Delete unreferenced blobs**
deletes them. While a collection runs, registry writes answer `503`
and pulls keep working.

**Accounts** is visible to the administrator. **Tokens** is visible
to every signed-in person.

## Configuration

The process requires `--config`. Unknown YAML keys fail startup, so a
typo does not silently use a zero value. Secrets are environment
variables. The YAML file names the variable. It never holds the
secret.

`server.read_timeout` and `server.write_timeout` stay `0s` so a long
blob upload or download is not cut off. Header reads still stop after
10 seconds. `server.max_upload_size` is the largest blob upload in
bytes. `0` means no limit.

| Field | What it sets |
| --- | --- |
| `server.port` | Listen port, from 1 to 65535. `--addr` overrides the whole address. |
| `server.public_url` | Absolute `http` or `https` origin with no path, query, fragment, or user info. Empty hides pull lines. |
| `storage.type` | `filesystem` or `s3`. |
| `storage.path` | Blob directory for the filesystem backend. |
| `storage.s3.endpoint` | S3-compatible base URL. Empty uses AWS for the region. |
| `storage.s3.region` | Required for `s3`. |
| `storage.s3.bucket` | Required for `s3`. The bucket must already exist. |
| `storage.s3.access_key_env` | Name of the env var with the access key. Empty uses the AWS SDK default chain. |
| `storage.s3.secret_key_env` | Name of the env var with the secret key. Set both names, or neither. |
| `storage.s3.force_path_style` | Path-style addressing. MinIO needs this. |
| `database.path` | Absolute path of the SQLite file. |
| `auth.jwt.private_key_path` | PEM RSA key. Created if missing. |
| `auth.jwt.issuer` | JWT `iss` claim. |
| `auth.jwt.service` | JWT audience and the `service` Docker must send to `/token`. |
| `auth.jwt.token_ttl` | Bearer-token lifetime. Greater than zero. Example: `15m`. |
| `auth.session.secret_env` | Name of the env var with the session secret. The value must be at least 32 bytes. |
| `auth.session.ttl` | Dashboard session lifetime. Example: `24h`. |
| `auth.dev_anonymous` | Skip bearer checks on `/v2`. Development only. |
| `gc.batch_size` | Blobs deleted per batch. Greater than zero. |
| `gc.interval` | Timer for collection. `0s` leaves it manual. A negative value is rejected. |
| `logging.level` | `debug`, `info`, `warn`, or `error`. |

Durations are Go duration strings, such as `30s` or `5m`, or an
integer number of seconds.

<!-- prettier-ignore -->
> [!WARNING]
> `auth.dev_anonymous: true` serves `/v2` with no bearer token. The
> dashboard still requires a session. Do not set this on a registry
> other people can reach.

## Blob storage

The filesystem backend writes each blob under `storage.path`, keyed by
digest. The S3 backend uses the digest as the object key. It does not
create or delete the bucket.

Pushing the same layer into a second repository does not write a
second copy. The repository gains a reference to the existing digest.
Deleting a tag or a manifest drops references. The blob is removed
only when a collection finds no references left.

## Garbage collection

Collection is manual unless `gc.interval` is greater than zero. On
**Storage**, **Dry run** changes nothing. **Delete unreferenced
blobs** deletes unreferenced blobs in batches of `gc.batch_size`.

A positive `gc.interval` starts the same collection from the process.
A tick that finds a collection already running is skipped. The
dashboard shows the interval and the time of the last successful run.

During a collection, a push or delete on `/v2` answers `503`. A pull
is still served. `SIGINT` or `SIGTERM` marks the process not ready, so
`/readyz` answers `503`, then waits up to 30 seconds for in-flight
requests to finish.

## HTTP endpoints

The registry API is the OCI distribution surface Docker already
speaks.

- `GET /v2/` checks the version. A missing or bad token answers `401`
  with a bearer challenge.
- `GET` and `POST /token` issue a bearer token. The `service`
  parameter must equal `auth.jwt.service`.
- `GET /v2/_catalog` and `GET /v2/<name>/tags/list` list repositories
  and tags. `n` sets the page size and `last` continues after an
  entry. An anonymous token cannot list the catalog.
- Blob, upload, manifest, and referrer routes live under
  `/v2/<name>/`. Repository names may contain slashes.
- `GET /v2/_keys` publishes the public signing keys.

Health and the dashboard sit beside that API.

- `GET /healthz` answers `200` while the process is up.
- `GET /readyz` answers `200` when SQLite and the blob store both
  answer, and `503` while dependencies are down or the process is
  shutting down.
- `GET /` redirects to `/dashboard/`.
- `GET /dashboard/` is the dashboard. Hashed assets are cached.
  Client routes fall back to `index.html`.
- `/dashboard/api/` is the dashboard JSON API. It is not served by
  the HTML fallback.

Each request log line includes the method, path, status, duration,
and request ID. The `Authorization` header is not logged. `/healthz`
and `/readyz` are not logged. Send `X-Request-ID` to reuse an inbound
ID. Otherwise the registry generates one.

## Develop

`make test` runs `go test ./...` without embedding the dashboard, so
the suite does not need Node.js. `make lint` runs `go vet ./...`.

Start the registry, then start the Vite dev server in another
terminal. Vite serves the dashboard at
`http://localhost:5174/dashboard/` and proxies `/dashboard/api`,
`/healthz`, and `/readyz` to the registry.

```sh
export REGISTRY_SESSION_SECRET='replace-with-a-long-random-secret'
./bin/registry --config config.yml
make dev
```

The proxy target defaults to `[::1]:5000`. Override it with
`REGISTRY_DEV_HOST` and `REGISTRY_DEV_PORT`.

An untagged `go build` embeds a stub page that says the dashboard
assets are not built. Ship with `make build` or
`go build -tags embed -o bin/registry ./cmd/registry` after
`pnpm build` in `site/`.

`make sqlc` regenerates `internal/db` from `internal/db/queries` and
`sqlc.yaml`. Do not edit the generated `*.sql.go` files.
`scripts/run-conformance.sh` runs the OCI distribution conformance
suite against a filesystem or MinIO backend.
`scripts/e2e-docker.sh` runs a Docker client round trip.

## Layout

The directories below are the ones you edit. Generated SQL and the
Vite `site/out` build are produced by the commands above.

- `cmd/registry` starts the process, migrates SQLite, and mounts the
  routes.
- `internal/api` is the `/v2` registry API.
- `internal/auth` issues bearer tokens and dashboard sessions.
- `internal/dashboard` is the JSON API behind the React application.
- `internal/storage` stores blobs on disk or on S3.
- `internal/gc` plans and sweeps unreferenced blobs.
- `internal/db` holds migrations, queries, and the generated data
  access code.
- `site` is the React dashboard.
- `deploy` holds the image Dockerfile, the image config, and the
  MinIO compose stack.
- `testdata/config.yml` is a local configuration example.

## Next steps

The repository keeps two short notes beside this guide. Read them
when you are changing the product or the visual design, not when you
are only trying to run the registry.

- [PRODUCT.md](PRODUCT.md) records what this registry is for and what
  is intentionally out of scope.
- [DESIGN.md](DESIGN.md) records the dashboard's visual system.
