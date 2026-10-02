// Package storage defines the blob storage abstraction and its drivers
// (filesystem, S3-compatible). Metadata lives in internal/db; blobs live here.
package storage

import (
	"context"
	"io"
)

// Protocol constants for the Docker Registry HTTP API V2 / OCI Distribution.
const (
	// HeaderAPIVersion is the response header every registry API response must
	// carry; its value must be APIVersion.
	HeaderAPIVersion = "Docker-Distribution-API-Version"

	// APIVersion is the registry protocol version this server implements.
	APIVersion = "registry/2.0"
)

// Storage is the content-addressable blob store.
//
// Digests are OCI content identifiers of the form "sha256:<64 hex chars>".
// Callers parse and validate digests at the API boundary and translate
// failures into ErrorCode values (see errors.go); implementations receive
// already-qualified digests and must not re-validate their syntax.
//
// Contract every implementation must honour:
//
//   - Blob bodies are streamed. Put consumes r without buffering the whole
//     blob in memory, and Get returns a stream the caller closes. Implementors
//     must never materialise a whole blob in memory.
//   - Errors are sentinel-wrapped (errors.Is-matchable): absence is
//     ErrBlobNotFound, content that does not match its digest is
//     ErrBlobInvalid, and a backend failure is ErrStorageUnavailable.
//   - Implementations are safe for concurrent use and honour ctx cancellation.
type Storage interface {
	// Get returns a reader over the blob's content; the caller closes it.
	// A missing digest fails with a wrap of ErrBlobNotFound.
	Get(ctx context.Context, digest string) (io.ReadCloser, error)

	// Put stores the content of r under digest and returns the number of
	// bytes written. Content that does not hash to digest fails with a wrap
	// of ErrBlobInvalid. A failed Put must not leave a readable blob behind.
	Put(ctx context.Context, digest string, r io.Reader) (int64, error)

	// Exists reports whether digest is held. Absence is the false result, not
	// an error.
	Exists(ctx context.Context, digest string) (bool, error)

	// Delete removes digest. A missing digest fails with a wrap of
	// ErrBlobNotFound; deleting is not idempotent here so callers can tell
	// "gone" from "never existed".
	Delete(ctx context.Context, digest string) error

	// Size returns the stored byte length of digest. A missing digest fails
	// with a wrap of ErrBlobNotFound.
	Size(ctx context.Context, digest string) (int64, error)

	// List invokes fn once per digest held. If fn returns an error, iteration
	// stops and that error is returned unwrapped. Implementations must not
	// hold internal locks while invoking fn. Callbacks may call back into the
	// same Storage.
	List(ctx context.Context, fn func(digest string) error) error
}
