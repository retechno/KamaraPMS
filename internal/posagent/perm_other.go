//go:build !windows

package posagent

import "os"

// restrictDir makes the directory of the configuration private to its owner.
func restrictDir(dir string) error { return os.Chmod(dir, 0o700) } //nolint:gosec // G302: a directory needs the x bit; 0700 is the owner only
