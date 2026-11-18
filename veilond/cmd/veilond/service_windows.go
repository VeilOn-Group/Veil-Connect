//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/veilon/veilon-app/veilond/internal/localapi"
)

// ServiceName is the Windows service veilond runs as (LocalSystem).
const ServiceName = "VeilOnService"

// UsersGroup may control the VPN through the local API.
const UsersGroup = "VeilOn Users"

// platformMain handles Windows-only commands and the service control
// manager. It returns handled=true when it fully handled the invocation.
func platformMain(cfgPath string, args []string) (bool, error) {
	if len(args) > 0 {
		switch args[0] {
		case "install-service":
			return true, installService(cfgPath)
		case "uninstall-service":
			return true, uninstallService()
		}
	}
	isSvc, err := svc.IsWindowsService()
	if err != nil || !isSvc {
		return false, err
	}
	return true, svc.Run(ServiceName, &handler{cfgPath: cfgPath})
}

type handler struct{ cfgPath string }

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	// The installer-free way to provide the UI access group.
	if err := localapi.EnsureLocalGroup(UsersGroup, "Members may control the VeilOn VPN"); err != nil {
		fmt.Fprintln(os.Stderr, "veilond:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runCtx(ctx, h.cfgPath) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, "veilond:", err)
				return false, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func installService(cfgPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	if s, err := m.OpenService(ServiceName); err == nil {
		_ = s.Close()
		return fmt.Errorf("service %s already exists", ServiceName)
	}
	s, err := m.CreateService(ServiceName, exe, mgr.Config{
		DisplayName: "VeilOn VPN",
		Description: "VeilOn VPN core service (zero-log)",
		StartType:   mgr.StartAutomatic,
	}, "-config", cfgPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	// Restart on failure: 5 s, 30 s, then every 60 s.
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400)
	return s.Start()
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	_, _ = s.Control(svc.Stop)
	return s.Delete()
}
