package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// errorCodeInternal is the wire code of a failure with no OCI mapping.
// storage.ErrorCode.HTTPStatus fails codes unknown to the spec table closed to
// 500, which is exactly right here; the code stays out of
// storage.AllErrorCodes so that package's exhaustiveness guard keeps covering
// only the spec table.
const errorCodeInternal storage.ErrorCode = "INTERNAL_ERROR"

// internalErrorBody is the fallback error body, used when a registry error
// cannot be marshalled (its Detail is an open value).
var internalErrorBody = []byte(`{"errors":[{"code":"INTERNAL_ERROR","message":"internal registry error","detail":{}}]}`)

// WriteError writes the registry error contract, one entry per response:
//
//	{"errors":[{"code":"BLOB_UNKNOWN","message":"...","detail":{}}]}
//
// with the status the error code implies.
func WriteError(w http.ResponseWriter, e *storage.Error) {
	WriteErrorStatus(w, e.Code.HTTPStatus(), e)
}

// WriteErrorStatus writes the registry error contract with an explicit HTTP
// status, for the cases where the wire status is fixed by the protocol rather
// than implied by the code (the tags route answers 404 for an unknown
// repository with the NAME_INVALID code the plan pins).
func WriteErrorStatus(w http.ResponseWriter, status int, e *storage.Error) {
	body, err := json.Marshal(storage.ErrorResponse{Errors: []*storage.Error{e}})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(internalErrorBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// StorageError maps a storage failure to the registry error it becomes on the
// wire. Messages are fixed: a backend error can carry filesystem paths the
// client has no business seeing.
func StorageError(err error) *storage.Error {
	switch {
	case errors.Is(err, storage.ErrBlobNotFound):
		return storage.NewError(storage.ErrorCodeBlobUnknown, "blob unknown to the registry")
	case errors.Is(err, storage.ErrBlobInvalid):
		return storage.NewError(storage.ErrorCodeDigestInvalid, "blob content does not match its digest")
	case errors.Is(err, storage.ErrStorageUnavailable):
		return storage.NewError(storage.ErrorCodeUnavailable, "blob storage is unavailable")
	default:
		return storage.NewError(errorCodeInternal, "internal registry error")
	}
}
