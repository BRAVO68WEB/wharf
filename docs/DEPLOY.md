# Deploy

This page is how you run the process. The YAML file is in
[Configuration](CONFIG.md). `docker login` and publish are in
[Registry](REGISTRY.md).

## Source build

A source build needs Go 1.26 or later, Node.js 24, and pnpm 9.
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

`dev` is the default branch. Day-to-day work lands there. `main` is
protected. A direct push is rejected. The only way onto `main` is a
pull request from `dev`, with the Test, From dev, and Conformance
checks green.

A preview tag is cut from `dev`. A stable tag is cut from `main`,
after that commit has been merged. Pushes that are not tags do not
publish a release or an image.

```bash
git checkout dev
git tag v1.2.3-rc.1
git push origin v1.2.3-rc.1
```

Open a pull request from `dev` into `main` and merge it. Then:

```bash
git checkout main
git pull
git tag v1.2.3
git push origin v1.2.3
```

`v1.2.3-preview.1` is the other preview form. A preview is a GitHub
prerelease. It does not move `latest`, `1.2`, or `1`. A stable tag
does.

Each archive contains the `wharf` binary, `LICENSE`, and `README.md`.
Linux and macOS archives are `.tar.gz`. Windows archives are `.zip`.
The names look like `wharf_1.2.3_linux_amd64.tar.gz`. The release also
uploads an SBOM for each archive and a checksum file. GitHub signs a
provenance attestation for those files. Verify one with
`gh attestation verify FILE -R BRAVO68WEB/wharf`.

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

`ghcr.io/bravo68web/wharf:1.2.3` takes the same flags. Both registries
store the image with max-level provenance and an SBOM, and GitHub
signs a second provenance statement for each registry. Inspect one
with `docker buildx imagetools inspect ghcr.io/bravo68web/wharf:1.2.3`.
The image still reads `/etc/registry/config.yml` and still requires
`REGISTRY_SESSION_SECRET`.

Create the public Docker Hub repository `bravo68web/wharf` before the
first tag. Set the GitHub Actions secrets `DOCKERHUB_USERNAME` and
`DOCKERHUB_TOKEN`. The token is a read-write access token. The first
GHCR push creates a private package. Open that package, link it to
this repository, and set the visibility to public before other people
can pull it.
