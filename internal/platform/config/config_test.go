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

func TestRateLimitSetting(t *testing.T) {
	base := map[string]string{"PMS_DATABASE_URL": "postgres://pms:secret@localhost:5432/pms", "PMS_JWT_SECRET": strings.Repeat("s", 32)}
	with := func(v string) (Config, error) {
		m := map[string]string{}
		for k, x := range base {
			m[k] = x
		}
		if v != "" {
			m["PMS_RATE_LIMIT_PER_MINUTE"] = v
		}
		return LoadFrom(env(m))
	}
	if cfg, err := with(""); err != nil || cfg.RateLimit != 600 {
		t.Fatalf("default: %v %d", err, cfg.RateLimit)
	}
	if cfg, err := with("120"); err != nil || cfg.RateLimit != 120 {
		t.Fatalf("120: %v %d", err, cfg.RateLimit)
	}
	if cfg, err := with("0"); err != nil || cfg.RateLimit != 0 {
		t.Fatalf("0 disables: %v %d", err, cfg.RateLimit)
	}
	for _, bad := range []string{"-1", "many", "1.5"} {
		if _, err := with(bad); err == nil || !strings.Contains(err.Error(), "PMS_RATE_LIMIT_PER_MINUTE") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestSMTPSettings(t *testing.T) {
	base := map[string]string{"PMS_DATABASE_URL": "postgres://pms:secret@localhost:5432/pms", "PMS_JWT_SECRET": strings.Repeat("s", 32)}
	with := func(extra map[string]string) (Config, error) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return LoadFrom(env(m))
	}
	if cfg, err := with(nil); err != nil || cfg.SMTPHost != "" || cfg.SMTPPort != 587 || cfg.SMTPTLS != "starttls" {
		t.Fatalf("off by default: %v %+v", err, cfg)
	}
	cfg, err := with(map[string]string{"PMS_SMTP_HOST": "smtp.example.com", "PMS_SMTP_FROM": "reservations@hotel.example", "PMS_SMTP_USERNAME": "u", "PMS_SMTP_PASSWORD": "p", "PMS_SMTP_TLS": "TLS", "PMS_SMTP_PORT": "465"})
	if err != nil || cfg.SMTPHost != "smtp.example.com" || cfg.SMTPTLS != "tls" || cfg.SMTPPort != 465 || cfg.SMTPPassword != "p" {
		t.Fatalf("configured: %v %+v", err, cfg)
	}
	for name, extra := range map[string]map[string]string{
		"no sender":          {"PMS_SMTP_HOST": "h"},
		"sender with a name": {"PMS_SMTP_HOST": "h", "PMS_SMTP_FROM": "Hotel <a@b.test>"},
		"bad tls":            {"PMS_SMTP_HOST": "h", "PMS_SMTP_FROM": "a@b.test", "PMS_SMTP_TLS": "ssl"},
		"bad port":           {"PMS_SMTP_HOST": "h", "PMS_SMTP_FROM": "a@b.test", "PMS_SMTP_PORT": "99999"},
		"plain text in prod": {"PMS_ENV": "production", "PMS_COOKIE_SECURE": "true", "PMS_SMTP_HOST": "h", "PMS_SMTP_FROM": "a@b.test", "PMS_SMTP_TLS": "none", "PMS_SMTP_USERNAME": "u"},
	} {
		if _, err := with(extra); err == nil || !strings.Contains(err.Error(), "PMS_SMTP") {
			t.Errorf("%s must be refused: %v", name, err)
		}
	}
	if _, err := with(map[string]string{"PMS_ENV": "production", "PMS_COOKIE_SECURE": "true", "PMS_SMTP_HOST": "localhost", "PMS_SMTP_FROM": "a@b.test", "PMS_SMTP_TLS": "none"}); err != nil {
		t.Errorf("an unauthenticated relay without TLS is allowed: %v", err)
	}
}

func TestTrustedProxies(t *testing.T) {
	base := map[string]string{"PMS_DATABASE_URL": "postgres://x@y/z", "PMS_JWT_SECRET": strings.Repeat("s", 32)}
	with := func(list string) (Config, error) {
		m := map[string]string{"PMS_TRUSTED_PROXIES": list}
		for k, v := range base {
			m[k] = v
		}
		return LoadFrom(env(m))
	}
	// the default trusts nobody
	cfg, err := LoadFrom(env(base))
	if err != nil || len(cfg.TrustedProxies) != 0 {
		t.Fatalf("default: %v %v", err, cfg.TrustedProxies)
	}
	// addresses and ranges, spaces and empty items allowed, a single address is a range of one, IPv4-mapped IPv6 is read as IPv4
	cfg, err = with(" 10.0.0.5, 172.29.0.0/24 ,, ::1, ::ffff:192.168.1.7, fd00::/8 ")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range cfg.TrustedProxies {
		got = append(got, p.String())
	}
	want := []string{"10.0.0.5/32", "172.29.0.0/24", "::1/128", "192.168.1.7/32", "fd00::/8"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	// a range is masked to its network
	if cfg, err = with("172.29.0.77/24"); err != nil || cfg.TrustedProxies[0].String() != "172.29.0.0/24" {
		t.Fatalf("masked: %v %v", err, cfg.TrustedProxies)
	}
	// refused: not an address, a bad range, and a range that trusts the internet
	for _, bad := range []string{"proxy.example.com", "10.0.0.0/33", "300.1.1.1", "0.0.0.0/0", "::/0", "::ffff:0:0/80"} {
		if _, err := with(bad); err == nil || !strings.Contains(err.Error(), "PMS_TRUSTED_PROXIES") {
			t.Fatalf("%q must be refused: %v", bad, err)
		}
	}
}

func TestLoadDatabaseURLNeedsNothingElse(t *testing.T) {
	if u, err := LoadDatabaseURL(env(map[string]string{"PMS_DATABASE_URL": " postgres://x@y/z "})); err != nil || u != "postgres://x@y/z" {
		t.Fatalf("%q %v", u, err)
	}
	if _, err := LoadDatabaseURL(env(nil)); err == nil || !strings.Contains(err.Error(), "PMS_DATABASE_URL") {
		t.Fatalf("missing URL: %v", err)
	}
}
