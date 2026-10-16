//go:build windows

package posagent

import (
	"os/exec"
	"os/user"
)

// restrictDir removes the inherited permissions of the configuration directory and leaves it to SYSTEM, the administrators and the account that
// enrolled the agent (icacls is part of Windows). A failure is not fatal: the file is still written; the guide asks the operator to check the folder.
func restrictDir(dir string) error {
	args := []string{dir, "/inheritance:r", "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F"}
	if u, err := user.Current(); err == nil && u.Uid != "" {
		args = append(args, "*"+u.Uid+":(OI)(CI)F")
	}
	_ = exec.Command("icacls", args...).Run() //nolint:gosec // G204: fixed arguments, the directory is the operator's
	return nil
}
