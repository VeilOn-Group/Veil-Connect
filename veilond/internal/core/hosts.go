package core

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"time"

	"github.com/veilon/veil-shared/pkg/profile"
)

// DefaultResolve resolves host with the system resolver. IP literals are
// returned as is.
func DefaultResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	// The Go resolver may return IPv4 addresses in 4-in-6 form
	// (::ffff:a.b.c.d); naive's host-resolver-rules, nftables sets and
	// IPv6-less hosts need the plain IPv4 form.
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs, err
}

// nodeHosts lists the distinct server host names/addresses of one node.
func nodeHosts(n *profile.Node) []string {
	set := map[string]bool{}
	if v := n.Transports.XHTTP; v != nil && v.Enabled {
		set[v.Server] = true
	}
	if v := n.Transports.VLESS; v != nil && v.Enabled {
		set[v.Server] = true
	}
	if h := n.Transports.Hysteria2; h != nil && h.Enabled {
		set[h.Server] = true
	}
	if nv := n.Transports.Naive; nv != nil && nv.Enabled {
		set[nv.Server] = true
	}
	out := make([]string, 0, len(set))
	for h := range set {
		if h != "" {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// apiHosts extracts host names from API base URLs.
func apiHosts(baseURLs []string) []string {
	var out []string
	for _, u := range baseURLs {
		if pu, err := url.Parse(u); err == nil && pu.Hostname() != "" {
			out = append(out, pu.Hostname())
		}
	}
	return out
}

// resolution is the outcome of resolving the hosts a connection needs.
type resolution struct {
	addrs map[string][]netip.Addr // host -> addresses (IP literals included)
}

func (r resolution) all(hosts []string) []netip.Addr {
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, h := range hosts {
		for _, a := range r.addrs[h] {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// pinned returns the host-name entries for the sing-box hosts DNS server
// (IP literals need no entry).
func (r resolution) pinned(hosts []string) map[string][]string {
	out := map[string][]string{}
	for _, h := range hosts {
		if _, err := netip.ParseAddr(h); err == nil {
			continue
		}
		for _, a := range r.addrs[h] {
			out[h] = append(out[h], a.String())
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveHosts resolves every host, falling back to the last known
// addresses from the state store (the kill switch may block the system
// resolver while no tunnel is up). required hosts must resolve; optional
// ones (API endpoints) are best effort.
func (m *Manager) resolveHosts(ctx context.Context, required, optional []string) (resolution, error) {
	res := resolution{addrs: map[string][]netip.Addr{}}
	cache := m.store.LoadResolved()
	changed := false
	try := func(h string) bool {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		addrs, err := m.opt.Resolve(rctx, h)
		if err == nil && len(addrs) > 0 {
			res.addrs[h] = addrs
			if _, lit := netip.ParseAddr(h); lit != nil {
				strs := make([]string, len(addrs))
				for i, a := range addrs {
					strs[i] = a.String()
				}
				cache[h] = strs
				changed = true
			}
			return true
		}
		for _, s := range cache[h] {
			if a, err := netip.ParseAddr(s); err == nil {
				res.addrs[h] = append(res.addrs[h], a)
			}
		}
		return len(res.addrs[h]) > 0
	}
	for _, h := range required {
		if !try(h) {
			return res, fmt.Errorf("core: cannot resolve server %s", h)
		}
	}
	for _, h := range optional {
		_ = try(h)
	}
	if changed {
		_ = m.store.SaveResolved(cache)
	}
	return res, nil
}
