package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
server:
  port: 5000
  read_timeout: 0s
  write_timeout: 0s
  max_upload_size: 0

storage:
  type: filesystem
  path: /tmp/blobs
  s3:
    endpoint: ""
    region: us-east-1
    bucket: registry-blobs
    access_key_env: ""
    secret_key_env: ""
    force_path_style: true

database:
  path: /tmp/registry.db

auth:
  jwt:
    private_key_path: /tmp/jwt.pem
    issuer: test-issuer
    service: test-service
    token_ttl: 15m
  session:
    secret_env: REGISTRY_SESSION_SECRET
    ttl: 24h

gc:
  batch_size: 256
  interval: 0s

logging:
  level: info
`

func loadYAML(t *testing.T, raw string) (*Config, error) {
	t.Helper()
	t.Setenv("REGISTRY_SESSION_SECRET", "test-session-secret")
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return Load(path)
}

func mutate(raw string, repl ...[2]string) string {
	for _, r := range repl {
		raw = strings.Replace(raw, r[0], r[1], 1)
	}
	return raw
}

func TestLoad_ValidConfig(t *testing.T) {
	// Given: the shipped sample config and its session-secret env var
	t.Setenv("REGISTRY_SESSION_SECRET", "test-session-secret")
	sample := filepath.Join("..", "..", "testdata", "config.yml")

	// When: Load runs
	cfg, err := Load(sample)

	// Then: every section parses into the struct
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != 5000 {
		t.Errorf("Server.Port = %d, want 5000", cfg.Server.Port)
	}
	if cfg.Server.ListenAddr() != ":5000" {
		t.Errorf("ListenAddr() = %q, want \":5000\"", cfg.Server.ListenAddr())
	}
	if got := cfg.Server.MaxUploadSize; got != 0 {
		t.Errorf("Server.MaxUploadSize = %d, want 0", got)
	}
	if cfg.Storage.Type != StorageFilesystem || cfg.Storage.Path != "./data/blobs" {
		t.Errorf("Storage = %+v, want filesystem ./data/blobs", cfg.Storage)
	}
	if cfg.Storage.S3.Bucket != "registry-blobs" || !cfg.Storage.S3.ForcePathStyle {
		t.Errorf("Storage.S3 = %+v, want bucket registry-blobs + force_path_style", cfg.Storage.S3)
	}
	if cfg.Database.Path != "./data/registry.db" {
		t.Errorf("Database.Path = %q", cfg.Database.Path)
	}
	if cfg.Auth.JWT.TokenTTL.Duration() != 15*time.Minute {
		t.Errorf("Auth.JWT.TokenTTL = %v, want 15m", cfg.Auth.JWT.TokenTTL.Duration())
	}
	if cfg.Auth.Session.TTL.Duration() != 24*time.Hour {
		t.Errorf("Auth.Session.TTL = %v, want 24h", cfg.Auth.Session.TTL.Duration())
	}
	if cfg.Auth.Session.Secret.Reveal() != "test-session-secret" {
		t.Error("Auth.Session.Secret not resolved from env")
	}
	host, err := cfg.Server.PullHost()
	if err != nil || host != "" {
		t.Errorf("PullHost() = %q, %v; want empty", host, err)
	}
	withHost := mutate(validYAML, [2]string{"max_upload_size: 0", "max_upload_size: 0\n  public_url: https://registry.example.com:5000"})
	cfg, err = loadYAML(t, withHost)
	if err != nil {
		t.Fatalf("Load(public_url) error = %v", err)
	}
	host, err = cfg.Server.PullHost()
	if err != nil || host != "registry.example.com:5000" {
		t.Errorf("PullHost() = %q, %v; want registry.example.com:5000", host, err)
	}
	if cfg.GC.BatchSize != 256 || cfg.Logging.Level != LogInfo {
		t.Errorf("GC/Logging = %d/%q, want 256/info", cfg.GC.BatchSize, cfg.Logging.Level)
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		repl    [][2]string
		wantErr string
	}{
		{"missing storage.type", [][2]string{{"  type: filesystem\n", ""}}, "storage.type is required"},
		{"invalid storage.type", [][2]string{{"type: filesystem", "type: gcs"}}, "storage.type must be"},
		{"missing storage.path", [][2]string{{"  path: /tmp/blobs\n", ""}}, "storage.path is required"},
		{"s3 missing bucket", [][2]string{{"type: filesystem", "type: s3"}, {"bucket: registry-blobs", "bucket: \"\""}}, "storage.s3.bucket is required"},
		{"s3 missing region", [][2]string{{"type: filesystem", "type: s3"}, {"region: us-east-1", "region: \"\""}}, "storage.s3.region is required"},
		{"missing database.path", [][2]string{{"  path: /tmp/registry.db\n", ""}}, "database.path is required"},
		{"missing private_key_path", [][2]string{{"private_key_path: /tmp/jwt.pem", "private_key_path: \"\""}}, "auth.jwt.private_key_path is required"},
		{"zero token_ttl", [][2]string{{"token_ttl: 15m", "token_ttl: 0s"}}, "auth.jwt.token_ttl"},
		{"missing secret_env", [][2]string{{"secret_env: REGISTRY_SESSION_SECRET", "secret_env: \"\""}}, "auth.session.secret_env is required"},
		{"zero session ttl", [][2]string{{"ttl: 24h", "ttl: 0s"}}, "auth.session.ttl"},
		{"zero batch_size", [][2]string{{"batch_size: 256", "batch_size: 0"}}, "gc.batch_size"},
		{"negative gc interval", [][2]string{{"interval: 0s", "interval: -1s"}}, "gc.interval"},
		{"bad logging.level", [][2]string{{"level: info", "level: verbose"}}, "logging.level"},
		{"zero port", [][2]string{{"port: 5000", "port: 0"}}, "server.port"},
		{"public url with a path", [][2]string{{"max_upload_size: 0", "max_upload_size: 0\n  public_url: https://registry.example.com/v2"}}, "server.public_url must not include a path"},
		{"public url without a scheme", [][2]string{{"max_upload_size: 0", "max_upload_size: 0\n  public_url: registry.example.com"}}, "server.public_url must be an absolute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: config with exactly one broken field
			raw := mutate(validYAML, tc.repl...)

			// When: Load runs
			_, err := loadYAML(t, raw)

			// Then: error names the offending field
			if err == nil {
				t.Fatal("Load() error = nil, want failure")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Load() error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoad_SessionSecretEnvUnset(t *testing.T) {
	// Given: valid config naming an env var that is not set
	t.Setenv("REGISTRY_SESSION_SECRET", "")
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// When: Load runs
	_, err := Load(path)

	// Then: error names auth.session.secret_env
	if err == nil || !strings.Contains(err.Error(), "auth.session.secret_env") {
		t.Errorf("Load() error = %v, want auth.session.secret_env failure", err)
	}
}

func TestLoad_DurationForms(t *testing.T) {
	// Given: duration strings and bare-integer seconds both appear in YAML
	for raw, want := range map[string]time.Duration{
		"token_ttl: 2m": 2 * time.Minute,
		"token_ttl: 90": 90 * time.Second,
	} {
		cfg, err := loadYAML(t, mutate(validYAML, [][2]string{{"token_ttl: 15m", raw}}...))
		if err != nil {
			t.Fatalf("Load(%q) error = %v", raw, err)
		}
		// Then: both forms resolve to the same duration type
		if got := cfg.Auth.JWT.TokenTTL.Duration(); got != want {
			t.Errorf("%q parsed to %v, want %v", raw, got, want)
		}
	}

	// Given: a malformed duration
	_, err := loadYAML(t, mutate(validYAML, [][2]string{{"token_ttl: 15m", "token_ttl: soon"}}...))
	if err == nil || !strings.Contains(err.Error(), "duration") {
		t.Errorf("Load() error = %v, want duration parse failure", err)
	}
}

func TestLoad_SecretRedaction(t *testing.T) {
	// Given: a config with a resolved session secret
	cfg, err := loadYAML(t, validYAML)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// When: the config is formatted for logs
	for _, out := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), cfg.Auth.Session.Secret.String()} {
		// Then: the secret value never appears
		if strings.Contains(out, "test-session-secret") {
			t.Fatalf("secret leaked into formatted output: %s", out)
		}
	}
	if cfg.Auth.Session.Secret.Reveal() != "test-session-secret" {
		t.Error("Reveal() must return the raw secret")
	}
}

func TestLoad_UnknownFieldRejected(t *testing.T) {
	// Given: a typo'd key strict decoding must catch
	raw := mutate(validYAML, [][2]string{{"  port: 5000", "  prot: 5000"}}...)

	// When: Load runs
	_, err := loadYAML(t, raw)

	// Then: the unknown field is rejected
	if err == nil || !strings.Contains(err.Error(), "prot") {
		t.Errorf("Load() error = %v, want unknown-field error naming prot", err)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Error("Load() error = nil, want failure for missing file")
	}
	if _, err := Load(""); err == nil {
		t.Error("Load() error = nil, want failure for empty path")
	}
}

func TestLoad_InvalidConfigExitsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test skipped in -short mode")
	}
	// Given: a config missing storage.type, run through the real binary
	bad := mutate(validYAML, [][2]string{{"  type: filesystem\n", ""}}...)
	path := filepath.Join(t.TempDir(), "bad.yml")
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// When: the process loads it
	cmd := exec.Command("go", "run", "../../cmd/registry", "--config", path)
	cmd.Env = append(os.Environ(), "REGISTRY_SESSION_SECRET=test-session-secret")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	// Then: it exits 1 and stderr names the field
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run error = %v, want exit code 1", err)
	}
	if !strings.Contains(stderr.String(), "storage.type") {
		t.Errorf("stderr = %q, want it to contain storage.type", stderr.String())
	}
}
