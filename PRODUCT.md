# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Go server as a single binary. The same process speaks the OCI Distribution API and serves an HTMX dashboard. Blob and manifest storage is the local filesystem. No separate frontend build.

## Users

One developer, running the registry on their own machine or a small server. They push and pull images with the Docker client, then open the dashboard to see what is stored, how much space it uses, and what to delete.

## Product Purpose

A private OCI registry that `docker push` and `docker pull` can use, with a dashboard for browsing and cleaning up what was stored. Success means a solo developer can push an image, find it in the dashboard, inspect it, and delete it or reclaim the space, without another service.

## Positioning

The official registry stores images and exposes the API. This one does that and makes the stored images visible: repositories, tags, manifests, layers, size, delete, and garbage collection, in an HTMX console served by the registry itself.

## Operating Context

The registry runs beside the Docker client on a local machine or a small server. Images move through `docker push` and `docker pull`. The person looks at the catalog in a browser on the same host.

## Capabilities and Constraints

Confirmed:

- OCI Distribution API sufficient for `docker push` and `docker pull`.
- HTMX dashboard to browse repositories and tags, inspect manifests and layers, see storage use, and delete or garbage-collect.
- One Go binary. Local filesystem storage. No separate frontend build step.
- Single-user. Not a multi-tenant platform, not a pull-through cache, and not a compatibility clone of the official registry's storage layout.
- One shared password. The first browser visit sets it and the server stores it. After that, the dashboard and `docker login` both use it. Until it is set, push and pull are refused.
- Garbage collection runs on a schedule and can also be started from the dashboard. The dashboard shows the last run.

Undecided:

- How often the garbage-collection schedule runs.
- Whether the registry terminates TLS itself or sits behind another proxy.
- The public hostname shown in pull commands. The console should show the configured host. `localhost:5000` is only the assumed default.

## Evidence on Hand

The repository has no application code, copy, logos, screenshots, or customer evidence. Do not invent testimonials, benchmarks, or adoption claims.

## Product Principles

- The registry protocol comes first. The dashboard never blocks a correct push or pull.
- One person can run it. One binary, local disk, no extra services.
- The dashboard answers three questions: what is stored, how big is it, and what can go.
- Stay a personal registry. Multi-tenant access control, quotas, and audit trails stay out of scope until asked for.
