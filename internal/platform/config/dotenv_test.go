package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "Feff# comment\n\nPMS_T_PLAIN=postgres://pms:pms@localhost:55432/pms?sslmode=disable\r\n" +
		"export PMS_T_EXPORT=yes\n" +
		"PMS_T_DQ=\"two words\"\n" +
		"PMS_T_SQ='single'\n" +
		"PMS_T_KEEP=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PMS_T_KEEP", "from-environment")
	for _, k := range []string{"PMS_T_PLAIN", "PMS_T_EXPORT", "PMS_T_DQ", "PMS_T_SQ"} {
		t.Setenv(k, "") // registers cleanup; then unset so the file can provide it
		_ = os.Unsetenv(k)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PMS_T_PLAIN":  "postgres://pms:pms@localhost:55432/pms?sslmode=disable", // '=' inside the value and CRLF handled
		"PMS_T_EXPORT": "yes",
		"PMS_T_DQ":     "two words",
		"PMS_T_SQ":     "single",
		"PMS_T_KEEP":   "from-environment", // the real environment wins
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestLoadDotEnvMissingFileIsFine(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDotEnvRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("THIS IS NOT AN ASSIGNMENT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(path); err == nil {
		t.Fatal("expected a line error")
	}
}
