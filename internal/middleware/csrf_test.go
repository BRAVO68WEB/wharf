package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/middleware"
)

// csrfProbe returns a CSRF-wrapped handler recording whether it ran.
func csrfProbe(called *bool) http.Handler {
	return middleware.CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusNoContent)
	}))
}

// Test_CSRF_when_MismatchedToken_then_403 pins that a wrong token is
// rejected exactly like a missing one (no oracle for valid tokens).
func Test_CSRF_when_MismatchedToken_then_403(t *testing.T) {
	// Given: a POST whose header carries a different token than the session.
	called := false
	req := httptest.NewRequest(http.MethodPost, "/dashboard/repos/test/delete", nil)
	req = req.WithContext(middleware.WithCSRFToken(req.Context(), "tok-123"))
	req.Header.Set(middleware.CSRFHeader, "tok-EVIL")
	rr := httptest.NewRecorder()

	// When: the request is served.
	csrfProbe(&called).ServeHTTP(rr, req)

	// Then: it is refused with 403 and the handler never runs.
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("handler ran on a mismatched CSRF token")
	}
}

// Test_CSRF_when_NoSessionToken_then_403 pins the fail-closed behaviour for
// mutations without any session-bound token, even with a header present.
func Test_CSRF_when_NoSessionToken_then_403(t *testing.T) {
	// Given: a POST with a CSRF header but no session token in context.
	called := false
	req := httptest.NewRequest(http.MethodPost, "/dashboard/repos/test/delete", nil)
	req.Header.Set(middleware.CSRFHeader, "tok-123")
	rr := httptest.NewRecorder()

	// When: the request is served.
	csrfProbe(&called).ServeHTTP(rr, req)

	// Then: it is refused with 403.
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("handler ran without a session-bound CSRF token")
	}
}

// Test_CSRF_when_EmptyTokens_then_403 pins that an empty session token never
// matches an empty header (both absent must not pass).
func Test_CSRF_when_EmptyTokens_then_403(t *testing.T) {
	// Given: a session with an empty token and an empty header.
	called := false
	req := httptest.NewRequest(http.MethodPost, "/dashboard/gc/run", nil)
	req = req.WithContext(middleware.WithCSRFToken(req.Context(), ""))
	req.Header.Set(middleware.CSRFHeader, "")
	rr := httptest.NewRecorder()

	// When: the request is served.
	csrfProbe(&called).ServeHTTP(rr, req)

	// Then: it is refused with 403.
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("handler ran with empty CSRF tokens")
	}
}

// Test_CSRF_when_MutatingMethods_then_AllRequireToken pins enforcement across
// POST/PUT/PATCH/DELETE — the plan names all four.
func Test_CSRF_when_MutatingMethods_then_AllRequireToken(t *testing.T) {
	// Given: one token-less request per mutating method.
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			called := false
			req := httptest.NewRequest(method, "/dashboard/repos/test", nil)
			rr := httptest.NewRecorder()

			// When: the request is served.
			csrfProbe(&called).ServeHTTP(rr, req)

			// Then: it is refused with 403.
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rr.Code)
			}
			if called {
				t.Fatalf("handler ran on token-less %s", method)
			}
		})
	}
}

// Test_CSRF_when_SafeMethods_then_NoTokenRequired pins that GET/HEAD/OPTIONS
// never demand a CSRF token.
func Test_CSRF_when_SafeMethods_then_NoTokenRequired(t *testing.T) {
	// Given: one token-less request per safe method.
	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodOptions,
	} {
		t.Run(method, func(t *testing.T) {
			called := false
			req := httptest.NewRequest(method, "/dashboard/repos", nil)
			rr := httptest.NewRecorder()

			// When: the request is served.
			csrfProbe(&called).ServeHTTP(rr, req)

			// Then: the handler runs.
			if !called {
				t.Fatalf("handler did not run on token-less %s", method)
			}
		})
	}
}

// Test_CSRF_when_V2PrefixVariants_then_AllExempt pins the Docker-client
// exemption for /v2 and /v2/... alike.
func Test_CSRF_when_V2PrefixVariants_then_AllExempt(t *testing.T) {
	// Given: token-less mutations at each /v2 shape.
	for _, path := range []string{
		"/v2",
		"/v2/",
		"/v2/library/nginx/blobs/uploads/",
	} {
		t.Run(path, func(t *testing.T) {
			called := false
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()

			// When: the request is served.
			csrfProbe(&called).ServeHTTP(rr, req)

			// Then: the handler runs without a token.
			if !called {
				t.Fatalf("handler did not run on token-less POST %s", path)
			}
		})
	}
}

// Test_CSRFToken_when_SetInContext_then_VisibleToTemplates pins the accessor
// that reads the session-bound token back out of the request context.
func Test_CSRFToken_when_SetInContext_then_VisibleToTemplates(t *testing.T) {
	// Given: a context with a bound CSRF token.
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req = req.WithContext(middleware.WithCSRFToken(req.Context(), "tok-42"))

	// When: the token is read back.
	got, ok := middleware.CSRFToken(req.Context())

	// Then: it matches and reports present.
	if !ok || got != "tok-42" {
		t.Fatalf("CSRFToken = %q, %v; want tok-42, true", got, ok)
	}

	// And: a bare context reports absent.
	if _, ok := middleware.CSRFToken(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Fatal("CSRFToken reported present on an empty context")
	}
}
