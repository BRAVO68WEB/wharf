package storage

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by Storage implementations. Callers match them with
// errors.Is and translate to an ErrorCode at the HTTP boundary; drivers wrap
// them with detail (digest, path) using fmt.Errorf("...: %w", ErrX).
var (
	// ErrBlobNotFound reports a digest the store does not hold.
	ErrBlobNotFound = errors.New("storage: blob not found")

	// ErrBlobInvalid reports blob content that does not match its digest or is
	// otherwise unusable (truncated, corrupt).
	ErrBlobInvalid = errors.New("storage: blob invalid")

	// ErrStorageUnavailable reports a backend that cannot serve the request
	// (I/O failure, unreachable object store). Transient and retryable.
	ErrStorageUnavailable = errors.New("storage: unavailable")
)

// ErrorCode is an OCI Distribution Spec error code. The value is the exact
// string that goes on the wire in an ErrorResponse entry.
type ErrorCode string

// OCI Distribution Spec error codes, plus UNAVAILABLE which the Docker
// registry uses for transient backend failures.
const (
	ErrorCodeBlobUnknown         ErrorCode = "BLOB_UNKNOWN"
	ErrorCodeBlobUploadInvalid   ErrorCode = "BLOB_UPLOAD_INVALID"
	ErrorCodeBlobUploadUnknown   ErrorCode = "BLOB_UPLOAD_UNKNOWN"
	ErrorCodeDigestInvalid       ErrorCode = "DIGEST_INVALID"
	ErrorCodeManifestBlobUnknown ErrorCode = "MANIFEST_BLOB_UNKNOWN"
	ErrorCodeManifestInvalid     ErrorCode = "MANIFEST_INVALID"
	ErrorCodeManifestUnknown     ErrorCode = "MANIFEST_UNKNOWN"
	ErrorCodeNameInvalid         ErrorCode = "NAME_INVALID"
	ErrorCodeNameUnknown         ErrorCode = "NAME_UNKNOWN"
	ErrorCodeRangeInvalid        ErrorCode = "RANGE_INVALID"
	ErrorCodeSizeInvalid         ErrorCode = "SIZE_INVALID"
	ErrorCodeTagInvalid          ErrorCode = "TAG_INVALID"
	ErrorCodeUnauthorized        ErrorCode = "UNAUTHORIZED"
	ErrorCodeDenied              ErrorCode = "DENIED"
	ErrorCodeUnsupported         ErrorCode = "UNSUPPORTED"
	ErrorCodeTooManyRequests     ErrorCode = "TOOMANYREQUESTS"
	ErrorCodeUnavailable         ErrorCode = "UNAVAILABLE"
)

// AllErrorCodes returns every defined code. The API layer uses it to prove
// exhaustiveness when mapping failures to responses.
func AllErrorCodes() []ErrorCode {
	return []ErrorCode{
		ErrorCodeBlobUnknown,
		ErrorCodeBlobUploadInvalid,
		ErrorCodeBlobUploadUnknown,
		ErrorCodeDigestInvalid,
		ErrorCodeManifestBlobUnknown,
		ErrorCodeManifestInvalid,
		ErrorCodeManifestUnknown,
		ErrorCodeNameInvalid,
		ErrorCodeNameUnknown,
		ErrorCodeRangeInvalid,
		ErrorCodeSizeInvalid,
		ErrorCodeTagInvalid,
		ErrorCodeUnauthorized,
		ErrorCodeDenied,
		ErrorCodeUnsupported,
		ErrorCodeTooManyRequests,
		ErrorCodeUnavailable,
	}
}

// HTTPStatus maps a code to the status the registry must respond with, per
// the OCI distribution spec error table. A code with no mapping fails closed
// to 500 rather than guessing a client-visible status.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case ErrorCodeBlobUnknown, ErrorCodeBlobUploadUnknown, ErrorCodeManifestUnknown, ErrorCodeNameUnknown:
		return 404
	case ErrorCodeBlobUploadInvalid, ErrorCodeDigestInvalid, ErrorCodeManifestBlobUnknown,
		ErrorCodeManifestInvalid, ErrorCodeNameInvalid, ErrorCodeSizeInvalid, ErrorCodeTagInvalid:
		return 400
	case ErrorCodeRangeInvalid:
		return 416
	case ErrorCodeUnauthorized:
		return 401
	case ErrorCodeDenied:
		return 403
	case ErrorCodeUnsupported:
		return 405
	case ErrorCodeTooManyRequests:
		return 429
	case ErrorCodeUnavailable:
		return 503
	default:
		return 500
	}
}

// Error is one entry of a registry error response body.
type Error struct {
	Code    ErrorCode   `json:"code"`
	Message string      `json:"message"`
	Detail  interface{} `json:"detail"`
}

// ErrorResponse is the registry error body:
//
//	{"errors":[{"code":"BLOB_UNKNOWN","message":"...","detail":{}}]}
type ErrorResponse struct {
	Errors []*Error `json:"errors"`
}

// NewError builds an Error whose detail is the empty object, matching the
// {"detail":{}} shape the wire contract shows when nothing more is known.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message, Detail: struct{}{}}
}

// WithDetail returns a copy of e carrying detail (a JSON-marshalable value,
// typically a map). The receiver is unchanged.
func (e *Error) WithDetail(detail interface{}) *Error {
	return &Error{Code: e.Code, Message: e.Message, Detail: detail}
}

// Error implements the error interface, leading with the wire code so log
// lines stay greppable.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
