package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"net/netip"

	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/killswitch"
	"github.com/veilon/veilon-app/veilond/internal/state"
	"github.com/veilon/veilon-app/veilond/internal/supervisor"
	"github.com/veilon/veilon-app/veilond/internal/transport"
)

// fakeKS records kill switch calls.
type fakeKS struct {
	mu      sync.Mutex
	active  bool
	engaged []killswitch.Spec
	events  []string
}

func (k *fakeKS) Engage(s killswitch.Spec) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.active = true
	k.engaged = append(k.engaged, s)
	k.events = append(k.events, "engage")
	return nil
}
func (k *fakeKS) Release() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.active = false
	k.events = append(k.events, "release")
	return nil
}
func (k *fakeKS) Active() (bool, error) { k.mu.Lock(); defer k.mu.Unlock(); return k.active, nil }
func (k *fakeKS) log() string           { k.mu.Lock(); defer k.mu.Unlock(); return strings.Join(k.events, ",") }

// recordingRunner wraps the fake core to log when it starts.
type recordingRunner struct {
	Runner
	onRun func()
}

func (r recordingRunner) Run(ctx context.Context) error { r.onRun(); return r.Runner.Run(ctx) }

func TestKillSwitchLifecycle(t *testing.T) {
	api := newFakeAPI(t)
	m, core, _ := newManager(t, api)
	ks := &fakeKS{}
	m.opt.KillSwitch = ks
	m.opt.APIBaseURLs = []string{"https://198.51.100.5/"}
	inner := m.opt.NewRunner
	m.opt.NewRunner = func(c supervisor.Config) (Runner, error) {
		r, err := inner(c)
		return recordingRunner{Runner: r, onRun: func() { ks.mu.Lock(); ks.events = append(ks.events, "core"); ks.mu.Unlock() }}, err
	}
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	st := m.Settings()
	st.KillSwitch = true
	if err := m.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := ks.log(); got != "engage,core" {
		t.Fatalf("kill switch must engage before the core starts, got %q", got)
	}
	spec := ks.engaged[0]
	if len(spec.Servers) != 1 || spec.Servers[0].String() != "203.0.113.10" || len(spec.APIs) != 1 || spec.TunName != "veilon0" {
		t.Fatalf("spec %+v", spec)
	}
	// Pinned hosts: the core never asks the (blocked) system resolver.
	if core.config().Route.DefaultDomainResolver != singbox.TagDNSPinned {
		t.Fatalf("core must resolve servers from pinned hosts, got %q", core.config().Route.DefaultDomainResolver)
	}
	if !m.Status(ctx).KillSwitchActive {
		t.Fatal("status must report the kill switch")
	}
	// A config change reconnects without a release in between.
	st = m.Settings()
	st.SplitRules = []state.SplitRule{{Kind: "domain_suffix", Value: "bank.example.rs", Action: "direct"}}
	if err := m.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got := ks.log(); strings.Contains(got, "release") {
		t.Fatalf("reconnect must keep the kill switch: %q", got)
	}
	found := false
	for _, r := range core.config().Route.Rules {
		if len(r.DomainSuffix) == 1 && r.DomainSuffix[0] == "bank.example.rs" && r.Outbound == singbox.TagDirect {
			found = true
		}
	}
	if !found {
		t.Fatal("split rule not in core config")
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if on, _ := ks.Active(); on {
		t.Fatal("explicit disconnect must release the kill switch")
	}

	// Stale kill switch from a crashed daemon: reported, released by Disconnect.
	ks.active = true
	m2, _, _ := newManager(t, api)
	m2.opt.KillSwitch = ks
	if !m2.Status(ctx).KillSwitchActive {
		t.Fatal("stale kill switch must be visible")
	}
	if err := m2.Disconnect(ctx); err != nil || ks.active {
		t.Fatalf("disconnect must release a stale kill switch: %v", err)
	}
}

func TestKillSwitchFailClosedOnUnresolvable(t *testing.T) {
	api := newFakeAPI(t)
	api.p.Nodes[0].Transports.VLESS.Server = "unknown.example.net"
	m, _, _ := newManager(t, api)
	ks := &fakeKS{}
	m.opt.KillSwitch = ks
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	st := m.Settings()
	st.KillSwitch = true
	_ = m.UpdateSettings(ctx, st)
	if err := m.Connect(ctx, ""); err == nil || !strings.Contains(err.Error(), "resolve") {
		t.Fatalf("want resolve error, got %v", err)
	}
	m.opt.KillSwitch = nil
	if err := m.UpdateSettings(ctx, st); !errors.Is(err, ErrNoKillSwitch) {
		t.Fatalf("unsupported platform must refuse the kill switch, got %v", err)
	}
}

func TestResolvedCacheFallback(t *testing.T) {
	api := newFakeAPI(t)
	m, _, _ := newManager(t, api)
	ctx := context.Background()
	if _, err := m.resolveHosts(ctx, []string{"node1.example.net"}, nil); err != nil {
		t.Fatal(err)
	}
	m.opt.Resolve = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("blocked") }
	res, err := m.resolveHosts(ctx, []string{"node1.example.net"}, nil)
	if err != nil || res.addrs["node1.example.net"][0].String() != "203.0.113.10" {
		t.Fatalf("cache fallback: %v %v", res.addrs, err)
	}
}

// fakeNaive emulates the external naive binary: it reads its config and
// listens on the SOCKS port from "listen".
type fakeNaive struct {
	cfg     supervisor.Config
	mu      sync.Mutex
	seen    naiveConfig
	perm    os.FileMode
	started chan struct{}
}

func (n *fakeNaive) State() (supervisor.State, int, error) { return supervisor.StateRunning, 2, nil }
func (n *fakeNaive) Run(ctx context.Context) error {
	if err := n.cfg.BeforeStart(); err != nil {
		return err
	}
	path := n.cfg.Args()[0]
	st, _ := os.Stat(path)
	raw, _ := os.ReadFile(path)
	n.mu.Lock()
	n.perm = st.Mode().Perm()
	err := json.Unmarshal(raw, &n.seen)
	n.mu.Unlock()
	close(n.started)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), `"log"`) {
		return errors.New("naive config must not enable logging")
	}
	<-ctx.Done()
	return nil
}

func TestNaiveExternalMode(t *testing.T) {
	api := newFakeAPI(t)
	m, core, runtime := newManager(t, api)
	m.opt.NaiveMode, m.opt.NaivePath, m.opt.NaiveSHA256 = NaiveExternal, "/usr/lib/veilon/naive", "11"
	naive := &fakeNaive{started: make(chan struct{})}
	inner := m.opt.NewRunner
	m.opt.NewRunner = func(c supervisor.Config) (Runner, error) {
		if c.Bin == m.opt.NaivePath {
			if c.ExpectedSHA256 != "11" {
				t.Error("naive binary must be pinned")
			}
			naive.cfg = c
			return naive, nil
		}
		return inner(c)
	}
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	m.opt.ReadyTimeout = 500 * time.Millisecond // fake naive does not listen
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Disconnect(ctx) }()
	<-naive.started
	naive.mu.Lock()
	defer naive.mu.Unlock()
	if naive.perm != 0o600 {
		t.Fatalf("naive config perms %v", naive.perm)
	}
	nv := api.p.Nodes[0].Transports.Naive
	if !strings.HasPrefix(naive.seen.Listen, "socks://v") || !strings.Contains(naive.seen.Listen, "@127.0.0.1:") {
		t.Fatalf("listen %q", naive.seen.Listen)
	}
	if !strings.Contains(naive.seen.Proxy, "@"+nv.SNI+":443") && !strings.Contains(naive.seen.Proxy, "@"+nv.Server+":443") {
		t.Fatalf("proxy %q", naive.seen.Proxy)
	}
	if naive.seen.HostResolver != "MAP node1.example.net 203.0.113.10" {
		t.Fatalf("host resolver rules %q", naive.seen.HostResolver)
	}
	seen := core.config()
	var socks *singbox.Outbound
	for i, o := range seen.Outbounds {
		if o.Tag == singbox.TagNaive {
			socks = &seen.Outbounds[i]
		}
	}
	if socks == nil || socks.Type != "socks" || socks.Username == "" || socks.Password == "" {
		t.Fatalf("core must reach naive through authenticated SOCKS: %+v", socks)
	}
	if !strings.Contains(naive.seen.Listen, socks.Username+":"+socks.Password+"@") {
		t.Fatal("core and naive credentials differ")
	}
	if len(seen.Route.Rules) == 0 || len(seen.Route.Rules[0].ProcessPath) != 1 {
		t.Fatal("naive's own traffic must bypass the TUN (process_path rule first)")
	}
	if _, err := os.Stat(runtime + "/naive.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("naive config with credentials must be removed after start")
	}
	if !m.Status(ctx).NaiveExternal {
		t.Fatal("status must report external naive")
	}
}

func TestSwitchNode(t *testing.T) {
	api := newFakeAPI(t)
	n2 := api.p.Nodes[0]
	n2.ID, n2.Name, n2.Region = "node-2", "Beograd 2", "RS"
	h := *n2.Transports.Hysteria2
	h.Server = "node2.example.net"
	n2.Transports.Hysteria2 = &h
	api.p.Nodes = append(api.p.Nodes, n2)
	m, core, _ := newManager(t, api)
	ks := &fakeKS{}
	m.opt.KillSwitch = ks
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	st := m.Settings()
	st.KillSwitch = true
	_ = m.UpdateSettings(ctx, st)
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.SwitchNode(ctx, "nope"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("unknown node: %v", err)
	}
	if err := m.SwitchNode(ctx, "node-2"); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(ctx); s.NodeID != "node-2" || s.State != Connected {
		t.Fatalf("status %+v", s)
	}
	if m.Settings().NodeID != "node-2" {
		t.Fatal("location not remembered")
	}
	if strings.Contains(ks.log(), "release") {
		t.Fatal("location switch must not release the kill switch")
	}
	last := ks.engaged[len(ks.engaged)-1]
	if len(last.Servers) != 2 { // 203.0.113.10 (naive) + 203.0.113.20 (hy2)
		t.Fatalf("servers %v", last.Servers)
	}
	found := false
	for _, o := range core.config().Outbounds {
		if o.Tag == singbox.TagHysteria2 && o.Server == "node2.example.net" {
			found = true
		}
	}
	if !found {
		t.Fatal("core config not built for node-2")
	}
	_ = m.Disconnect(ctx)
}

// stubTransport has fixed health.
type stubTransport struct {
	transport.Transport
	h transport.Health
}

func (s stubTransport) Health() transport.Health { return s.h }

func TestAutoSwitchStep(t *testing.T) {
	api := newFakeAPI(t)
	m, core, _ := newManager(t, api)
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	// Stub health before connecting: the health loop reads m.trans.
	for _, tr := range transport.All() {
		h := transport.Health{Reachable: true, LatencyMS: 50}
		if tr.ID() == profile.TransportVLESS {
			h = transport.Health{Reachable: false}
		}
		m.trans[tr.ID()] = stubTransport{Transport: tr, h: h}
	}
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Disconnect(ctx) }()
	m.mu.Lock()
	clash := m.clash
	m.mu.Unlock()
	pol := api.p.Fallback
	pol.FailThreshold = 2
	a := newAutoSwitcher(pol)
	node := &api.p.Nodes[0]
	now := testNow
	if d := a.step(ctx, m, clash, node, now); len(d) != 0 {
		t.Fatalf("one failure must not switch: %+v", d)
	}
	d := a.step(ctx, m, clash, node, now.Add(time.Second))
	if len(d) != 1 || d[0].To == profile.TransportVLESS {
		t.Fatalf("want switch away from failed vless, got %+v", d)
	}
	core.mu.Lock()
	now2 := core.selected[singbox.TagSelectorTCP]
	core.mu.Unlock()
	if now2 == singbox.TagVLESS {
		t.Fatal("selector not changed through the Clash API")
	}
	_ = clash
}

// fakeXray captures the Xray config the manager writes.
type fakeXray struct {
	cfg     supervisor.Config
	mu      sync.Mutex
	raw     []byte
	perm    os.FileMode
	started chan struct{}
}

func (x *fakeXray) State() (supervisor.State, int, error) { return supervisor.StateRunning, 1, nil }
func (x *fakeXray) Run(ctx context.Context) error {
	if err := x.cfg.BeforeStart(); err != nil {
		return err
	}
	args := x.cfg.Args()
	path := args[len(args)-1]
	st, _ := os.Stat(path)
	raw, _ := os.ReadFile(path)
	x.mu.Lock()
	x.perm, x.raw = st.Mode().Perm(), raw
	x.mu.Unlock()
	close(x.started)
	<-ctx.Done()
	return nil
}

// TestXraySidecar: VLESS (XHTTP default, Vision) runs in a pinned Xray
// process; sing-box reaches it through authenticated loopback SOCKS and
// lets its traffic bypass the TUN.
func TestXraySidecar(t *testing.T) {
	api := newFakeAPI(t)
	m, core, runtime := newManager(t, api)
	m.opt.XrayPath, m.opt.XraySHA256 = "/usr/lib/veilon/xray", "22"
	x := &fakeXray{started: make(chan struct{})}
	inner := m.opt.NewRunner
	m.opt.NewRunner = func(c supervisor.Config) (Runner, error) {
		if c.Bin == m.opt.XrayPath {
			if c.ExpectedSHA256 != "22" || len(c.Env) != 0 {
				t.Error("xray must be pinned and get an empty environment")
			}
			x.cfg = c
			return x, nil
		}
		return inner(c)
	}
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	m.opt.ReadyTimeout = 500 * time.Millisecond // fake xray does not listen
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Disconnect(ctx) }()
	<-x.started
	x.mu.Lock()
	raw, perm := string(x.raw), x.perm
	x.mu.Unlock()
	if perm != 0o600 {
		t.Fatalf("xray config perms %v", perm)
	}
	if !strings.Contains(raw, `"network":"xhttp"`) || !strings.Contains(raw, `"loglevel":"none"`) || !strings.Contains(raw, `"address":"203.0.113.10"`) {
		t.Fatalf("xray config: %s", raw)
	}
	cfg := core.config()
	var xh *singbox.Outbound
	for i, o := range cfg.Outbounds {
		if o.Tag == singbox.TagXHTTP {
			xh = &cfg.Outbounds[i]
		}
		if o.Tag == singbox.TagSelectorTCP && o.Default != singbox.TagXHTTP {
			t.Fatalf("XHTTP must be the default transport, got %s", o.Default)
		}
	}
	if xh == nil || xh.Type != "socks" || !strings.Contains(raw, xh.Password) {
		t.Fatal("sing-box must reach Xray through its per-start SOCKS credentials")
	}
	if len(cfg.Route.Rules) == 0 || cfg.Route.Rules[0].ProcessPath[0] != m.opt.XrayPath {
		t.Fatal("Xray's own traffic must bypass the TUN")
	}
	if _, err := os.Stat(runtime + "/xray.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("xray config with credentials must be removed after start")
	}
	if len(cfg.Route.RuleSet) != 1 || cfg.Route.RuleSet[0].Tag != "geosite-category-ru" {
		t.Fatal("category-ru rule set must be on by default")
	}
}
