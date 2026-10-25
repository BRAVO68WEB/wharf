package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Valid values for Config.Storage.Type and Config.Logging.Level.
const (
	StorageFilesystem = "filesystem"
	StorageS3         = "s3"

	LogDebug = "debug"
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"
)

// Load reads the YAML configuration file at path, resolves secrets from the
// environment, and validates the result. Every validation error names the
// offending field (e.g. "storage.type is required").
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("config: --config flag is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	// KnownFields rejects unknown keys so typos fail at startup instead of
	// silently leaving a setting at its zero value.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg.resolveEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resolveEnv resolves secrets referenced by environment-variable name. AWS
// credentials are intentionally not resolved here: the AWS SDK default
// credential chain handles them at storage-driver construction time.
func (cfg *Config) resolveEnv() {
	if name := cfg.Auth.Session.SecretEnv; name != "" {
		cfg.Auth.Session.Secret = Secret(os.Getenv(name))
	}
}

// Validate reports the first schema or constraint violation, naming the
// field in the error message.
func (cfg *Config) Validate() error {
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return errors.New("server.port must be between 1 and 65535")
	}
	if cfg.Server.ReadTimeout < 0 {
		return errors.New("server.read_timeout must not be negative")
	}
	if cfg.Server.WriteTimeout < 0 {
		return errors.New("server.write_timeout must not be negative")
	}
	if cfg.Server.MaxUploadSize < 0 {
		return errors.New("server.max_upload_size must not be negative")
	}
	if _, err := cfg.Server.PullHost(); err != nil {
		return err
	}

	switch cfg.Storage.Type {
	case StorageFilesystem:
		if cfg.Storage.Path == "" {
			return errors.New("storage.path is required when storage.type is \"filesystem\"")
		}
	case StorageS3:
		if cfg.Storage.S3.Bucket == "" {
			return errors.New("storage.s3.bucket is required when storage.type is \"s3\"")
		}
		if cfg.Storage.S3.Region == "" {
			return errors.New("storage.s3.region is required when storage.type is \"s3\"")
		}
	case "":
		return errors.New("storage.type is required")
	default:
		return fmt.Errorf("storage.type must be %q or %q, got %q", StorageFilesystem, StorageS3, cfg.Storage.Type)
	}

	if cfg.Database.Path == "" {
		return errors.New("database.path is required")
	}

	if cfg.Auth.JWT.PrivateKeyPath == "" {
		return errors.New("auth.jwt.private_key_path is required")
	}
	if cfg.Auth.JWT.Issuer == "" {
		return errors.New("auth.jwt.issuer is required")
	}
	if cfg.Auth.JWT.Service == "" {
		return errors.New("auth.jwt.service is required")
	}
	if cfg.Auth.JWT.TokenTTL <= 0 {
		return errors.New("auth.jwt.token_ttl must be greater than zero")
	}

	if cfg.Auth.Session.SecretEnv == "" {
		return errors.New("auth.session.secret_env is required")
	}
	if cfg.Auth.Session.Secret == "" {
		return fmt.Errorf("auth.session.secret_env: environment variable %q is empty or unset", cfg.Auth.Session.SecretEnv)
	}
	if cfg.Auth.Session.TTL <= 0 {
		return errors.New("auth.session.ttl must be greater than zero")
	}

	if cfg.GC.BatchSize <= 0 {
		return errors.New("gc.batch_size must be greater than zero")
	}
	if cfg.GC.Interval < 0 {
		return errors.New("gc.interval must not be negative")
	}

	switch cfg.Logging.Level {
	case LogDebug, LogInfo, LogWarn, LogError:
	default:
		return fmt.Errorf("logging.level must be one of %q, %q, %q, %q, got %q",
			LogDebug, LogInfo, LogWarn, LogError, cfg.Logging.Level)
	}

	return nil
}
