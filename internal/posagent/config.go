package posagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// FileConfig is what an enrolled agent keeps on disk: where the server is and the token of the device. The file is readable by the account that
// runs the agent only; the token is never printed to a log or to the screen.
type FileConfig struct {
	Server   string `json:"server"`
	Token    string `json:"token"`
	Name     string `json:"name"`
	DeviceID int64  `json:"device_id"`
}

// DefaultConfigPath is where the configuration lives: %ProgramData%\KamaraPMS\pos-agent on Windows, /etc/kamarapms/pos-agent on Linux.
func DefaultConfigPath() string {
	if v := os.Getenv("POS_AGENT_CONFIG"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "KamaraPMS", "pos-agent", "config.json")
	}
	return "/etc/kamarapms/pos-agent/config.json"
}

// LogPath is the log beside the configuration.
func LogPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "agent.log")
}

// LoadConfig reads the configuration of an enrolled agent.
func LoadConfig(path string) (FileConfig, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path of the configuration is the operator's
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileConfig{}, fmt.Errorf("no configuration at %s: run `pos-agent enroll` first", path)
		}
		return FileConfig{}, err
	}
	var c FileConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return FileConfig{}, fmt.Errorf("the configuration %s is not valid: %w", path, err)
	}
	if c.Server == "" || c.Token == "" {
		return FileConfig{}, fmt.Errorf("the configuration %s has no server or token: enrol the agent again", path)
	}
	return c, nil
}

// SaveConfig writes the configuration with permissions that keep the token to the account of the agent (0600 in a 0700 directory; on Windows
// the directory is then restricted to SYSTEM and the administrators).
func SaveConfig(path string, c FileConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return restrictDir(dir)
}
