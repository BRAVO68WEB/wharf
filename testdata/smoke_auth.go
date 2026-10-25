//go:build ignore

// Bearer auth middleware smoke test over a real socket with a real RS256
// signer: unauthenticated /v2 is challenged, pull tokens can GET but not PUT,
// push tokens can PUT (QA scenarios), the version check answers 200/401, and
// /token stays public. Run: go run testdata/smoke_auth.go
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/config"
	"github.com/BRAVO68WEB/wharf/internal/middleware"
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

func token(t *auth.Signer, subject string, access ...auth.AccessEntry) string {
	signed, err := t.Sign(t.NewClaims(subject, access))
	if err != nil {
		fmt.Println("Sign:", err)
		os.Exit(1)
	}
	return signed
}

func main() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fmt.Println("GenerateKey:", err)
		os.Exit(1)
	}
	keyPath := filepath.Join(os.TempDir(), fmt.Sprintf("smoke-auth-%d.pem", time.Now().UnixNano()))
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		fmt.Println("write key:", err)
		os.Exit(1)
	}
	defer func() { _ = os.Remove(keyPath) }()
	signer, err := auth.LoadSigner(config.JWTConfig{
		PrivateKeyPath: keyPath,
		Issuer:         "smoke-issuer",
		Service:        "smoke-service",
		TokenTTL:       config.Duration(time.Hour),
	})
	if err != nil {
		fmt.Println("LoadSigner:", err)
		os.Exit(1)
	}

	manifests := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := middleware.AuthUser(r.Context())
		_, _ = fmt.Fprintf(w, "%s by %s", r.Method, user)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: middleware.BearerAuth(signer, "smoke-service", "")(manifests), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	base := "http://" + ln.Addr().String()

	pull := token(signer, "alice", auth.AccessEntry{Type: "repository", Name: "app", Actions: []string{"pull"}})
	push := token(signer, "bob", auth.AccessEntry{Type: "repository", Name: "app", Actions: []string{"push"}})
	probe := token(signer, "carol")

	do := func(method, target, bearer string) (*http.Response, string) {
		req, _ := http.NewRequest(method, base+target, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println("request:", err)
			os.Exit(1)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(body)
	}

	resp, body := do(http.MethodGet, "/v2/app/manifests/latest", "")
	challenge := `Bearer realm="http://` + ln.Addr().String() + `/token",service="smoke-service",scope=""`
	check(resp.StatusCode == 401, "unauthenticated GET /v2 manifests -> 401 (got %d)", resp.StatusCode)
	check(resp.Header.Get("WWW-Authenticate") == challenge, "401 carries challenge %q (got %q)", challenge, resp.Header.Get("WWW-Authenticate"))
	check(body == `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required","detail":{}}]}`, "401 body is UNAUTHORIZED (got %s)", body)

	resp, body = do(http.MethodGet, "/v2/app/manifests/latest", pull)
	check(resp.StatusCode == 200 && body == "GET by alice", "QA-adjacent: pull token GET -> 200 (got %d %q)", resp.StatusCode, body)

	resp, body = do(http.MethodPut, "/v2/app/manifests/latest", pull)
	check(resp.StatusCode == 403, "QA failure: pull token PUT -> 403 (got %d)", resp.StatusCode)
	check(body == `{"errors":[{"code":"DENIED","message":"requested access to the resource is denied","detail":{}}]}`, "403 body is DENIED (got %s)", body)

	resp, body = do(http.MethodPut, "/v2/app/manifests/latest", push)
	check(resp.StatusCode == 200 && body == "PUT by bob", "QA happy: push token PUT -> 200 (got %d %q)", resp.StatusCode, body)

	resp, _ = do(http.MethodGet, "/v2/", "")
	check(resp.StatusCode == 401, "version check without token -> 401 (got %d)", resp.StatusCode)
	resp, _ = do(http.MethodGet, "/v2/", probe)
	check(resp.StatusCode == 200, "version check with scope-less token -> 200 (got %d)", resp.StatusCode)
	check(resp.Header.Get("Docker-Distribution-API-Version") == "registry/2.0", "version check carries API version header")

	resp, _ = do(http.MethodGet, "/token", "")
	check(resp.StatusCode == 200, "/token public without credentials -> 200 (got %d)", resp.StatusCode)

	if failures > 0 {
		fmt.Printf("\n%d FAILURES\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nall smoke checks passed")
}
