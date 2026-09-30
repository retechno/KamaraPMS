// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is the deployment environment.
type Env string

const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvProduction  Env = "production"
)

// Config is the complete process configuration.
type Config struct {
	Env             Env
	HTTPAddr        string        // PMS_HTTP_ADDR, default ":8080"
	DatabaseURL     string        // PMS_DATABASE_URL, required
	DBMaxConns      int32         // PMS_DB_MAX_CONNS, default 10
	DBLockTimeout   time.Duration // PMS_DB_LOCK_TIMEOUT, default 5s (SET LOCAL lock_timeout per transaction)
	LogLevel        slog.Level    // PMS_LOG_LEVEL: debug|info|warn|error, default info
	LogFormat       string        // PMS_LOG_FORMAT: json|text, default json in production, text otherwise
	ShutdownTimeout time.Duration // PMS_SHUTDOWN_TIMEOUT, default 15s
	MigrateOnStart  bool          // PMS_MIGRATE_ON_START, default false
	JWTSecret       []byte        // PMS_JWT_SECRET: HMAC key for access tokens, required, >= 32 bytes
	AccessTokenTTL  time.Duration // PMS_ACCESS_TOKEN_TTL, default 15m
	RefreshTokenTTL time.Duration // PMS_REFRESH_TOKEN_TTL, default 720h (30 days)
	CookieSecure    bool          // PMS_COOKIE_SECURE: refresh cookie over HTTPS only; default true except in development
}

// Load reads the configuration from the process environment.
func Load() (Config, error) { return LoadFrom(os.LookupEnv) }

// LoadFrom reads the configuration through lookup (testable). All problems are
// reported together.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	var errs []error
	get := func(key, def string) string {
		if v, ok := lookup(key); ok {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
		return def
	}

	cfg := Config{
		Env:         Env(get("PMS_ENV", string(EnvDevelopment))),
		HTTPAddr:    get("PMS_HTTP_ADDR", ":8080"),
		DatabaseURL: get("PMS_DATABASE_URL", ""),
	}

	switch cfg.Env {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("PMS_ENV: must be development, test or production, got %q", cfg.Env))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("PMS_DATABASE_URL: required"))
	}

	if n, err := strconv.ParseInt(get("PMS_DB_MAX_CONNS", "10"), 10, 32); err != nil || n < 1 {
		errs = append(errs, errors.New("PMS_DB_MAX_CONNS: must be a positive integer"))
	} else {
		cfg.DBMaxConns = int32(n)
	}

	cfg.DBLockTimeout = positiveDuration(get, "PMS_DB_LOCK_TIMEOUT", "5s", &errs)
	cfg.ShutdownTimeout = positiveDuration(get, "PMS_SHUTDOWN_TIMEOUT", "15s", &errs)

	if err := cfg.LogLevel.UnmarshalText([]byte(get("PMS_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("PMS_LOG_LEVEL: %w", err))
	}

	defaultFormat := "text"
	if cfg.Env == EnvProduction {
		defaultFormat = "json"
	}
	cfg.LogFormat = get("PMS_LOG_FORMAT", defaultFormat)
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("PMS_LOG_FORMAT: must be json or text, got %q", cfg.LogFormat))
	}

	migrate, err := strconv.ParseBool(get("PMS_MIGRATE_ON_START", "false"))
	if err != nil {
		errs = append(errs, errors.New("PMS_MIGRATE_ON_START: must be true or false"))
	}
	cfg.MigrateOnStart = migrate

	if secret := get("PMS_JWT_SECRET", ""); len(secret) < 32 {
		errs = append(errs, errors.New("PMS_JWT_SECRET: required, at least 32 characters (e.g. openssl rand -base64 48)"))
	} else {
		cfg.JWTSecret = []byte(secret)
	}
	cfg.AccessTokenTTL = positiveDuration(get, "PMS_ACCESS_TOKEN_TTL", "15m", &errs)
	cfg.RefreshTokenTTL = positiveDuration(get, "PMS_REFRESH_TOKEN_TTL", "720h", &errs)
	if cfg.AccessTokenTTL > time.Hour {
		errs = append(errs, errors.New("PMS_ACCESS_TOKEN_TTL: at most 1h (access tokens cannot be revoked individually)"))
	}

	defaultSecure := "true"
	if cfg.Env == EnvDevelopment {
		defaultSecure = "false"
	}
	secure, err := strconv.ParseBool(get("PMS_COOKIE_SECURE", defaultSecure))
	if err != nil {
		errs = append(errs, errors.New("PMS_COOKIE_SECURE: must be true or false"))
	}
	if !secure && cfg.Env == EnvProduction {
		errs = append(errs, errors.New("PMS_COOKIE_SECURE: must be true in production"))
	}
	cfg.CookieSecure = secure

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

func positiveDuration(get func(string, string) string, key, def string, errs *[]error) time.Duration {
	d, err := time.ParseDuration(get(key, def))
	if err != nil || d <= 0 {
		*errs = append(*errs, fmt.Errorf("%s: must be a positive duration such as 5s", key))
		return 0
	}
	return d
}

// RedactedDatabaseURL returns the database URL with its password removed, for logs.
func (c Config) RedactedDatabaseURL() string {
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Scheme == "" {
		return "(redacted)"
	}
	return u.Redacted()
}
