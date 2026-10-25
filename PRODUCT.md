# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Go server as a single binary. The same process speaks the OCI
Distribution API and serves the dashboard at `/dashboard`. The
dashboard is a Vite and React application, compiled into that binary
when you build it. Blobs live on the local filesystem or in an
S3-compatible bucket. A blob is stored once, under its digest, and
repositories share a layer by pointing at that digest. There's no
separate frontend process at runtime.

## Users

One developer, running the registry on their own machine or a small
server. They push and pull images with the Docker client, then open
the dashboard to see what is stored, how much space it uses, and what
to delete.

## Product Purpose

A private OCI registry that `docker push` and `docker pull` can use,
with a dashboard for browsing and cleaning up what was stored. Success
means a solo developer can push an image, find it in the dashboard,
inspect it, and delete it or reclaim the space, without another
service.

## Positioning

The official registry stores images and exposes the API. This one does
that and makes the stored images visible: repositories, tags,
manifests, layers, size, delete, and garbage collection, in a
dashboard served by the registry itself.

## Operating Context

The registry runs beside the Docker client on a local machine or a
small server. Images move through `docker push` and `docker pull`. The
person looks at the catalog in a browser on the same host.

## Capabilities and Constraints

Confirmed:

- OCI Distribution API sufficient for `docker push` and `docker pull`.
- Dashboard at `/dashboard` to browse repositories and tags, inspect
  manifests and the full image-history commands, see storage use, and
  delete or garbage-collect.
- A repository can be published from the dashboard. The repositories
  page lists published repositories with no sign-in. `docker pull` of a
  published repository works without a login. Push, delete, tags in the
  dashboard, manifests in the dashboard, storage, and unpublished
  repositories still require the account. A new repository is
  unpublished, and a later push does not change that.
- One Go binary at runtime. The dashboard is built with Vite before
  the binary is compiled, then embedded in it. Blob storage is the
  local filesystem or an S3-compatible bucket. No separate frontend
  process.
- Optional `server.public_url`, an absolute `http` or `https` origin
  with no path. When it is set, the dashboard shows
  `docker pull <host>/<repo>:<tag>` for a repository the viewer is
  allowed to see. When it is empty, the dashboard shows no pull
  command and does not guess a host.
- Not a multi-tenant platform, not a pull-through cache, and not a
  compatibility clone of the official registry's storage layout.
  There are no per-repository roles.
- The bootstrap admin is created from `REGISTRY_ADMIN_USER` and
  `REGISTRY_ADMIN_PASSWORD` before the first start, both set or both
  empty. An existing user is never rewritten. That account signs in
  to the dashboard and to `docker login`.
- An admin can add human accounts. A human signs in with a password.
- A signed-in human can create a personal access token. `docker login`
  accepts that token as the password. Revoking the token leaves the
  password working. The token is shown once.
- An admin can add a service account. It cannot open the dashboard.
  Creating it, or choosing New token, returns a token once. `docker
  login` sends that token as the password. Revoking it by issuing a
  new token stops the old one. Until some account exists, push and
  pull are refused.
- Garbage collection is started from the dashboard. A dry run lists
  what a sweep would delete, and a run deletes it. `gc.interval` can
  also start the same sweep on a timer. Zero, the default, leaves it
  manual. A tick that finds a collection already running is skipped.

Undecided:

- Whether the registry terminates TLS itself or sits behind another
  proxy.

## Evidence on Hand

The repository contains the registry and its dashboard. There are no
logos, screenshots, or customer evidence. Do not invent testimonials,
benchmarks, or adoption claims.

## Product Principles

- The registry protocol comes first. The dashboard never blocks a
  correct push or pull.
- One person can run it. One binary at runtime, local disk, no extra
  services. The dashboard is compiled into that binary.
- The dashboard answers three questions: what is stored, how big is
  it, and what can go.
- Stay a personal registry. Multi-tenant access control, quotas, and
  audit trails stay out of scope until asked for.
