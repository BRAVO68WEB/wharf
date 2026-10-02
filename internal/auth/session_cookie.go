package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

// Cookie shape and MAC verification for dashboard sessions. The cookie value
// is "<token>.<sig>": the token indexes the sessions table and the signature
// rejects forged values without a database round-trip.

func (m *SessionManager) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    m.signSessionCookie(token),
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.ttl.Seconds()),
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (m *SessionManager) clearedSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (m *SessionManager) signSessionCookie(token string) string {
	return token + "." + base64.RawURLEncoding.EncodeToString(m.sessionMAC(token))
}

func (m *SessionManager) parseSessionCookie(value string) (string, bool) {
	token, sig, ok := strings.Cut(value, ".")
	if !ok || token == "" || sig == "" {
		return "", false
	}
	want := base64.RawURLEncoding.EncodeToString(m.sessionMAC(token))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", false
	}
	return token, true
}

func (m *SessionManager) sessionMAC(token string) []byte {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(token))
	return mac.Sum(nil)
}
