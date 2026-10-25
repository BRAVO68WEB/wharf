package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/repopath"
	"github.com/user/better-docker-registry/internal/storage"
)

// Access actions of the OCI token auth spec. Pull covers GET/HEAD; push
// covers every mutating method. A push grant never implies pull (and vice
// versa) — tokens request exactly the actions they get.
const (
	authActionPull = "pull"
	authActionPush = "push"
)

// Fixed wire messages. Validation failures share one message so a caller
// cannot probe why a token was rejected (expired vs bad signature vs ...).
const (
	authMsgRequired = "authentication required"
	authMsgInvalid  = "invalid bearer token"
	authMsgDenied   = "requested access to the resource is denied"
)

// authInternalBody is the fallback error body, used only if the registry
// error contract ever fails to marshal (it cannot: closed shape).
var authInternalBody = []byte(`{"errors":[{"code":"INTERNAL_ERROR","message":"internal registry error","detail":{}}]}`)

type authClaimsKey struct{}

// AuthUser returns the subject of the Bearer token that authenticated the
// request. Dashboard and audit code reads it; only BearerAuth injects it.
func AuthUser(ctx context.Context) (string, bool) {
	claims, ok := ctx.Value(authClaimsKey{}).(*auth.Claims)
	if !ok {
		return "", false
	}
	return claims.Subject, true
}

// AuthScopes returns the access entries granted by the Bearer token.
func AuthScopes(ctx context.Context) ([]auth.AccessEntry, bool) {
	claims, ok := ctx.Value(authClaimsKey{}).(*auth.Claims)
	if !ok {
		return nil, false
	}
	return claims.Access, true
}

// BearerAuth enforces the OCI token-auth contract on the registry API tree:
// every /v2 request must carry a Bearer JWT that validates against signer,
// and repository-scoped routes additionally require the pull action (GET/HEAD)
// or the push action (every other method) for the named repository.
//
// Public surfaces: /token passes through untouched (it mints the tokens), as
// does every path outside /v2 so one instance can wrap a mux that also holds
// the dashboard. The version check (GET/HEAD /v2/) needs a valid token but no
// scope and is answered here with 200 + the API version header.
//
// Failure contract: no/foreign credentials → 401 + WWW-Authenticate Bearer
// challenge; invalid token → the same 401 (indistinguishable from a probe's
// point of view); valid token without the required scope → 403 + DENIED with
// a challenge naming the scope to fetch. signer must not be nil.
//
// realm is the external origin, such as https://registry.example.com,
// written into WWW-Authenticate. Empty uses the request scheme and
// host, which is correct only when clients connect directly.
func BearerAuth(signer *auth.Signer, service, realm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/token" || !isV2Path(path) {
				next.ServeHTTP(w, r)
				return
			}
			token, hasToken := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !hasToken {
				writeUnauthorized(w, bearerChallenge(r, service, "", realm), authMsgRequired)
				return
			}
			claims, err := signer.Validate(token)
			if err != nil {
				writeUnauthorized(w, bearerChallenge(r, service, "", realm), authMsgInvalid)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), authClaimsKey{}, claims))
			name, scoped := repopath.Name(path)
			if !scoped {
				if (path == "/v2" || path == "/v2/") && isVersionCheckMethod(r.Method) {
					w.Header().Set(storage.HeaderAPIVersion, storage.APIVersion)
					w.WriteHeader(http.StatusOK)
					return
				}
				// An anonymous pull token must not open the catalog or any
				// other route that is not tied to one repository.
				if claims.Subject == auth.AnonymousSubject {
					writeAuthError(w, http.StatusForbidden, storage.NewError(storage.ErrorCodeDenied, authMsgDenied))
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			action := requiredAction(r.Method)
			if !claims.HasAccess(name, action) {
				scope := fmt.Sprintf("repository:%s:%s", name, action)
				w.Header().Set("WWW-Authenticate", bearerChallenge(r, service, scope, realm))
				writeAuthError(w, http.StatusForbidden, storage.NewError(storage.ErrorCodeDenied, authMsgDenied))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeUnauthorized answers 401 with the given WWW-Authenticate challenge and
// registry error message.
func writeUnauthorized(w http.ResponseWriter, challenge, message string) {
	w.Header().Set("WWW-Authenticate", challenge)
	writeAuthError(w, http.StatusUnauthorized, storage.NewError(storage.ErrorCodeUnauthorized, message))
}

// bearerChallenge builds the WWW-Authenticate value of the token auth spec:
// Bearer realm="<scheme>://<host>/token",service="<svc>",scope="<scope>".
// scope is empty on 401 and names the required grant on 403.
func bearerChallenge(r *http.Request, service, scope, realm string) string {
	if realm == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		realm = scheme + "://" + r.Host
	}
	return fmt.Sprintf("Bearer realm=\"%s/token\",service=\"%s\",scope=\"%s\"", realm, service, scope)
}

// writeAuthError writes the registry error contract (byte-compatible with the
// API layer's WriteError) with an explicit status.
func writeAuthError(w http.ResponseWriter, status int, e *storage.Error) {
	w.Header().Set("Content-Type", "application/json")
	body, err := json.Marshal(storage.ErrorResponse{Errors: []*storage.Error{e}})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(authInternalBody)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// requiredAction maps a request method to the token action it consumes.
// GET/HEAD read; everything else — including exotic verbs — writes (fail
// closed to the stricter grant).
func requiredAction(method string) string {
	if method == http.MethodGet || method == http.MethodHead {
		return authActionPull
	}
	return authActionPush
}

// isVersionCheckMethod reports whether the method may take the /v2 version
// check. HEAD mirrors GET per HTTP semantics (empty body either way).
func isVersionCheckMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}
