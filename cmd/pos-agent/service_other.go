//go:build !windows

package main

import "errors"

// runAsService is false off Windows: a Linux agent is run by systemd as an ordinary program (deploy/pos-agent.service).
func runAsService() bool { return false }

func serviceCommand(cmd string, _ []string) error {
	return errors.New(cmd + " is for the Windows service; on Linux use the systemd unit in deploy/pos-agent.service")
}
