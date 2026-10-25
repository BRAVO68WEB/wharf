# Registry

This page is the Docker client. Accounts and tokens are in
[Accounts](ACCOUNTS.md). The HTTP routes are in [API](API.md).

## Login

The same account opens the dashboard and `docker login`. Until an
account exists, push and pull are refused. A published repository is
the exception: pull works with no login.

An `http://` host belongs in Docker's `insecure-registries` list. The
process does not speak TLS.

```bash
docker login localhost:5000
printf '%s' "$TOKEN" | docker login localhost:5000 \
  -u ci --password-stdin
```

The password is the account password or a token. `--password-stdin`
still needs `-u`.

`auth.jwt.service` is the `service` value in the `GET /v2/` challenge.
The sample configs use `registry.docker.io`. That string does not have
to match the host in `docker login`. `/token` rejects any other
service.

`docker login` with no repository scope checks the credential and
stores a bearer token. Later push and pull send that token.

## Push and pull

```bash
docker tag alpine:latest localhost:5000/library/alpine:latest
docker push localhost:5000/library/alpine:latest
docker pull localhost:5000/library/alpine:latest
```

A push creates the repository if it is missing. A new repository is
unpublished. A later push does not change that.

## Publish

Signed-in users see every repository. Signed-out visitors see
published names only, and those names are not links into the tag
pages.

Publish from the dashboard, or `POST /dashboard/api/repos/publish`
with `{"name":"library/alpine","published":true}`. Set
`server.public_url` to the origin clients use, or leave it empty.
When it is set, the dashboard shows a `docker pull` line. The host in
that line has no scheme. An empty `public_url` shows no line and does
not guess a host.

```bash
docker pull registry.example.com/library/alpine:latest
```

Anonymous pull is `pull` on that repository only. It does not open
`/v2/_catalog`, push, delete, or read a blob the repository does not
reference. A username with a bad password is rejected even when the
repository is published.

Unpublish sets `published` to false. Anonymous pull stops. Signed-in
users can still pull it.
