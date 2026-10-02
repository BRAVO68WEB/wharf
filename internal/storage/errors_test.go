package storage_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/user/better-docker-registry/internal/storage"
)

// Given: a registry Error wrapped in a caller's message
// When:  errors.Is inspects the chain
// Then:  the sentinel is still matched
func Test_SentinelErrors_when_Wrapped_then_ErrorsIsMatches(t *testing.T) {
	for _, tc := range []struct {
		name string
		sent error
	}{
		{"ErrBlobNotFound", storage.ErrBlobNotFound},
		{"ErrBlobInvalid", storage.ErrBlobInvalid},
		{"ErrStorageUnavailable", storage.ErrStorageUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("driver: %w", tc.sent)
			if !errors.Is(wrapped, tc.sent) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", wrapped, tc.sent)
			}
		})
	}
}

// Given: a registry ErrorResponse built from NewError
// When:  it is marshaled to JSON
// Then:  the wire shape is exactly the OCI distribution error contract
func Test_ErrorResponse_when_Marshaled_then_MatchesWireContract(t *testing.T) {
	resp := storage.ErrorResponse{
		Errors: []*storage.Error{
			storage.NewError(storage.ErrorCodeBlobUnknown, "blob unknown to registry"),
		},
	}
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to registry","detail":{}}]}`
	if string(got) != want {
		t.Fatalf("Marshal = %s\nwant      %s", got, want)
	}
}

// Given: an Error carrying a caller-supplied detail
// When:  it is marshaled to JSON
// Then:  the detail object appears in place of the empty default
func Test_ErrorResponse_when_DetailSet_then_DetailSerialized(t *testing.T) {
	resp := storage.ErrorResponse{
		Errors: []*storage.Error{
			storage.NewError(storage.ErrorCodeSizeInvalid, "provided length did not match content length").
				WithDetail(map[string]any{"expected": 10, "actual": 7}),
		},
	}
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"errors":[{"code":"SIZE_INVALID","message":"provided length did not match content length","detail":{"actual":7,"expected":10}}]}`
	if string(got) != want {
		t.Fatalf("Marshal = %s\nwant      %s", got, want)
	}
}

// Given: every defined ErrorCode
// When:  HTTPStatus is called
// Then:  the status matches the OCI distribution spec table
func Test_ErrorCode_when_HTTPStatus_then_MapsToDistributionSpecStatus(t *testing.T) {
	for _, tc := range []struct {
		code storage.ErrorCode
		want int
	}{
		{storage.ErrorCodeBlobUnknown, 404},
		{storage.ErrorCodeBlobUploadInvalid, 400},
		{storage.ErrorCodeBlobUploadUnknown, 404},
		{storage.ErrorCodeDigestInvalid, 400},
		{storage.ErrorCodeManifestBlobUnknown, 400},
		{storage.ErrorCodeManifestInvalid, 400},
		{storage.ErrorCodeManifestUnknown, 404},
		{storage.ErrorCodeNameInvalid, 400},
		{storage.ErrorCodeNameUnknown, 404},
		{storage.ErrorCodeRangeInvalid, 416},
		{storage.ErrorCodeSizeInvalid, 400},
		{storage.ErrorCodeTagInvalid, 400},
		{storage.ErrorCodeUnauthorized, 401},
		{storage.ErrorCodeDenied, 403},
		{storage.ErrorCodeUnsupported, 405},
		{storage.ErrorCodeTooManyRequests, 429},
		{storage.ErrorCodeUnavailable, 503},
	} {
		t.Run(string(tc.code), func(t *testing.T) {
			if got := tc.code.HTTPStatus(); got != tc.want {
				t.Fatalf("HTTPStatus(%s) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

// Given: every code declared in AllErrorCodes
// When:  each is run through HTTPStatus
// Then:  none lands on the 500 "unmapped" default, so no constant is left
//
//	without a spec status
func Test_ErrorCode_when_HTTPStatusTable_then_CoversAllErrorCodes(t *testing.T) {
	var unmapped []storage.ErrorCode
	for _, c := range storage.AllErrorCodes() {
		if c.HTTPStatus() == 500 {
			unmapped = append(unmapped, c)
		}
	}
	if len(unmapped) != 0 {
		t.Fatalf("codes falling through to the 500 default: %v", unmapped)
	}
}

// Given: the full ErrorCode set
// When:  the constants are inspected
// Then:  every code is non-empty and no wire value is duplicated
func Test_ErrorCodes_when_Listed_then_UniqueAndNonEmpty(t *testing.T) {
	all := storage.AllErrorCodes()
	if len(all) != 17 {
		t.Fatalf("AllErrorCodes() returned %d codes, want 17", len(all))
	}
	seen := map[storage.ErrorCode]bool{}
	for _, c := range all {
		if c == "" {
			t.Fatal("empty ErrorCode in AllErrorCodes()")
		}
		if seen[c] {
			t.Fatalf("duplicate ErrorCode %q", c)
		}
		seen[c] = true
	}
}

// Given: a *storage.Error
// When:  Error() is called
// Then:  the message leads with the wire code for log greppability
func Test_Error_Error_when_Called_then_PrefixesMessageWithCode(t *testing.T) {
	e := storage.NewError(storage.ErrorCodeDenied, "requested access to the resource is denied")
	want := "DENIED: requested access to the resource is denied"
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
