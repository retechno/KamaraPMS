//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"kamarapms/internal/posagent"
)

const serviceName = "KamaraPMSPrintAgent"

type agentService struct{ cfgPath string }

// Execute is the service: it runs the agent until the service manager asks it to stop. A token that is not accepted ends the service with an error
// that stays in the log and in the event log.
func (s agentService) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runAgent(ctx, s.cfgPath, false) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			}
		}
	}
}

// runAsService reports whether the program was started by the service manager, and runs the service if so.
func runAsService() bool {
	ok, err := svc.IsWindowsService()
	if err != nil || !ok {
		return false
	}
	cfg := posagent.DefaultConfigPath()
	if len(os.Args) > 2 && os.Args[1] == "service" {
		cfg = os.Args[2]
	}
	if err := svc.Run(serviceName, agentService{cfgPath: cfg}); err != nil {
		os.Exit(1)
	}
	return true
}

// serviceCommand installs, removes, starts and stops the Windows service.
func serviceCommand(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("the service manager is not reachable (run as administrator): %w", err)
	}
	defer m.Disconnect()
	switch cmd {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, _ = filepath.Abs(exe)
		if s, err := m.OpenService(serviceName); err == nil {
			s.Close()
			return errors.New("the service is already installed")
		}
		s, err := m.CreateService(serviceName, exe, mgr.Config{DisplayName: "KamaraPMS print agent", Description: "Prints the receipts and the kitchen tickets of KamaraPMS",
			StartType: mgr.StartAutomatic}, "service", *cfgPath)
		if err != nil {
			return err
		}
		defer s.Close()
		_ = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 30 * time.Second}, {Type: mgr.ServiceRestart, Delay: time.Minute}}, 86400)
		fmt.Println("the service", serviceName, "is installed. Start it with: pos-agent start")
		return nil
	}
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("the service %s is not installed: %w", serviceName, err)
	}
	defer s.Close()
	switch cmd {
	case "uninstall":
		if err := s.Delete(); err != nil {
			return err
		}
		fmt.Println("the service is removed")
	case "start":
		if err := s.Start(); err != nil {
			return err
		}
		fmt.Println("the service is started")
	case "stop":
		if _, err := s.Control(svc.Stop); err != nil {
			return err
		}
		fmt.Println("the service is stopping")
	}
	return nil
}
