package posagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"kamarapms/internal/platform/clock"
)

// sysClock is the clock of the machine the agent runs on: socket deadlines and the log, never a date of the hotel.
var sysClock clock.System

// Printer statuses the agent reports.
const (
	StatusOnline    = "ONLINE"
	StatusOffline   = "OFFLINE"
	StatusPaperOut  = "PAPER_OUT"
	StatusCoverOpen = "COVER_OPEN"
)

// Error codes of an attempt that failed. They are what the cashier and the setup screen show.
const (
	CodeConnect     = "CONNECT_FAILED"
	CodeWrite       = "WRITE_FAILED"
	CodeUnsupported = "UNSUPPORTED_CONNECTION"
	CodeBadPayload  = "BAD_PAYLOAD"
)

// Target is where the bytes of a printer go, from its connection_info.
type Target struct {
	Kind string // TCP, USB or DEV
	Addr string
}

// ParseTarget reads "192.168.1.50:9100" (TCP raw; the port defaults to 9100), "USB:EPSON TM-T82" (a Windows printer, RAW through the spooler) or
// "DEV:/dev/usb/lp0" (a device file, Linux).
func ParseTarget(info string) (Target, error) {
	info = strings.TrimSpace(info)
	switch {
	case info == "":
		return Target{}, errors.New("the printer has no connection_info")
	case strings.HasPrefix(strings.ToUpper(info), "USB:"):
		name := strings.TrimSpace(info[4:])
		if name == "" {
			return Target{}, errors.New("USB: needs the name of the Windows printer")
		}
		return Target{Kind: "USB", Addr: name}, nil
	case strings.HasPrefix(strings.ToUpper(info), "DEV:"):
		path := strings.TrimSpace(info[4:])
		if path == "" {
			return Target{}, errors.New("DEV: needs a device path")
		}
		return Target{Kind: "DEV", Addr: path}, nil
	}
	host, port, err := net.SplitHostPort(info)
	if err != nil {
		if strings.Contains(info, ":") {
			return Target{}, fmt.Errorf("connection_info %q is not host:port", info)
		}
		host, port = info, "9100"
	}
	if host == "" {
		return Target{}, fmt.Errorf("connection_info %q has no host", info)
	}
	return Target{Kind: "TCP", Addr: net.JoinHostPort(host, port)}, nil
}

// Writer sends bytes to printers. DialTimeout and WriteTimeout bound a stuck printer; they are fields so a test can shorten them.
type Writer struct {
	DialTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewWriter returns a Writer with the timeouts of a hotel LAN.
func NewWriter() *Writer {
	return &Writer{DialTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second}
}

// Write sends data to the printer as it is. The error carries a code (see Code*).
func (w *Writer) Write(ctx context.Context, info string, data []byte) error {
	t, err := ParseTarget(info)
	if err != nil {
		return &PrintError{Code: CodeUnsupported, Err: err}
	}
	switch t.Kind {
	case "TCP":
		d := net.Dialer{Timeout: w.DialTimeout}
		c, err := d.DialContext(ctx, "tcp", t.Addr)
		if err != nil {
			return &PrintError{Code: CodeConnect, Err: err}
		}
		defer c.Close()
		_ = c.SetWriteDeadline(sysClock.Now().Add(w.WriteTimeout))
		if _, err := c.Write(data); err != nil {
			return &PrintError{Code: CodeWrite, Err: err}
		}
		// let the printer take what it was sent before the socket closes
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		return nil
	case "DEV":
		f, err := os.OpenFile(t.Addr, os.O_WRONLY, 0) //nolint:gosec // G304: the path is the one the hotel set up for its printer
		if err != nil {
			return &PrintError{Code: CodeConnect, Err: err}
		}
		defer f.Close()
		if _, err := f.Write(data); err != nil {
			return &PrintError{Code: CodeWrite, Err: err}
		}
		return nil
	case "USB":
		return writeSpooler(t.Addr, data)
	}
	return &PrintError{Code: CodeUnsupported, Err: fmt.Errorf("unknown connection %q", t.Kind)}
}

// Probe says what the printer is now, without printing: ONLINE when it answers, PAPER_OUT or COVER_OPEN when the printer says so (the status
// bytes of ESC/POS, asked for with DLE EOT; a printer that does not answer is taken as ONLINE), OFFLINE when it cannot be reached.
func (w *Writer) Probe(ctx context.Context, info string) string {
	t, err := ParseTarget(info)
	if err != nil {
		return StatusOffline
	}
	switch t.Kind {
	case "TCP":
		d := net.Dialer{Timeout: w.DialTimeout}
		c, err := d.DialContext(ctx, "tcp", t.Addr)
		if err != nil {
			return StatusOffline
		}
		defer c.Close()
		return probeTCP(c)
	case "DEV":
		f, err := os.OpenFile(t.Addr, os.O_WRONLY, 0) //nolint:gosec // G304: the path is the one the hotel set up for its printer
		if err != nil {
			return StatusOffline
		}
		_ = f.Close()
		return StatusOnline
	case "USB":
		return probeSpooler(t.Addr)
	}
	return StatusOffline
}

// probeTCP asks the printer for the status of its paper (DLE EOT 4) and of its cover (DLE EOT 2). A printer that is silent is online.
func probeTCP(c net.Conn) string {
	ask := func(n byte) (byte, bool) {
		_ = c.SetDeadline(sysClock.Now().Add(400 * time.Millisecond))
		if _, err := c.Write([]byte{0x10, 0x04, n}); err != nil {
			return 0, false
		}
		var b [1]byte
		if _, err := c.Read(b[:]); err != nil {
			return 0, false
		}
		return b[0], true
	}
	if b, ok := ask(4); ok && b&0x60 == 0x60 { // paper roll sensor: no paper
		return StatusPaperOut
	}
	if b, ok := ask(2); ok && b&0x04 != 0 { // offline status: cover is open
		return StatusCoverOpen
	}
	return StatusOnline
}

// PrintError carries the code the server shows with a failed job.
type PrintError struct {
	Code string
	Err  error
}

func (e *PrintError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *PrintError) Unwrap() error { return e.Err }

// CodeOf is the error code of an error of Write.
func CodeOf(err error) string {
	if pe, ok := errors.AsType[*PrintError](err); ok {
		return pe.Code
	}
	return CodeWrite
}
