package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
)

// CSRFHeader is the request header carrying the CSRF token on state-changing
// dashboard requests. The dashboard renders the session-bound token into its
// forms/HTMX headers; this middleware verifies it on the way back in.
const CSRFHeader = "X-CSRF-Token"

type csrfTokenKey struct{}

// WithCSRFToken binds the session's CSRF token to the request context. The
// session middleware calls this after loading a session so that CSRF can
// verify state-changing requests against the session-embedded token.
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfTokenKey{}, token)
}

// CSRFToken returns the CSRF token bound to the request's session, if any.
// Dashboard templates use it to render the token into forms.
func CSRFToken(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(csrfTokenKey{}).(string)
	return token, ok && token != ""
}

// CSRF rejects state-changing requests (anything outside GET/HEAD/OPTIONS)
// whose X-CSRF-Token header does not match the session-embedded token. Both a
// missing token and a mismatch fail closed with 403.
//
// The Docker registry API (/v2/*) is exempt: Docker clients never send CSRF
// tokens. Every other path — the dashboard included — is enforced.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isV2Path(r.URL.Path) || isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		expected, ok := CSRFToken(r.Context())
		got := r.Header.Get(CSRFHeader)
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			http.Error(w, "forbidden: invalid or missing CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isSafeMethod reports whether the method never mutates state and therefore
// needs no CSRF token. Everything else — including exotic verbs — is treated
// as state-changing (fail closed).
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
