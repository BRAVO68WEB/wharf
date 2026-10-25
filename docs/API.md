# API

Two HTTP trees share the process. Docker speaks `/v2` and `/token`.
The dashboard speaks `/dashboard/api`. Client behavior is in
[Registry](REGISTRY.md).

Every `/v2` response sets `Docker-Distribution-API-Version` to
`registry/2.0`. A `4xx` body from the registry API is
`{"errors":[{"code":"...","message":"...","detail":{}}]}`.

## Registry

A missing or bad bearer token on `/v2` is `401` with
`WWW-Authenticate: Bearer realm="…/token",service="…",scope=""`.
`auth.dev_anonymous` skips that check. See
[Configuration](CONFIG.md).

| Method | Path | Success |
| --- | --- | --- |
| GET | `/v2/` | `200` with a valid token |
| GET, POST | `/token` | bearer token |
| GET | `/v2/_keys` | JWKS for the signing key |
| GET, HEAD | `/v2/<name>/blobs/<digest>` | `200`, or `206` for a `Range` |
| DELETE | `/v2/<name>/blobs/<digest>` | `202`, drops this repo's reference |
| POST | `/v2/<name>/blobs/uploads/` | `202` session, `201` monolithic or mount |
| GET | `/v2/<name>/blobs/uploads/<uuid>` | `204`, current offset |
| PATCH | `/v2/<name>/blobs/uploads/<uuid>` | `202` chunk |
| PUT | `/v2/<name>/blobs/uploads/<uuid>?digest=` | `201` |
| DELETE | `/v2/<name>/blobs/uploads/<uuid>` | `204`, cancels the upload |
| GET, HEAD | `/v2/<name>/manifests/<tag-or-digest>` | `200` |
| PUT | `/v2/<name>/manifests/<tag-or-digest>` | `201` |
| DELETE | `/v2/<name>/manifests/<tag-or-digest>` | `202` |
| GET | `/v2/<name>/tags/list` | `200` |
| GET | `/v2/_catalog` | `200` |
| GET | `/v2/<name>/referrers/<digest>` | `200` image index |

`<name>` may contain slashes. Tag list and catalog take `n` and
`last`. A missing or non-positive `n` uses a page of 100. The next
page is a `Link` header with `rel="next"`.

`POST /v2/<name>/blobs/uploads/?mount=<digest>&from=<other>` mounts an
existing blob and answers `201`. An unknown digest opens a session
instead, `202`.

Referrers answers `200` with an empty manifest list when nothing
refers to that digest. `?artifactType=` filters the list and sets
`OCI-Filters-Applied: artifactType`. A manifest push that carries
`subject` answers with `OCI-Subject`.

An anonymous token, including a pull of a published repository, cannot
call `/v2/_catalog`.

## Health

| Method | Path | Success |
| --- | --- | --- |
| GET | `/healthz` | `200` while the process is up |
| GET | `/readyz` | `200` when SQLite and blob storage answer |
| GET | `/` | `302` to `/dashboard/` |

`/readyz` is `503` when a dependency is down or the process is
draining.

## Dashboard

State-changing dashboard routes need a session cookie and
`X-CSRF-Token` set to that session's raw token. Login does not.
`GET /dashboard/api/public/repos` does not need a session.

| Method | Path | Who |
| --- | --- | --- |
| POST | `/dashboard/api/login` | anyone. JSON `username` and `password`. |
| GET | `/dashboard/api/session` | signed in |
| POST | `/dashboard/api/logout` | signed in. `204`. |
| GET | `/dashboard/api/public/repos` | anyone. Published repositories. |
| GET | `/dashboard/api/repos` | signed in |
| POST | `/dashboard/api/repos/publish` | signed in. Body `{"name","published"}`. |
| GET | `/dashboard/api/repo/<name>` | signed in. Tag list. |
| GET | `/dashboard/api/repo/<name>/manifests/<digest>` | signed in |
| DELETE | `/dashboard/api/repo/<name>/tags/<tag>` | signed in. `204`. |
| DELETE | `/dashboard/api/repo/<name>/manifests/<digest>` | signed in. `204`. |
| POST | `/dashboard/api/repo/<name>/tags/<tag>/retag` | signed in. Body `{"targetRepo","targetTag"}`. |
| GET | `/dashboard/api/gc` | signed in |
| POST | `/dashboard/api/gc/dry-run` | signed in |
| POST | `/dashboard/api/gc/run` | signed in. `409` if a run is already going. |
| GET, POST | `/dashboard/api/accounts` | administrator |
| DELETE | `/dashboard/api/accounts/<id>` | administrator |
| POST | `/dashboard/api/accounts/<id>/tokens` | administrator. Service account. |
| GET, POST | `/dashboard/api/tokens` | a person |
| DELETE | `/dashboard/api/tokens/<id>` | that person |

`GET /dashboard/` and `GET /dashboard/*` serve the single-page app.
`/dashboard/api` is not part of that fallback. Hashed build assets are
cached. Other dashboard URLs fall back to `index.html`.
