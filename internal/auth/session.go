package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/bcrypt"
	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/config"
	"github.com/BRAVO68WEB/wharf/internal/db"
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
	ID        int64
	Username  string
	IsAdmin   bool
	IsService bool
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

// sessionLoginURL is the absolute login URL for the request's own host.
func sessionLoginURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + SessionLoginPath
}

func (m *SessionManager) Mount(r chi.Router) {
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
	opened, status := m.startSession(r, r.PostFormValue("username"), r.PostFormValue("password"))
	if status == http.StatusInternalServerError {
		http.Error(w, "internal server error", status)
		return
	}
	if status != http.StatusOK {
		m.rejectLogin(w)
		return
	}
	http.SetCookie(w, m.sessionCookie(opened.token, opened.expires))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// openedSession is a session row that was just created. token is the raw
// session token: the cookie signs it, and dashboard CSRF compares it to the
// X-CSRF-Token header. The cookie is HttpOnly, so a browser UI can only learn
// the token from a JSON response that returns it on purpose.
type openedSession struct {
	token    string
	username string
	admin    bool
	expires  time.Time
}

// startSession checks credentials and inserts a session row. Empty
// credentials, an unknown user, and a wrong password all return 401. The
// dummy bcrypt compare keeps unknown users from answering faster than wrong
// passwords. Callers choose the response shape (form redirect or JSON).
func (m *SessionManager) startSession(r *http.Request, username, password string) (openedSession, int) {
	if username == "" || password == "" {
		return openedSession{}, http.StatusUnauthorized
	}
	user, err := m.q.GetUserByUsername(r.Context(), username)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(sessionDummyPasswordHash, []byte(password))
		return openedSession{}, http.StatusUnauthorized
	}
	if err != nil {
		return openedSession{}, http.StatusInternalServerError
	}
	if user.IsService || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return openedSession{}, http.StatusUnauthorized
	}
	token, err := newSessionToken()
	if err != nil {
		return openedSession{}, http.StatusInternalServerError
	}
	expiresAt := time.Now().Add(m.ttl)
	err = m.q.CreateSession(r.Context(), db.CreateSessionParams{
		Token:     token,
		UserID:    user.ID,
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		return openedSession{}, http.StatusInternalServerError
	}
	return openedSession{token: token, username: user.Username, admin: user.IsAdmin, expires: expiresAt}, http.StatusOK
}

// HandleLogout ends the session: deletes the row and clears the cookie, then
// sends the browser to the login page.
func (m *SessionManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	m.endSession(w, r)
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
				ID:        user.ID,
				Username:  user.Username,
				IsAdmin:   user.IsAdmin,
				IsService: user.IsService,
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

// ContextWithSession returns ctx carrying user. Tests and handlers use it
// to know who is signed in.
func ContextWithSession(ctx context.Context, user SessionUser) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, &Session{User: user})
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

// endSession deletes the session row when the cookie still verifies and
// clears the cookie either way. A leftover row dies with its TTL at the latest.
func (m *SessionManager) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		if token, ok := m.parseSessionCookie(c.Value); ok {
			_ = m.q.DeleteSession(r.Context(), token)
		}
	}
	http.SetCookie(w, m.clearedSessionCookie())
}

// denySession sends the client to the login page. Dashboard JSON calls get
// 401 JSON instead: a redirect would make fetch follow into HTML.
func (m *SessionManager) denySession(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/dashboard/api" || strings.HasPrefix(r.URL.Path, "/dashboard/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthenticated"}` + "\n"))
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
