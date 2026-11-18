package killswitch

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

// NFTables implements the Linux kill switch with one dedicated table.
// It never touches other tables, so it coexists with firewalld/ufw rules.
type NFTables struct {
	Bin   string // default "nft"
	Table string // default "veilon_ks"
}

func (n *NFTables) bin() string {
	if n.Bin == "" {
		return "nft"
	}
	return n.Bin
}

func (n *NFTables) table() string {
	if n.Table == "" {
		return "veilon_ks"
	}
	return n.Table
}

// Ruleset renders the nftables script (exported for golden tests).
func (n *NFTables) Ruleset(s Spec) (string, error) {
	if s.TunName == "" || strings.ContainsAny(s.TunName, " \"';{}") {
		return "", fmt.Errorf("killswitch: invalid tun name")
	}
	v4, v6 := split(append(append([]netip.Addr(nil), s.Servers...), s.APIs...))
	var b strings.Builder
	t := n.table()
	// "add" + "delete" makes the script idempotent; the table is recreated atomically.
	fmt.Fprintf(&b, "add table inet %s\ndelete table inet %s\ntable inet %s {\n", t, t, t)
	b.WriteString("  chain output {\n    type filter hook output priority filter - 10; policy drop;\n")
	b.WriteString("    oifname \"lo\" accept\n")
	fmt.Fprintf(&b, "    oifname %q accept\n", s.TunName)
	b.WriteString("    ct state established,related accept\n")
	if s.DirectMark != 0 {
		fmt.Fprintf(&b, "    meta mark 0x%x accept\n", s.DirectMark)
	}
	if len(v4) > 0 {
		fmt.Fprintf(&b, "    ip daddr { %s } accept\n", strings.Join(v4, ", "))
	}
	if s.HostIPv6 && len(v6) > 0 {
		fmt.Fprintf(&b, "    ip6 daddr { %s } accept\n", strings.Join(v6, ", "))
	}
	b.WriteString("    udp sport 68 udp dport 67 accept\n") // DHCPv4 renewals
	if s.AllowLAN {
		fmt.Fprintf(&b, "    ip daddr { %s } accept\n", strings.Join(LAN4, ", "))
		if s.HostIPv6 {
			fmt.Fprintf(&b, "    ip6 daddr { %s } accept\n", strings.Join(LAN6, ", "))
		}
	}
	if s.HostIPv6 {
		b.WriteString("    icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit } accept\n")
	}
	b.WriteString("  }\n}\n")
	return b.String(), nil
}

func (n *NFTables) run(stdin string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, n.bin(), args...) //nolint:gosec // fixed binary, generated ruleset
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("killswitch: nft %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}

// Engage installs (or atomically replaces) the table.
func (n *NFTables) Engage(s Spec) error {
	rs, err := n.Ruleset(s)
	if err != nil {
		return err
	}
	return n.run(rs, "-f", "-")
}

// Release deletes the table (no-op if absent).
func (n *NFTables) Release() error {
	return n.run(fmt.Sprintf("add table inet %s\ndelete table inet %s\n", n.table(), n.table()), "-f", "-")
}

// Active reports whether the table exists.
func (n *NFTables) Active() (bool, error) {
	err := n.run("", "list", "table", "inet", n.table())
	return err == nil, nil
}
