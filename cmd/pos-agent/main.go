// Command pos-agent is the print agent of a hotel: one program that calls the server over HTTPS, takes the print jobs of the printers assigned
// to this machine and writes their bytes to the printers (docs/pos-agent.md). It has no database and nothing to open to the network.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kamarapms/internal/platform/clock"
	"kamarapms/internal/pos/printing"
	"kamarapms/internal/posagent"
)

// version is set at build time: -ldflags "-X main.version=0.1.0".
var version = "dev"

const usage = `pos-agent %s: the print agent of KamaraPMS

  pos-agent enroll -server URL -code XXXX-XXXX -name "Kasir Lobby" [-config FILE]
  pos-agent run [-config FILE]               work in the foreground
  pos-agent status [-config FILE]            the server, the printers and what they say
  pos-agent test-print <printer> [-config FILE]
  pos-agent install | uninstall | start | stop    (Windows service)
  pos-agent version
`

func main() {
	if runAsService() { // started by the Windows service manager
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "enroll":
		err = cmdEnroll(args)
	case "run":
		err = cmdRun(args)
	case "status":
		err = cmdStatus(args)
	case "test-print":
		err = cmdTestPrint(args)
	case "install", "uninstall", "start", "stop":
		err = serviceCommand(cmd, args)
	case "version", "-v", "--version":
		fmt.Println("pos-agent", version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pos-agent:", err)
		os.Exit(1)
	}
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", posagent.DefaultConfigPath(), "the configuration file")
}

func cmdEnroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	server := fs.String("server", "", "the address of the server, https://pms.example.com")
	code := fs.String("code", "", "the enrolment code of the setup screen")
	name := fs.String("name", "", "the name of this machine, for the list of devices")
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *code == "" || *name == "" {
		return errors.New("enroll needs -server, -code and -name")
	}
	if !strings.HasPrefix(*server, "https://") && !strings.HasPrefix(*server, "http://localhost") && !strings.HasPrefix(*server, "http://127.0.0.1") {
		return errors.New("the server must be an https:// address (http is for localhost only)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, srv, err := posagent.NewClient(*server, "").Enroll(ctx, *code, *name, version)
	if err != nil {
		return err
	}
	if err := posagent.SaveConfig(*cfgPath, posagent.FileConfig{Server: strings.TrimRight(*server, "/"), Token: token, Name: *name, DeviceID: srv.DeviceID}); err != nil {
		return fmt.Errorf("the device is enrolled but the configuration could not be written to %s: %w", *cfgPath, err)
	}
	fmt.Printf("enrolled as %q (device %d). The token is in %s and is not shown again.\nNext: assign printers to this device in Setup > Stations and printers, then `pos-agent install` and `pos-agent start`, or `pos-agent run`.\n",
		*name, srv.DeviceID, *cfgPath)
	return nil
}

// newLogger writes to the rotating log beside the configuration and, in the foreground, to the screen too.
func newLogger(cfgPath string, foreground bool) (*slog.Logger, func(), error) {
	f, err := posagent.NewRotatingFile(posagent.LogPath(cfgPath), 1<<20, 3)
	if err != nil {
		return nil, nil, err
	}
	var w io.Writer = f
	if foreground {
		w = io.MultiWriter(f, os.Stderr)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})), func() { _ = f.Close() }, nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runAgent(ctx, *cfgPath, true)
}

// runAgent is the loop, for the foreground and for the service.
func runAgent(ctx context.Context, cfgPath string, foreground bool) error {
	cfg, err := posagent.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	log, closeLog, err := newLogger(cfgPath, foreground)
	if err != nil {
		return err
	}
	defer closeLog()
	log.Info("pos-agent starting", "version", version, "server", cfg.Server, "device", cfg.Name)
	a := &posagent.Agent{Client: posagent.NewClient(cfg.Server, cfg.Token), Writer: posagent.NewWriter(), Log: log, Version: version}
	if err := a.Run(ctx); err != nil {
		if errors.Is(err, posagent.ErrRevoked) {
			return fmt.Errorf("%w (see %s)", err, posagent.LogPath(cfgPath))
		}
		return err
	}
	log.Info("pos-agent stopped")
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := posagent.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Printf("pos-agent %s\nserver:  %s\ndevice:  %s (id %d)\nconfig:  %s\nlog:     %s\n", version, cfg.Server, cfg.Name, cfg.DeviceID, *cfgPath, posagent.LogPath(*cfgPath))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	srv, err := posagent.NewClient(cfg.Server, cfg.Token).Config(ctx)
	if err != nil {
		if errors.Is(err, posagent.ErrRevoked) {
			return err
		}
		return fmt.Errorf("the server cannot be reached: %w", err)
	}
	fmt.Printf("server:  reachable, the token is accepted\nprinters: %d\n", len(srv.Printers))
	w := posagent.NewWriter()
	for _, p := range srv.Printers {
		fmt.Printf("  #%d %-24s %-4s %-28s %s\n", p.ID, p.Name, p.Kind, p.ConnectionInfo, w.Probe(ctx, p.ConnectionInfo))
	}
	return nil
}

func cmdTestPrint(args []string) error {
	fs := flag.NewFlagSet("test-print", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	// the printer may come before or after the flags
	var name string
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, rest = args[0], args[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	if name == "" {
		return errors.New("test-print needs the name or the number of a printer (see `pos-agent status`)")
	}
	cfg, err := posagent.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	srv, err := posagent.NewClient(cfg.Server, cfg.Token).Config(ctx)
	if err != nil {
		return err
	}
	var pr *posagent.Printer
	id, _ := strconv.ParseInt(strings.TrimPrefix(name, "#"), 10, 64)
	for i := range srv.Printers {
		if srv.Printers[i].ID == id || strings.EqualFold(srv.Printers[i].Name, name) {
			pr = &srv.Printers[i]
		}
	}
	if pr == nil {
		return fmt.Errorf("no printer %q is assigned to this device", name)
	}
	doc := printing.TestPage(pr.Name, cfg.Name, version, clock.System{}.Now())
	b, err := printing.Render(doc, printing.Profile{WidthMM: pr.PaperWidthMM, Codepage: "CP437"}, "")
	if err != nil {
		return err
	}
	if err := posagent.NewWriter().Write(ctx, pr.ConnectionInfo, b); err != nil {
		return fmt.Errorf("the test page was not printed: %w", err)
	}
	fmt.Printf("a test page was sent to %s (%s)\n", pr.Name, pr.ConnectionInfo)
	return nil
}
