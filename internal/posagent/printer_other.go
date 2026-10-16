//go:build !windows

package posagent

import "errors"

// writeSpooler is the Windows spooler: not here. A Linux or macOS agent writes to a device file ("DEV:/dev/usb/lp0") or to the network.
func writeSpooler(name string, _ []byte) error {
	return &PrintError{Code: CodeUnsupported, Err: errors.New("USB:" + name + " is for the Windows spooler; use DEV:/dev/usb/lp0 or an IP address on this system")}
}

func probeSpooler(string) string { return StatusOffline }
