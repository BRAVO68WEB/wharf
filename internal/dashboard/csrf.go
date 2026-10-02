package dashboard

import (
	"net/http"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/middleware"
)

// WithSessionCSRF binds the session's raw token into the request context slot
// middleware.CSRF verifies against. internal/auth cannot do this itself —
// middleware already imports auth, so that would be an import cycle — and the
// dashboard is the one package that sees both sides.
//
// Mount order matters: run AFTER auth.SessionManager.RequireSession, which is
// what injects the session the token is read from. Wrapping the dashboard
// tree as
//
//	dashboard.WithSessionCSRF(sessionManager.RequireSession(r))
//
// gives every dashboard POST/DELETE (logout, delete flows, GC) the token its
// templates render into hx-headers.
func WithSessionCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := auth.SessionTokenFromContext(r.Context()); ok {
			r = r.WithContext(middleware.WithCSRFToken(r.Context(), token))
		}
		next.ServeHTTP(w, r)
	})
}

// sessionUser returns the signed-in operator's name for the nav bar, empty
// when the request carries no session (e.g. handler tests).
func sessionUser(r *http.Request) string {
	user, ok := auth.SessionUserFromContext(r.Context())
	if !ok {
		return ""
	}
	return user.Username
}

// sessionCSRF returns the CSRF token templates must render into HTMX
// hx-headers so state-changing requests satisfy middleware.CSRF.
func sessionCSRF(r *http.Request) string {
	token, ok := middleware.CSRFToken(r.Context())
	if !ok {
		return ""
	}
	return token
}
