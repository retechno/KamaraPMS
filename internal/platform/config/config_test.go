package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{"PMS_DATABASE_URL": "postgres://pms:secret@localhost:5432/pms", "PMS_JWT_SECRET": strings.Repeat("s", 32)}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != EnvDevelopment || cfg.HTTPAddr != ":8080" || cfg.DBMaxConns != 10 ||
		cfg.DBLockTimeout != 5*time.Second || cfg.ShutdownTimeout != 15*time.Second ||
		cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != "text" || cfg.MigrateOnStart {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestProductionDefaultsToJSONLogs(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{"PMS_ENV": "production", "PMS_DATABASE_URL": "postgres://x@y/z", "PMS_JWT_SECRET": strings.Repeat("s", 32)}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("got %q", cfg.LogFormat)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := LoadFrom(env(map[string]string{
		"PMS_ENV":              "test",
		"PMS_HTTP_ADDR":        "127.0.0.1:9000",
		"PMS_DATABASE_URL":     "postgres://x@y/z",
		"PMS_DB_MAX_CONNS":     "25",
		"PMS_DB_LOCK_TIMEOUT":  "750ms",
		"PMS_LOG_LEVEL":        "debug",
		"PMS_LOG_FORMAT":       "json",
		"PMS_SHUTDOWN_TIMEOUT": "3s",
		"PMS_MIGRATE_ON_START": "true",
		"PMS_JWT_SECRET":       strings.Repeat("s", 40),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != EnvTest || cfg.HTTPAddr != "127.0.0.1:9000" || cfg.DBMaxConns != 25 ||
		cfg.DBLockTimeout != 750*time.Millisecond || cfg.LogLevel != slog.LevelDebug ||
		cfg.LogFormat != "json" || cfg.ShutdownTimeout != 3*time.Second || !cfg.MigrateOnStart {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadReportsAllProblems(t *testing.T) {
	_, err := LoadFrom(env(map[string]string{
		"PMS_ENV":              "staging",
		"PMS_DB_MAX_CONNS":     "0",
		"PMS_DB_LOCK_TIMEOUT":  "-1s",
		"PMS_LOG_LEVEL":        "loud",
		"PMS_LOG_FORMAT":       "xml",
		"PMS_MIGRATE_ON_START": "maybe",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"PMS_ENV", "PMS_DATABASE_URL", "PMS_DB_MAX_CONNS", "PMS_DB_LOCK_TIMEOUT",
		"PMS_LOG_LEVEL", "PMS_LOG_FORMAT", "PMS_MIGRATE_ON_START"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}

func TestRedactedDatabaseURL(t *testing.T) {
	cfg := Config{DatabaseURL: "postgres://pms:secret@db:5432/pms?sslmode=disable"}
	got := cfg.RedactedDatabaseURL()
	if strings.Contains(got, "secret") || !strings.Contains(got, "db:5432") {
		t.Fatalf("got %q", got)
	}
	if (Config{DatabaseURL: "host=db password=secret"}).RedactedDatabaseURL() != "(redacted)" {
		t.Fatal("keyword DSNs must be fully redacted")
	}
}

func TestAuthSettings(t *testing.T) {
	base := map[string]string{"PMS_DATABASE_URL": "postgres://x@y/z", "PMS_JWT_SECRET": strings.Repeat("s", 32)}
	cfg, err := LoadFrom(env(base))
	if err != nil || len(cfg.JWTSecret) != 32 || cfg.AccessTokenTTL != 15*time.Minute ||
		cfg.RefreshTokenTTL != 720*time.Hour || cfg.CookieSecure {
		t.Fatalf("development defaults: %v %+v", err, cfg)
	}

	prod := map[string]string{"PMS_ENV": "production"}
	for k, v := range base {
		prod[k] = v
	}
	if cfg, err := LoadFrom(env(prod)); err != nil || !cfg.CookieSecure {
		t.Fatalf("production must default to secure cookies: %v", err)
	}
	prod["PMS_COOKIE_SECURE"] = "false"
	if _, err := LoadFrom(env(prod)); err == nil || !strings.Contains(err.Error(), "PMS_COOKIE_SECURE") {
		t.Fatalf("insecure cookies in production must be refused: %v", err)
	}

	short := map[string]string{"PMS_DATABASE_URL": "postgres://x@y/z", "PMS_JWT_SECRET": "too-short", "PMS_ACCESS_TOKEN_TTL": "24h"}
	_, err = LoadFrom(env(short))
	if err == nil || !strings.Contains(err.Error(), "PMS_JWT_SECRET") || !strings.Contains(err.Error(), "PMS_ACCESS_TOKEN_TTL") {
		t.Fatalf("weak secret / long access TTL must be refused: %v", err)
	}
}
