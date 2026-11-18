//go:build windows

package main

import (
	"context"
	"time"

	"golang.org/x/sys/windows/svc"

	"github.com/veilon/veilon-app/veilond/internal/localapi"
	"github.com/veilon/veilon-app/veilond/internal/trustpath"
)

const (
	defaultConfig    = `C:\ProgramData\VeilOn\veilond.yaml`
	platformCommands = " | service | setup-group"
	usersGroup       = "VeilOn Users"
)

func platformCommand(cmd, cfgPath string) (bool, error) {
	switch cmd {
	case "service":
		return true, svc.Run("VeilOnUpdater", &service{cfg: cfgPath})
	case "setup-group":
		return true, setupGroup()
	}
	return false, nil
}

// setupGroup creates the local group whose members may control the VPN
// (ADR-0060); the services also do it at start. An existing group is kept.
func setupGroup() error {
	return localapi.EnsureLocalGroup(usersGroup, "Members may control the VeilOn VPN")
}

// service polls the inbox; Windows has no path-triggered services.
type service struct{ cfg string }

func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	_ = setupGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				return false, 0
			}
		case <-tick.C:
			_ = applyOnce(ctx, s.cfg)
		}
	}
}

// prepareInbox: veilond and the helper are both LocalSystem services on
// Windows; the inbox gets a protected DACL (SYSTEM and Administrators).
func prepareInbox(p string) error {
	return trustpath.EnsureDir(p, trustpath.DirOptions{})
}
