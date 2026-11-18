// Package killswitch blocks all traffic that does not go through the VPN
// tunnel, at the OS firewall level (ADR-0009, ADR-0053). It complements
// sing-box strict_route, whose rules disappear when the core stops: these
// rules persist while the core restarts and until the user explicitly
// disconnects, so a crashed or restarting core never leaks traffic.
package killswitch

import (
	"errors"
	"net/netip"
	"sort"
)

// Spec is what stays reachable while the kill switch is engaged.
type Spec struct {
	TunName    string       // e.g. "veilon0" (Linux), utunN is matched by source address on macOS
	TunAddr4   netip.Addr   // TUN IPv4 address (Windows/macOS match by source address)
	Servers    []netip.Addr // VPN node addresses (the core must reach them directly)
	APIs       []netip.Addr // veilon-api endpoints (profile refresh while the tunnel is down)
	AllowLAN   bool         // RFC 1918 / link-local / multicast stay reachable
	HostIPv6   bool         // emit IPv6 rules (false on IPv6-less kernels, ADR-0049)
	CorePath   string       // sing-box binary (Windows program rule)
	DaemonPath string       // veilond binary (Windows program rule)
	// DirectMark (Linux): packets carrying this SO_MARK come from the core's
	// "direct" outbound (split tunnelling) and are allowed out.
	DirectMark uint32
}

// DefaultDirectMark is the mark veilond gives the core's direct outbound.
const DefaultDirectMark uint32 = 0x5649 // "VI"

// Engine applies and removes the OS rules. Implementations are idempotent.
type Engine interface {
	Engage(Spec) error
	Release() error
	Active() (bool, error)
}

// ErrUnsupported is returned on platforms without an implementation.
var ErrUnsupported = errors.New("killswitch: not supported on this platform")

// LAN4 and LAN6 are the ranges allowed when Spec.AllowLAN is set.
var (
	LAN4 = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "224.0.0.0/4", "255.255.255.255/32"}
	LAN6 = []string{"fe80::/10", "fc00::/7", "ff00::/8"}
)

// split separates and sorts addresses by family (deterministic rule text).
func split(addrs []netip.Addr) (v4, v6 []string) {
	seen := map[netip.Addr]bool{}
	for _, a := range addrs {
		a = a.Unmap()
		if !a.IsValid() || seen[a] {
			continue
		}
		seen[a] = true
		if a.Is4() {
			v4 = append(v4, a.String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return v4, v6
}
