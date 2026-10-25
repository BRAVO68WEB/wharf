# Configuration

`--config` is one YAML file. A missing flag, a missing file, or an
unknown key exits before the process listens. The file names
environment variables. It does not hold secrets.

How to start the process is in [Deploy](DEPLOY.md).

## File

Durations are Go strings, such as `30s` or `5m`, or an integer number
of seconds.

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
    issuer: wharf
    service: registry.docker.io
    token_ttl: 15m
  session:
    secret_env: REGISTRY_SESSION_SECRET
    ttl: 24h
  dev_anonymous: false

gc:
  batch_size: 256
  interval: 0s

logging:
  level: info
```

`testdata/config.yml` is this shape with paths for one checkout. Do
not run that file unchanged.

## Server

`server.port` is from 1 to 65535. `--addr` replaces `:<port>` for the
whole listen address.

Leave `read_timeout` and `write_timeout` at `0s`. A non-zero value
cuts off a long blob transfer. Reading the request headers still stops
after 10 seconds. `max_upload_size` is the largest blob upload, in
bytes. `0` means no limit.

`public_url` is an absolute `http` or `https` origin. It has a host,
an optional port, and nothing else. A path, query, fragment, or user
info is rejected.

| `public_url` | Cookie `Secure` | Bearer realm |
| --- | --- | --- |
| empty | off | the connection's scheme and `Host` |
| `http://host` | off | that origin |
| `https://host` | on | that origin |

The process does not read `X-Forwarded-Proto`. An empty `public_url`
hides pull lines in the dashboard and does not guess a host.

## Storage

`storage.type` is `filesystem` or `s3`.

`filesystem` stores blobs under `storage.path`. `s3` requires
`storage.s3.region` and `storage.s3.bucket`. The bucket must already
exist. An empty `endpoint` uses AWS for that region. Set
`access_key_env` and `secret_key_env` together, or leave both empty
and use the AWS SDK default chain. MinIO needs
`force_path_style: true`.

What a push and a delete do to those bytes is in
[Storage](STORAGE.md).

## Database

`database.path` is the SQLite file. Use an absolute path. A relative
path is rejected by the driver, not by this file check.

## Auth

`auth.jwt.private_key_path` is an RSA PEM, PKCS#1 or PKCS#8. A missing
file is created on startup: 2048-bit RSA, mode `0600`. An ECDSA key is
rejected. `issuer` is the JWT `iss` claim. `service` is the `aud`
claim and the `service` value Docker must send to `/token`.
`token_ttl` is greater than zero.

`auth.session.secret_env` is the name of the variable that holds the
dashboard session secret. That variable must be set, and the value
must be at least 32 bytes. `ttl` is greater than zero.

`auth.dev_anonymous: true` serves `/v2` with no bearer token. The
dashboard still requires a session. Startup allows the flag only when
`public_url` is empty, or when its host is `localhost`, `127.0.0.1`,
or `::1`.

The first administrator is not a YAML field. It comes from
`REGISTRY_ADMIN_USER` and `REGISTRY_ADMIN_PASSWORD`. See
[Accounts](ACCOUNTS.md).

## Garbage collection

`gc.batch_size` is greater than zero. `gc.interval` of `0s` leaves
collection as a dashboard action. A negative interval is rejected. A
positive interval runs the same collection a dashboard run does. A
tick that finds a run already going is skipped.

## Logging

`logging.level` is `debug`, `info`, `warn`, or `error`. Request lines
are written at info, so `warn` and `error` omit them. `/healthz` and
`/readyz` are not logged. The `Authorization` header is recorded as
`REDACTED`. An inbound `X-Request-ID` is kept. Otherwise the process
generates one.
