package middleware

import (
	"net/http"
	"strings"
)

// DashboardCSP is the Content-Security-Policy for every route outside /v2.
// The dashboard is a same-origin Vite bundle: scripts, styles, and fonts all
// come from this process. Nothing else may load.
const DashboardCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// SecurityHeaders sets the baseline hardening headers on every response:
// X-Content-Type-Options and X-Frame-Options everywhere, plus the dashboard
// CSP on routes outside the Docker API tree (/v2/* stays free of dashboard
// middleware — Docker clients ignore these headers and must not be affected).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		if !isV2Path(r.URL.Path) {
			header.Set("Content-Security-Policy", DashboardCSP)
		}
		next.ServeHTTP(w, r)
	})
}

// isV2Path reports whether the request targets the Docker registry API tree,
// which no dashboard middleware (CSRF, CSP) may touch.
func isV2Path(path string) bool {
	return path == "/v2" || strings.HasPrefix(path, "/v2/")
}
