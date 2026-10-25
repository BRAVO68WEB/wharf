package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/BRAVO68WEB/wharf/internal/db"
)

// ErrInvalidScope is the sentinel behind every ParseScope failure, so callers
// can reject a scope without matching on message text.
var ErrInvalidScope = errors.New("invalid scope")

// errInvalidCredentials marks the refused-credentials path of the token
// endpoint (wrong password or unknown user — deliberately indistinguishable).
var errInvalidCredentials = errors.New("invalid credentials")

// actionPattern is the action grammar of the token scope: lowercase words or
// the wildcard "*".
var actionPattern = regexp.MustCompile(`^[a-z*][a-z0-9_-]*$`)

// tokenMaxFormBytes bounds a form-encoded POST to the token endpoint. Token
// requests are tiny (service + scopes + credentials); anything larger is a
// probe or an abuse, never a legitimate client.
const tokenMaxFormBytes = 1 << 16

// ParseScope parses one scope query parameter of the token request
// ("repository:<name>:<actions>", actions comma-separated) into the access
// claim entry it becomes. Only repository scopes exist in v1. The repository
// name grammar itself is enforced at the registry route boundary; here the
// name must just be a plausible single resource name.
func ParseScope(raw string) (AccessEntry, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] == "" {
		return AccessEntry{}, fmt.Errorf("%w %q: want <type>:<name>:<actions>", ErrInvalidScope, raw)
	}
	if parts[0] != accessTypeRepository {
		return AccessEntry{}, fmt.Errorf("%w %q: unsupported type %q", ErrInvalidScope, raw, parts[0])
	}
	name := parts[1]
	if name == "" || len(name) > 255 || strings.ContainsAny(name, " \t") {
		return AccessEntry{}, fmt.Errorf("%w %q: invalid name %q", ErrInvalidScope, raw, name)
	}
	actions := strings.Split(parts[2], ",")
	for _, action := range actions {
		if !actionPattern.MatchString(action) {
			return AccessEntry{}, fmt.Errorf("%w %q: invalid actions %q", ErrInvalidScope, raw, parts[2])
		}
	}
	return AccessEntry{Type: accessTypeRepository, Name: name, Actions: actions}, nil
}

// TokenHandler is the OCI token endpoint: it exchanges Basic credentials for
// a short-lived RS256 registry token. Wire it at GET /token (chi:
// r.Get("/token", h.ServeHTTP)).
type TokenHandler struct {
	users  db.Querier
	signer *Signer
}

// NewTokenHandler returns the token endpoint backed by the users table.
func NewTokenHandler(users db.Querier, signer *Signer) *TokenHandler {
	return &TokenHandler{users: users, signer: signer}
}

// ServeHTTP answers GET/POST /token?service=<svc>&scope=repository:<name>:<actions>.
// POST is served with the same contract as GET: Docker probes the token
// endpoint with POST before GET. Form-encoded POST bodies are accepted
// alongside query parameters (OAuth2 token endpoint shape). When credentials
// are present they are checked before any query parameter is acted on. With
// no credentials, a token is issued only when every scope is pull on a
// published repository, which is what lets docker pull a public image. A
// missing scope, a push scope, or an unpublished repository is refused.
// Empty scope parameters (docker sends "scope=&scope=repository:...") are
// skipped. A signed-in request with no scope yields a token with an empty
// access array (docker login only checks credentials).
func (h *TokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeTokenError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, password, hasBasic := r.BasicAuth()
	if hasBasic && h.checkCredentials(r.Context(), username, password) != nil {
		h.refuseToken(w)
		return
	}
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, tokenMaxFormBytes)
		if err := r.ParseForm(); err != nil {
			writeTokenError(w, http.StatusBadRequest, "invalid form body")
			return
		}
		values = r.Form
	}
	service := values.Get("service")
	switch {
	case service == "":
		writeTokenError(w, http.StatusBadRequest, "service parameter is required")
		return
	case service != h.signer.cfg.Service:
		writeTokenError(w, http.StatusBadRequest, "unknown service")
		return
	}
	scopes := values["scope"]
	access := make([]AccessEntry, 0, len(scopes))
	for _, raw := range scopes {
		if raw == "" {
			continue
		}
		entry, err := ParseScope(raw)
		if err != nil {
			writeTokenError(w, http.StatusBadRequest, err.Error())
			return
		}
		access = append(access, entry)
	}
	subject := username
	if !hasBasic {
		if err := h.allowAnonymousPull(r.Context(), access); err != nil {
			if errors.Is(err, errInvalidCredentials) {
				h.refuseToken(w)
				return
			}
			writeTokenError(w, http.StatusInternalServerError, "internal error")
			return
		}
		subject = AnonymousSubject
	}
	claims := h.signer.NewClaims(subject, access)
	token, err := h.signer.Sign(claims)
	if err != nil {
		writeTokenError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeToken(w, token, claims)
}

// AnonymousSubject is the JWT subject of a token issued with no credentials.
// It grants only the pull scopes named in that token, never the catalog.
const AnonymousSubject = "anonymous"

func (h *TokenHandler) refuseToken(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", h.signer.cfg.Service))
	writeTokenError(w, http.StatusUnauthorized, "invalid credentials")
}

// allowAnonymousPull accepts a credential-less token request only when every
// scope is exactly pull and every named repository is published.
func (h *TokenHandler) allowAnonymousPull(ctx context.Context, access []AccessEntry) error {
	if len(access) == 0 {
		return errInvalidCredentials
	}
	for _, entry := range access {
		if len(entry.Actions) != 1 || entry.Actions[0] != "pull" {
			return errInvalidCredentials
		}
		repo, err := h.users.GetRepositoryByName(ctx, entry.Name)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !repo.Published) {
			return errInvalidCredentials
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// checkCredentials verifies username/password against the users table.
// Unknown users and wrong passwords both return errInvalidCredentials after
// identical bcrypt work: no user enumeration through status, body or timing.
func (h *TokenHandler) checkCredentials(ctx context.Context, username, password string) error {
	user, err := h.users.GetUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(password))
		return errInvalidCredentials
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil && !user.IsService {
		return nil
	}
	_, err = h.users.GetActiveAccessToken(ctx, db.GetActiveAccessTokenParams{
		UserID:    user.ID,
		TokenHash: HashAccessToken(password),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return errInvalidCredentials
	}
	return err
}

// writeToken writes the token response contract of the token auth spec:
// "token" with the OAuth-compatible "access_token" alias (identical values),
// "expires_in" in seconds and "issued_at" as RFC 3339 UTC.
func (h *TokenHandler) writeToken(w http.ResponseWriter, token string, claims *Claims) {
	// The response is a closed string-only struct: marshalling cannot fail.
	body, _ := json.Marshal(tokenResponse{
		Token:       token,
		AccessToken: token,
		ExpiresIn:   int64(h.signer.cfg.TokenTTL.Duration().Seconds()),
		IssuedAt:    claims.IssuedAt.Time.UTC().Format(time.RFC3339),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// tokenResponse is the token endpoint's success body.
type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

// tokenErrorBody is the token endpoint's error body. The /v2 OCI error
// envelope does not apply here: this is the token-auth-spec surface, whose
// clients read the status code and, for humans, one stable message.
type tokenErrorBody struct {
	Error string `json:"error"`
}

// writeTokenError writes the token endpoint's error contract.
func writeTokenError(w http.ResponseWriter, status int, message string) {
	// A single string field cannot fail to marshal.
	body, _ := json.Marshal(tokenErrorBody{Error: message})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// dummyPasswordHash is generated once per process and used only to burn
// bcrypt cycles for unknown users. It is not a credential.
var (
	dummyOnce sync.Once
	dummyHash []byte
)

func dummyPasswordHash() []byte {
	dummyOnce.Do(func() {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("invalid"), bcrypt.MinCost)
	})
	return dummyHash
}
