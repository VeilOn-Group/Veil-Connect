package killswitch

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WindowsFirewall implements the Windows kill switch with Windows Defender
// Firewall: the outbound default policy is switched to "block" for all
// profiles and a small set of allow rules is added (group "VeilOn Kill
// Switch"). The previous policy is saved to StateFile and restored on
// Release, also after a crash (Engage/Release are idempotent).
//
// Rationale (ADR-0053): sing-box strict_route already installs WFP filters,
// but in a dynamic WFP session that vanishes when the core exits; firewall
// policy persists until explicitly restored. Traffic routed into the tunnel
// is allowed by its source address (the TUN address).
type WindowsFirewall struct {
	Netsh     string // default "netsh"
	StateFile string // where the previous policy is saved (0600)
}

const fwGroup = "VeilOn Kill Switch"

// Commands renders the netsh invocations (exported for tests).
func (w *WindowsFirewall) Commands(s Spec) ([][]string, error) {
	if !s.TunAddr4.IsValid() || s.CorePath == "" || s.DaemonPath == "" {
		return nil, fmt.Errorf("killswitch: TUN address, core and daemon paths required")
	}
	v4, v6 := split(append(append([]netip.Addr(nil), s.Servers...), s.APIs...))
	rule := func(name string, extra ...string) []string {
		return append([]string{"advfirewall", "firewall", "add", "rule", "name=" + fwGroup + ": " + name,
			"group=" + fwGroup, "dir=out", "action=allow", "enable=yes", "profile=any"}, extra...)
	}
	cmds := [][]string{
		{"advfirewall", "firewall", "delete", "rule", "name=all", "group=" + fwGroup},
		rule("tunnel", "localip="+s.TunAddr4.String()),
		rule("core", "program="+filepath.Clean(s.CorePath)),
		rule("daemon", "program="+filepath.Clean(s.DaemonPath)),
		rule("dhcp", "protocol=udp", "localport=68", "remoteport=67"),
	}
	if remote := strings.Join(append(v4, func() []string {
		if s.HostIPv6 {
			return v6
		}
		return nil
	}()...), ","); remote != "" {
		cmds = append(cmds, rule("servers", "remoteip="+remote))
	}
	if s.AllowLAN {
		cmds = append(cmds, rule("lan", "remoteip=LocalSubnet"))
	}
	cmds = append(cmds, []string{"advfirewall", "set", "allprofiles", "firewallpolicy", "blockinbound,blockoutbound"})
	return cmds, nil
}

func (w *WindowsFirewall) netsh(args ...string) (string, error) {
	bin := w.Netsh
	if bin == "" {
		bin = "netsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binary, generated arguments
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("killswitch: netsh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// previousPolicy reads "Firewall Policy  BlockInbound,AllowOutbound" for the
// current profile; it is saved once so repeated Engage calls do not
// overwrite the user's original setting with our own.
func (w *WindowsFirewall) savePolicy() error {
	if _, err := os.Stat(w.StateFile); err == nil {
		return nil
	}
	out, err := w.netsh("advfirewall", "show", "currentprofile", "firewallpolicy")
	if err != nil {
		return err
	}
	policy := "blockinbound,allowoutbound"
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 3 && strings.EqualFold(f[0]+f[1], "FirewallPolicy") {
			policy = strings.ToLower(f[2])
		}
	}
	return os.WriteFile(w.StateFile, []byte(policy), 0o600)
}

// Engage installs rules and switches the outbound policy to block.
func (w *WindowsFirewall) Engage(s Spec) error {
	cmds, err := w.Commands(s)
	if err != nil {
		return err
	}
	if err := w.savePolicy(); err != nil {
		return err
	}
	for i, c := range cmds {
		if _, err := w.netsh(c...); err != nil && i != 0 { // deleting a missing group is fine
			return err
		}
	}
	return nil
}

// Release restores the saved policy and removes the rules.
func (w *WindowsFirewall) Release() error {
	policy := "blockinbound,allowoutbound"
	if b, err := os.ReadFile(w.StateFile); err == nil && len(b) > 0 {
		policy = strings.TrimSpace(string(b))
	}
	if _, err := w.netsh("advfirewall", "set", "allprofiles", "firewallpolicy", policy); err != nil {
		return err
	}
	_, _ = w.netsh("advfirewall", "firewall", "delete", "rule", "name=all", "group="+fwGroup)
	_ = os.Remove(w.StateFile)
	return nil
}

// Active reports whether the saved-policy marker exists.
func (w *WindowsFirewall) Active() (bool, error) {
	_, err := os.Stat(w.StateFile)
	return err == nil, nil
}
