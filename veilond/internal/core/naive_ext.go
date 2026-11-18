package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/veilon/veil-shared/pkg/crash"
	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/supervisor"
)

// Naive modes (ADR-0002, ADR-0052).
const (
	NaiveInTree   = "in_tree"  // sing-box naive outbound (cronet inside the core)
	NaiveExternal = "external" // official naive binary as a local SOCKS5 proxy
)

// naiveConfig is the external naive config.json: keys are the command-line
// flag names (USAGE.txt). "log" is deliberately absent: naive then neither
// writes nor prints a log (ADR-0011).
type naiveConfig struct {
	Listen       string `json:"listen"`
	Proxy        string `json:"proxy"`
	HostResolver string `json:"host-resolver-rules,omitempty"`
}

// buildNaiveConfig renders the external naive config. The proxy host is the
// SNI (or server name) so the TLS name matches the node certificate; the
// address comes from host-resolver-rules, so naive never queries DNS itself
// (its queries would otherwise be hijacked by the TUN and could loop back
// through naive).
func buildNaiveConfig(n *profile.Naive, ext *singbox.ExternalSOCKS, addrs []netip.Addr) ([]byte, error) {
	host := n.SNI
	if host == "" {
		host = n.Server
	}
	if host == "" || n.Port == 0 {
		return nil, fmt.Errorf("core: naive server missing")
	}
	listen := url.URL{Scheme: "socks", User: url.UserPassword(ext.Username, ext.Password), Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(ext.Port)))}
	proxy := url.URL{Scheme: "https", User: url.UserPassword(n.Username, n.Password), Host: net.JoinHostPort(host, strconv.Itoa(int(n.Port)))}
	cfg := naiveConfig{Listen: listen.String(), Proxy: proxy.String()}
	if _, err := netip.ParseAddr(host); err != nil {
		if len(addrs) == 0 {
			return nil, fmt.Errorf("core: naive server %s not resolved", host)
		}
		// Chromium host resolver rules take one address per MAP rule.
		cfg.HostResolver = "MAP " + host + " " + addrs[0].String()
	}
	return json.Marshal(cfg)
}

// naiveSetup is what Connect needs to run the external naive process.
type naiveSetup struct {
	ext     *singbox.ExternalSOCKS
	cfgPath string
	raw     []byte
}

// prepareNaive decides whether the external naive process is needed for
// node and prepares its config. It returns nil when naive runs in-tree, is
// disabled, or the node has no usable naive endpoint.
func (m *Manager) prepareNaive(node *profile.Node, disabled map[profile.TransportID]bool, res resolution) (*naiveSetup, error) {
	nv := node.Transports.Naive
	if m.opt.NaiveMode != NaiveExternal || nv == nil || !nv.Enabled || disabled[profile.TransportNaive] {
		return nil, nil
	}
	if nv.CertificatePEM != "" {
		// A private test CA cannot be passed to the official binary; such
		// profiles exist only in dev environments (ADR-0052).
		return nil, nil
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("core: bad loopback port %d", port)
	}
	ext := &singbox.ExternalSOCKS{
		Port:     uint16(port), //nolint:gosec // range checked above
		Username: "v" + randomHex(8), Password: randomHex(24), ProcessPath: m.opt.NaivePath,
	}
	host := nv.SNI
	if host == "" {
		host = nv.Server
	}
	addrs := res.addrs[nv.Server]
	if host != nv.Server && len(addrs) == 0 {
		addrs = res.addrs[host]
	}
	raw, err := buildNaiveConfig(nv, ext, addrs)
	if err != nil {
		return nil, err
	}
	return &naiveSetup{ext: ext, cfgPath: filepath.Join(m.opt.RuntimeDir, "naive.json"), raw: raw}, nil
}

// naiveRunner builds the supervised naive process.
func (m *Manager) naiveRunner(ns *naiveSetup) (Runner, error) {
	return m.opt.NewRunner(supervisor.Config{
		Bin: m.opt.NaivePath, ExpectedSHA256: m.opt.NaiveSHA256, AllowUnpinned: m.opt.AllowUnpinned,
		Args:        func() []string { return []string{ns.cfgPath} },
		BeforeStart: func() error { return writeFile0600(ns.cfgPath, ns.raw) },
		Env:         []string{},
		Output:      m.opt.CoreOutput,
		Logger:      m.opt.Logger,
		OnCrash:     m.crashHook(crash.ComponentNaive),
	})
}

// waitListening polls until something accepts TCP on 127.0.0.1:port.
func waitListening(ctx context.Context, port uint16) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
	for {
		c, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
