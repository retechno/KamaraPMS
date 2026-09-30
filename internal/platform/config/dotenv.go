package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// LoadDotEnv reads KEY=VALUE lines from path into the process environment, for
// local development. A missing file is not an error. Variables that are already
// set in the environment win, so real deployment configuration is never
// overridden by a stray .env file.
//
// Supported syntax: blank lines, "# comments", an optional "export " prefix, and
// values optionally wrapped in single or double quotes.
func LoadDotEnv(path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: the path is chosen by the program (".env"), not by input
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "Feff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("config: %s line %d: expected KEY=VALUE", path, n)
		}
		value = unquote(strings.TrimSpace(value))
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("config: %s line %d: %w", path, n, err)
		}
	}
	return scanner.Err()
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}
