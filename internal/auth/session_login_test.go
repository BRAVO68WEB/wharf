package auth_test

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/db"
)

func Test_LoginRejected_when_PasswordWrong(t *testing.T) {
	// Given: a user whose password is "s3cret".
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)

	// When: login is posted with a wrong password.
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"wrong"}})

	// Then: 401 with no session cookie at all.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("cookies = %v, want none", resp.Cookies())
	}
}

func Test_LoginRejected_when_UserUnknown(t *testing.T) {
	// Given: a database with no users at all.
	m, _ := newSessionTestManager(t, time.Hour, true)
	h := newSessionTestMux(m)

	// When: login is posted for an unknown user.
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {"nobody"}, "password": {"guess"}})

	// Then: the same 401 as a wrong password, with no cookie.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("cookies = %v, want none", resp.Cookies())
	}
}

func Test_LoginRejected_when_CredentialsMissing(t *testing.T) {
	// Given: a user that could log in.
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)

	for name, form := range map[string]url.Values{
		"username": {"password": {"s3cret"}},
		"password": {"username": {"alice"}},
	} {
		// When: login is posted without one of the two fields.
		resp := sessionPostForm(t, h, auth.SessionLoginPath, form)

		// Then: 401 and no cookie.
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s missing: status = %d, want %d", name, resp.StatusCode, http.StatusUnauthorized)
		}
		if len(resp.Cookies()) != 0 {
			t.Errorf("%s missing: cookies = %v, want none", name, resp.Cookies())
		}
	}
}

func Test_LoginRejected_when_PasswordHashMalformed(t *testing.T) {
	// Given: a user whose stored hash is not a bcrypt hash.
	m, q := newSessionTestManager(t, time.Hour, true)
	_, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "alice",
		PasswordHash: "not-a-bcrypt-hash",
		IsAdmin:      false,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	h := newSessionTestMux(m)

	// When: login is posted with any password.
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"s3cret"}})

	// Then: 401 and no cookie — a corrupt hash must not authenticate.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("cookies = %v, want none", resp.Cookies())
	}
}

func Test_CookieOmitsSecureFlag_when_CookieSecureDisabled(t *testing.T) {
	// Given: a manager configured for plain-HTTP development.
	m, q := newSessionTestManager(t, time.Hour, false)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)

	// When: login succeeds.
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"s3cret"}})

	// Then: the cookie is set without the Secure flag but keeps the other
	// hardening attributes.
	c := sessionCookie(t, resp)
	if c.Secure {
		t.Error("cookie has Secure flag in dev mode")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie = %+v, want HttpOnly + SameSite=Strict", c)
	}
}

func Test_SessionExpiryFollowsTTL_when_TTLConfigured(t *testing.T) {
	// Given: a manager with a non-default TTL.
	const ttl = 90 * time.Minute
	m, q := newSessionTestManager(t, ttl, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)

	// When: login succeeds.
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"s3cret"}})

	// Then: the stored expiry and the cookie MaxAge both follow the TTL.
	token := sessionRawToken(t, sessionCookie(t, resp))
	row, err := q.GetSession(t.Context(), token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	want := time.Now().Add(ttl).Unix()
	if row.ExpiresAt < want-5 || row.ExpiresAt > want+5 {
		t.Errorf("expires_at = %d, want ~%d", row.ExpiresAt, want)
	}
	if got := sessionCookie(t, resp).MaxAge; got != int(ttl.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", got, int(ttl.Seconds()))
	}
}

func Test_TokensDiffer_when_LoginRepeated(t *testing.T) {
	// Given: a user that can log in repeatedly.
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)

	// When: two logins happen back to back.
	first := sessionRawToken(t, sessionLogin(t, h, "alice", "s3cret"))
	second := sessionRawToken(t, sessionLogin(t, h, "alice", "s3cret"))

	// Then: each session gets its own random token and both rows live.
	if first == second {
		t.Errorf("both logins produced token %q, want distinct tokens", first)
	}
	for _, token := range []string{first, second} {
		if _, err := q.GetSession(t.Context(), token); err != nil {
			t.Errorf("GetSession(%q): %v", token, err)
		}
	}
}

func Test_ManagerConstructionFails_when_SecretMissing(t *testing.T) {
	// Given: a session config with no secret.
	q := newSessionTestDB(t)

	// When: the manager is constructed.
	_, err := auth.NewSessionManager(q, config.SessionConfig{TTL: config.Duration(time.Hour)}, true)

	// Then: the secret sentinel is returned.
	if !errors.Is(err, auth.ErrSessionSecretMissing) {
		t.Fatalf("err = %v, want %v", err, auth.ErrSessionSecretMissing)
	}
}

func Test_ManagerConstructionFails_when_TTLInvalid(t *testing.T) {
	// Given: a session config with a zero TTL.
	q := newSessionTestDB(t)

	// When: the manager is constructed.
	_, err := auth.NewSessionManager(q, config.SessionConfig{Secret: config.Secret(sessionTestSecret)}, true)

	// Then: the TTL sentinel is returned.
	if !errors.Is(err, auth.ErrSessionTTLInvalid) {
		t.Fatalf("err = %v, want %v", err, auth.ErrSessionTTLInvalid)
	}
}

func Test_SecretComesFromEnvVar_when_ConfigLoaded(t *testing.T) {
	// Given: a config file naming an env var for the session secret and that
	// variable set in the environment — the secret never appears in YAML.
	t.Setenv("BDR_TEST_SESSION_SECRET", "env-derived-signing-secret-pad!!!")
	path := filepath.Join(t.TempDir(), "config.yml")
	err := os.WriteFile(path, []byte(`
server: {port: 5000}
storage: {type: filesystem, path: ./data/blobs}
database: {path: ./data/registry.db}
auth:
  jwt: {private_key_path: ./deploy/jwt_private.pem, issuer: test, service: registry.docker.io, token_ttl: 15m}
  session: {secret_env: BDR_TEST_SESSION_SECRET, ttl: 24h}
gc: {batch_size: 256}
logging: {level: info}
`), 0o600)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	// When: the config is loaded and a manager is built from it.
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	q := newSessionTestDB(t)
	m, err := auth.NewSessionManager(q, cfg.Auth.Session, true)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	h := newSessionTestMux(m)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	cookie := sessionLogin(t, h, "alice", "s3cret")

	// Then: the env-derived secret signs cookies the middleware accepts.
	resp := sessionGet(t, h, "/protected", cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
