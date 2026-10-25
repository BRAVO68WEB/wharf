# Storage

Blobs are content-addressed. Metadata is SQLite. Config keys are in
[Configuration](CONFIG.md).

## One copy of a layer

A blob is stored once, under its digest. A second repository that
pushes the same layer gets a reference, not a second copy.

The filesystem layout is `<storage.path>/<digest[:2]>/<digest[2:]>`.
`.` , `..`, and a slash in a path element are rejected. An S3 object
key is the digest. The backend does not create or delete the bucket.

## Delete

`DELETE /v2/<name>/blobs/<digest>` drops that repository's reference
and answers `202`. The file or object stays. If this repository has no
reference, the answer is `404` and storage is untouched.

Deleting a tag removes the name. Deleting a manifest removes that
manifest and every tag that points at it. Neither call deletes blob
bytes.

`GET` and `HEAD` of a blob require a reference from that repository.
A digest that exists only in another repository is `404` here.

## Collection

Collection is the only path that deletes blob bytes. It removes a
digest when no manifest still references it. `blob_refs` rows left
behind by a manifest delete do not keep the blob.

A dry run lists that set and writes nothing. A run deletes it in
batches of `gc.batch_size`. Two runs do not overlap. The second
answers `409` from the dashboard, and a scheduled tick skips.

During a run, a push or delete on `/v2` answers `503`. A pull is
still served.

`SIGINT` or `SIGTERM` makes `/readyz` answer `503`, then waits up to
30 seconds for requests already in flight.
