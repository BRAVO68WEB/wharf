//go:build ignore

// Session auth smoke test over a real socket: login sets the cookie, the
// cookie passes RequireSession, logout clears it, and an expired session is
// redirected to the login page. Run: go run testdata/smoke_session.go
package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/go-chi/chi/v5"
	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/config"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

var failures int

func check(ok bool, format string, args ...any) {
	if ok {
		fmt.Printf("PASS  "+format+"\n", args...)
		return
	}
	failures++
	fmt.Printf("FAIL  "+format+"\n", args...)
}

func main() {
	ctx := httptest.NewRequest(http.MethodGet, "/", nil).Context()
	sqlDB, err := db.OpenAndMigrate(ctx, filepath.Join(os.TempDir(), fmt.Sprintf("smoke-session-%d.db", time.Now().UnixNano())))
	if err != nil {
		fmt.Println("OpenAndMigrate:", err)
		os.Exit(1)
	}
	defer func() { _ = sqlDB.Close() }()
	q := db.New(sqlDB)
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		fmt.Println("hash:", err)
		os.Exit(1)
	}
	user, err := q.CreateUser(ctx, db.CreateUserParams{Username: "alice", PasswordHash: string(hash)})
	if err != nil {
		fmt.Println("CreateUser:", err)
		os.Exit(1)
	}
	m, err := auth.NewSessionManager(q, config.SessionConfig{
		Secret: config.Secret("smoke-session-secret"),
		TTL:    config.Duration(time.Hour),
	}, true)
	if err != nil {
		fmt.Println("NewSessionManager:", err)
		os.Exit(1)
	}
	r := chi.NewRouter()
	m.Mount(r)
	protected := m.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, _ := auth.SessionUserFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "hello %s\n", u.Username)
	}))
	r.Get("/protected", protected.ServeHTTP)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(1)
	}
	defer func() { _ = ln.Close() }()
	go func() { _ = http.Serve(ln, r) }()
	base := "http://" + ln.Addr().String()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	fmt.Println("== happy: login sets cookie, cookie passes middleware ==")
	loginResp := postForm(client, base+auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"s3cret"}}, nil)
	check(loginResp.StatusCode == http.StatusSeeOther, "login status = %d, want 303", loginResp.StatusCode)
	var cookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c
		}
	}
	check(cookie != nil, "login sets %q cookie", auth.SessionCookieName)
	if cookie == nil {
		os.Exit(1)
	}
	check(cookie.HttpOnly && cookie.SameSite == http.SameSiteStrictMode && cookie.Secure,
		"cookie hardened: HttpOnly=%v SameSite=%v Secure=%v", cookie.HttpOnly, cookie.SameSite, cookie.Secure)
	getResp := get(client, base+"/protected", cookie)
	check(getResp.StatusCode == http.StatusOK, "protected with cookie = %d, want 200", getResp.StatusCode)
	bare := get(client, base+"/protected")
	check(bare.StatusCode == http.StatusFound && bare.Header.Get("Location") == base+auth.SessionLoginPath,
		"protected without cookie = %d -> %q, want 302 -> %s", bare.StatusCode, bare.Header.Get("Location"), base+auth.SessionLoginPath)

	fmt.Println("== failure: expired session cookie redirected to login ==")
	token, _, _ := strings.Cut(cookie.Value, ".")
	if err := q.DeleteSession(ctx, token); err != nil {
		fmt.Println("DeleteSession:", err)
		os.Exit(1)
	}
	err = q.CreateSession(ctx, db.CreateSessionParams{Token: token, UserID: user.ID, ExpiresAt: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		fmt.Println("CreateSession:", err)
		os.Exit(1)
	}
	expired := get(client, base+"/protected", cookie)
	check(expired.StatusCode == http.StatusFound && expired.Header.Get("Location") == base+auth.SessionLoginPath,
		"expired cookie = %d -> %q, want 302 -> %s", expired.StatusCode, expired.Header.Get("Location"), base+auth.SessionLoginPath)

	fmt.Println("== logout clears session ==")
	loginResp = postForm(client, base+auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"s3cret"}}, nil)
	for _, c := range loginResp.Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c
		}
	}
	logoutResp := postForm(client, base+auth.SessionLogoutPath, nil, cookie)
	check(logoutResp.StatusCode == http.StatusSeeOther, "logout status = %d, want 303", logoutResp.StatusCode)
	cleared := false
	for _, c := range logoutResp.Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	check(cleared, "logout clears the cookie")
	after := get(client, base+"/protected", cookie)
	check(after.StatusCode == http.StatusFound, "old cookie after logout = %d, want 302", after.StatusCode)

	if failures > 0 {
		fmt.Printf("\n%d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nall smoke checks passed")
}

func postForm(client *http.Client, target string, form url.Values, cookie *http.Cookie) *http.Response {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		fmt.Println("request:", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("do:", err)
		os.Exit(1)
	}
	_ = resp.Body.Close()
	return resp
}

func get(client *http.Client, target string, cookies ...*http.Cookie) *http.Response {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		fmt.Println("request:", err)
		os.Exit(1)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("do:", err)
		os.Exit(1)
	}
	_ = resp.Body.Close()
	return resp
}
