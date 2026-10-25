// Package config loads and validates registry configuration from YAML,
// resolving secrets from environment variables (never from the file itself).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from YAML as either a Go
// duration string ("30s", "5m20s") or an integer number of seconds.
type Duration time.Duration

// Duration returns d as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar like \"30s\" or an integer number of seconds, got %s", node.ShortTag())
	}
	var s string
	if err := node.Decode(&s); err == nil {
		if parsed, err := time.ParseDuration(s); err == nil {
			*d = Duration(parsed)
			return nil
		}
	}
	var secs int64
	if err := node.Decode(&secs); err == nil {
		*d = Duration(time.Duration(secs) * time.Second)
		return nil
	}
	return fmt.Errorf("invalid duration %q (want e.g. \"30s\", \"5m\", or an integer number of seconds)", node.Value)
}

// Secret is a sensitive value that redacts itself in all formatting, so it
// cannot leak into logs via fmt or slog.
type Secret string

// Reveal returns the secret value. Callers must never log the result.
func (s Secret) Reveal() string { return string(s) }

// String implements fmt.Stringer, always redacting the value.
func (Secret) String() string { return "[REDACTED]" }

// GoString implements fmt.GoStringer, always redacting the value.
func (Secret) GoString() string { return "[REDACTED]" }

// Config is the runtime configuration for the registry process.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	GC       GCConfig       `yaml:"gc"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	// Port is the TCP port to listen on.
	Port int `yaml:"port"`
	// ReadTimeout bounds reading the full request, including the body.
	// Zero disables it; keep it zero because blob uploads stream for a
	// long time and a read timeout would truncate them.
	ReadTimeout Duration `yaml:"read_timeout"`
	// WriteTimeout bounds writing the full response. Zero disables it;
	// keep it zero so multi-GB blob downloads are never cut off.
	WriteTimeout Duration `yaml:"write_timeout"`
	// MaxUploadSize is the largest accepted blob upload in bytes.
	// Zero means unlimited.
	MaxUploadSize int64 `yaml:"max_upload_size"`
	// PublicURL is the origin clients use to reach this registry, such as
	// "https://registry.example.com". Empty means the dashboard must not
	// invent a docker pull command.
	PublicURL string `yaml:"public_url"`
}

// ListenAddr returns the listen address for the configured port.
func (s ServerConfig) ListenAddr() string { return fmt.Sprintf(":%d", s.Port) }

// PullHost is the host[:port] from PublicURL, with no scheme. An empty
// PublicURL returns an empty host and no error. A URL with a path, query,
// fragment, or userinfo is rejected.
func (s ServerConfig) PullHost() (string, error) {
	if s.PublicURL == "" {
		return "", nil
	}
	u, err := url.Parse(s.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("server.public_url must be an absolute http(s) URL with a host")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("server.public_url must not include a path, query, or fragment")
	}
	return u.Host, nil
}

// ExternalOrigin is the scheme and host of PublicURL, with no path.
// https reports whether clients reach the registry over TLS. An empty
// PublicURL returns an empty origin and https false; the caller then
// derives the realm from the request.
func (s ServerConfig) ExternalOrigin() (origin string, https bool) {
	if s.PublicURL == "" {
		return "", false
	}
	u, err := url.Parse(s.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return u.Scheme + "://" + u.Host, u.Scheme == "https"
}

// StorageConfig selects and configures the blob storage backend.
type StorageConfig struct {
	// Type is the backend: "filesystem" or "s3".
	Type string `yaml:"type"`
	// Path is the blob root directory (filesystem backend).
	Path string `yaml:"path"`
	// S3 holds S3-compatible settings (s3 backend).
	S3 S3Config `yaml:"s3"`
}

// S3Config configures the S3-compatible blob backend.
type S3Config struct {
	// Endpoint is the service endpoint (MinIO, R2). Empty uses AWS S3.
	Endpoint string `yaml:"endpoint"`
	// Region is the service region (required for s3 backend).
	Region string `yaml:"region"`
	// Bucket is the target bucket (required for s3 backend).
	Bucket string `yaml:"bucket"`
	// AccessKeyEnv names the environment variable holding the access key.
	// Empty defers to the AWS SDK default credential chain.
	AccessKeyEnv string `yaml:"access_key_env"`
	// SecretKeyEnv names the environment variable holding the secret key.
	// Empty defers to the AWS SDK default credential chain.
	SecretKeyEnv string `yaml:"secret_key_env"`
	// ForcePathStyle forces path-style S3 addressing (required by MinIO).
	ForcePathStyle bool `yaml:"force_path_style"`
}

// DatabaseConfig holds SQLite metadata store settings.
type DatabaseConfig struct {
	// Path is the SQLite database file path.
	Path string `yaml:"path"`
}

// AuthConfig holds JWT token and dashboard session settings.
type AuthConfig struct {
	JWT     JWTConfig     `yaml:"jwt"`
	Session SessionConfig `yaml:"session"`
	// DevAnonymous disables Bearer enforcement on /v2/* so unauthenticated
	// clients (OCI conformance, local docker E2E) can push and pull. It never
	// belongs in production: the dashboard keeps its session auth regardless.
	DevAnonymous bool `yaml:"dev_anonymous"`
}

// JWTConfig configures the integrated auth token service.
type JWTConfig struct {
	// PrivateKeyPath is the PEM file with the RSA/ECDSA signing key.
	PrivateKeyPath string `yaml:"private_key_path"`
	// Issuer is the JWT "iss" claim.
	Issuer string `yaml:"issuer"`
	// Service is the JWT "aud" claim expected by Docker clients.
	Service string `yaml:"service"`
	// TokenTTL is how long issued tokens stay valid.
	TokenTTL Duration `yaml:"token_ttl"`
}

// SessionConfig configures dashboard session cookies.
type SessionConfig struct {
	// SecretEnv names the environment variable holding the session
	// signing secret. The secret itself never appears in YAML; Load
	// resolves it from the environment into Secret.
	SecretEnv string `yaml:"secret_env"`
	// Secret is resolved from SecretEnv at load time.
	Secret Secret `yaml:"-"`
	// TTL is how long a dashboard session stays valid.
	TTL Duration `yaml:"ttl"`
}

// GCConfig configures garbage collection.
type GCConfig struct {
	// BatchSize is the number of blobs swept per GC batch.
	BatchSize int `yaml:"batch_size"`
	// Interval is how often a collection starts on its own. Zero, the
	// default, leaves collection as a dashboard action only. A tick that
	// finds a collection already running is skipped.
	Interval Duration `yaml:"interval"`
}

// LoggingConfig configures structured logging.
type LoggingConfig struct {
	// Level is one of "debug", "info", "warn", "error".
	Level string `yaml:"level"`
}
