# Dashboard

The dashboard is the React app at `/dashboard`. `GET /` redirects
there. It is compiled into the registry binary. There is no separate
frontend process.

Visual rules are in [DESIGN.md](../DESIGN.md). The JSON routes behind
the pages are in [API](API.md).

## Pages

| Path | Who | What it shows |
| --- | --- | --- |
| `/dashboard/` | anyone | Repositories. Signed out, published names only. |
| `/dashboard/repo/…` | signed in | Tags, then one manifest: layers and history. |
| `/dashboard/gc` | signed in | Referenced and unreferenced blobs. |
| `/dashboard/tokens` | a person | Personal access tokens. |
| `/dashboard/accounts` | administrator | People and service accounts. |
| `/dashboard/login` | signed out | Sign in. |

The theme control is light, dark, or system. The choice is stored in
the browser under `registry-theme`. Until you pick one, the page
follows the system theme.

## Repositories

Search matches a substring of the name. A row shows tags, size, and
the newest tag. **Publish** and **Unpublish** are on that row for a
signed-in user. What publish changes for `docker pull` is in
[Registry](REGISTRY.md).

Open a repository for its tags. Open a tag for the manifest, the layer
digests, and each history command in full. Retag copies that manifest
onto another tag, in this repository or another. The source tag stays.

**Delete tag** removes that name. The manifest stays, including when
it was the last tag. **Delete manifest** removes the manifest and
every tag that points at it. The blobs stay until a collection runs.

## Storage

**Dry run** lists unreferenced blobs and deletes nothing. **Delete
unreferenced blobs** deletes them. While a collection runs, pushes and
deletes on `/v2` answer `503`. Pulls keep working.

`gc.interval` runs that same collection on a timer. `0s`, the usual
setting, leaves it manual. The page shows the interval and the last
successful run.
