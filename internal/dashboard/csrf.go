package dashboard

import (
	"net/http"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/middleware"
)

// WithSessionCSRF binds the session's raw token into the request context slot
// middleware.CSRF verifies against. internal/auth cannot do this itself —
// middleware already imports auth, so that would be an import cycle — and the
// dashboard is the one package that sees both sides.
//
// Mount order matters: run AFTER auth.SessionManager.RequireSession, which is
// what injects the session the token is read from. The SPA sends that token
// back in X-CSRF-Token.
func WithSessionCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, ok := auth.SessionTokenFromContext(r.Context()); ok {
			r = r.WithContext(middleware.WithCSRFToken(r.Context(), token))
		}
		next.ServeHTTP(w, r)
	})
}
