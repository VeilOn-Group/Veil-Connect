package killswitch

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// PF implements the macOS kill switch. Rules live in the anchor
// "com.apple/250.VeilOnKillSwitch": the stock /etc/pf.conf evaluates
// `anchor "com.apple/*"`, so no system file is edited. pf is enabled with a
// reference token (pfctl -E) and released with that token (pfctl -X), so other
// pf users are not disturbed.
type PF struct {
	Bin    string // default "/sbin/pfctl"
	Anchor string // default "com.apple/250.VeilOnKillSwitch"
	token  string
}

func (p *PF) bin() string {
	if p.Bin == "" {
		return "/sbin/pfctl"
	}
	return p.Bin
}

func (p *PF) anchor() string {
	if p.Anchor == "" {
		return "com.apple/250.VeilOnKillSwitch"
	}
	return p.Anchor
}

// Ruleset renders the anchor rules. Traffic routed into the tunnel leaves
// with the TUN source address, so it is matched by source instead of by the
// (dynamic) utunN interface name.
func (p *PF) Ruleset(s Spec) (string, error) {
	if !s.TunAddr4.IsValid() || !s.TunAddr4.Is4() {
		return "", fmt.Errorf("killswitch: TUN IPv4 address required")
	}
	v4, v6 := split(append(append([]netip.Addr(nil), s.Servers...), s.APIs...))
	var b strings.Builder
	b.WriteString("pass out quick on lo0 all\n")
	fmt.Fprintf(&b, "pass out quick inet from %s to any\n", s.TunAddr4)
	if len(v4) > 0 {
		fmt.Fprintf(&b, "pass out quick inet to { %s }\n", strings.Join(v4, " "))
	}
	if s.HostIPv6 && len(v6) > 0 {
		fmt.Fprintf(&b, "pass out quick inet6 to { %s }\n", strings.Join(v6, " "))
	}
	b.WriteString("pass out quick inet proto udp from any port 68 to any port 67\n")
	if s.AllowLAN {
		fmt.Fprintf(&b, "pass out quick inet to { %s }\n", strings.Join(LAN4, " "))
		if s.HostIPv6 {
			fmt.Fprintf(&b, "pass out quick inet6 to { %s }\n", strings.Join(LAN6, " "))
		}
	}
	b.WriteString("block drop out quick all\n")
	return b.String(), nil
}

func (p *PF) run(stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.bin(), args...) //nolint:gosec // fixed binary, generated rules
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("killswitch: pfctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

var reToken = regexp.MustCompile(`Token : (\d+)`)

// Engage loads the anchor and takes a pf enable reference.
func (p *PF) Engage(s Spec) error {
	rs, err := p.Ruleset(s)
	if err != nil {
		return err
	}
	if _, err := p.run(rs, "-a", p.anchor(), "-f", "-"); err != nil {
		return err
	}
	if p.token == "" {
		out, err := p.run("", "-E")
		if err != nil {
			return err
		}
		if m := reToken.FindStringSubmatch(out); m != nil {
			p.token = m[1]
		}
	}
	return nil
}

// Release flushes the anchor and drops the enable reference.
func (p *PF) Release() error {
	if _, err := p.run("", "-a", p.anchor(), "-F", "all"); err != nil {
		return err
	}
	if p.token != "" {
		if _, err := p.run("", "-X", p.token); err != nil {
			return err
		}
		p.token = ""
	}
	return nil
}

// Active reports whether the anchor has rules.
func (p *PF) Active() (bool, error) {
	out, err := p.run("", "-a", p.anchor(), "-s", "rules")
	return err == nil && strings.TrimSpace(out) != "", nil
}
