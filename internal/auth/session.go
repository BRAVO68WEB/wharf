package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/db"
)

const (
	SessionCookieName = "session"
	SessionLoginPath  = "/dashboard/login"
	SessionLogoutPath = "/dashboard/logout"

	sessionTokenBytes   = 32
	sessionMaxFormBytes = 1 << 16
)

var (
	ErrSessionSecretMissing = errors.New("auth: session secret must not be empty")
	ErrSessionTTLInvalid    = errors.New("auth: session ttl must be greater than zero")
)

// sessionDummyPasswordHash is a bcrypt hash of a value nobody knows. Login
// runs a comparison against it when the user does not exist so unknown users
// do not answer faster than wrong passwords (user-enumeration timing).
// Generated once with bcrypt.GenerateFromPassword(DefaultCost).
var sessionDummyPasswordHash = []byte("$2a$10$ECTTgEIJBf78DCKdGNaZLuUQ13v7LSGkiNQBIj5EdoT2X60sRoejC")

type SessionUser struct {
	ID       int64
	Username string
	IsAdmin  bool
}

type Session struct {
	Token     string
	User      SessionUser
	ExpiresAt time.Time
}

type sessionContextKey struct{}

// SessionManager implements dashboard session auth: form login, HttpOnly
// cookie sessions stored in SQLite, and the RequireSession middleware.
//
// Cookie value is "<token>.<sig>": token is the random session token stored
// in the sessions table, sig is base64url(HMAC-SHA256(secret, token)). The
// signature rejects forged cookie values without a database round-trip and
// keeps a leaked sessions table from yielding usable cookies (the signing
// secret lives only in the environment). CSRF checks compare the raw token
// from SessionTokenFromContext against the X-CSRF-Token header.
type SessionManager struct {
	q            db.Querier
	secret       []byte
	ttl          time.Duration
	cookieSecure bool
}

func NewSessionManager(q db.Querier, cfg config.SessionConfig, cookieSecure bool) (*SessionManager, error) {
	secret := []byte(cfg.Secret.Reveal())
	if len(secret) == 0 {
		return nil, ErrSessionSecretMissing
	}
	ttl := cfg.TTL.Duration()
	if ttl <= 0 {
		return nil, ErrSessionTTLInvalid
	}
	return &SessionManager{q: q, secret: secret, ttl: ttl, cookieSecure: cookieSecure}, nil
}

func (m *SessionManager) Mount(r chi.Router) {
	r.Get(SessionLoginPath, m.HandleLoginPage)
	r.Post(SessionLoginPath, m.HandleLogin)
	r.Post(SessionLogoutPath, m.HandleLogout)
}

// HandleLogin verifies form credentials and starts a session. All failures
// answer 401 with one message: no user enumeration through status or text.
func (m *SessionManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, sessionMaxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid login form", http.StatusBadRequest)
		return
	}
	username := r.PostFormValue("username")
	password := r.PostFormValue("password")
	if username == "" || password == "" {
		m.rejectLogin(w)
		return
	}
	user, err := m.q.GetUserByUsername(r.Context(), username)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(sessionDummyPasswordHash, []byte(password))
		m.rejectLogin(w)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		m.rejectLogin(w)
		return
	}
	token, err := newSessionToken()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(m.ttl)
	err = m.q.CreateSession(r.Context(), db.CreateSessionParams{
		Token:     token,
		UserID:    user.ID,
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, m.sessionCookie(token, expiresAt))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// HandleLogout ends the session: deletes the row and clears the cookie.
// HTMX callers get an HX-Redirect for the same reason denySession does it: a
// 303 followed by XHR swaps the login page into the calling element instead of
// navigating the browser.
func (m *SessionManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		if token, ok := m.parseSessionCookie(c.Value); ok {
			// Best effort: the cleared cookie is what ends the session for
			// the client; a leftover row dies with its TTL at the latest.
			_ = m.q.DeleteSession(r.Context(), token)
		}
	}
	http.SetCookie(w, m.clearedSessionCookie())
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", SessionLoginPath)
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, sessionLoginURL(r), http.StatusSeeOther)
}

// RequireSession rejects requests without a live session and injects the
// authenticated user into the request context.
func (m *SessionManager) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookieName)
		if err != nil {
			m.denySession(w, r)
			return
		}
		token, ok := m.parseSessionCookie(c.Value)
		if !ok {
			m.denySession(w, r)
			return
		}
		row, err := m.q.GetSession(r.Context(), token)
		if errors.Is(err, sql.ErrNoRows) {
			m.denySession(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if row.ExpiresAt <= time.Now().Unix() {
			_ = m.q.DeleteSession(r.Context(), token)
			m.denySession(w, r)
			return
		}
		user, err := m.q.GetUserByID(r.Context(), row.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			m.denySession(w, r)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		sess := &Session{
			Token: token,
			User: SessionUser{
				ID:       user.ID,
				Username: user.Username,
				IsAdmin:  user.IsAdmin,
			},
			ExpiresAt: time.Unix(row.ExpiresAt, 0),
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, sess)))
	})
}

func SessionFromContext(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionContextKey{}).(*Session)
	return s, ok
}

func SessionUserFromContext(ctx context.Context) (SessionUser, bool) {
	s, ok := SessionFromContext(ctx)
	if !ok {
		return SessionUser{}, false
	}
	return s.User, true
}

// SessionTokenFromContext returns the raw session token: the value CSRF
// checks compare the X-CSRF-Token header against.
func SessionTokenFromContext(ctx context.Context) (string, bool) {
	s, ok := SessionFromContext(ctx)
	if !ok {
		return "", false
	}
	return s.Token, true
}

func (m *SessionManager) rejectLogin(w http.ResponseWriter) {
	http.Error(w, "invalid username or password", http.StatusUnauthorized)
}

// denySession sends the client to the login page. HTMX requests get an
// HX-Redirect: a 302 followed by XHR would swap the login page into the
// calling element instead of navigating the browser.
func (m *SessionManager) denySession(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", SessionLoginPath)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, sessionLoginURL(r), http.StatusFound)
}

func newSessionToken() (string, error) {
	var b [sessionTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
