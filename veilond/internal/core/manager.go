// Package core orchestrates veilond: verified profile -> sing-box config ->
// supervised core -> manual/auto transport selection -> local health.
// Nothing here is ever sent off the device except the API calls in
// apiclient (auth, keyset, profile).
package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/veilon/veil-shared/assets/rulesets"
	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veil-shared/pkg/crash"
	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/sign"
	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/clashapi"
	"github.com/veilon/veilon-app/veilond/internal/crashq"
	"github.com/veilon/veilon-app/veilond/internal/killswitch"
	"github.com/veilon/veilon-app/veilond/internal/state"
	"github.com/veilon/veilon-app/veilond/internal/supervisor"
	"github.com/veilon/veilon-app/veilond/internal/transport"
)

// ConnState is the tunnel state shown in the UI.
type ConnState string

const (
	Disconnected  ConnState = "disconnected"
	Connecting    ConnState = "connecting"
	Connected     ConnState = "connected"
	Reconnecting  ConnState = "reconnecting"
	Disconnecting ConnState = "disconnecting"
	Failed        ConnState = "failed"
)

// Errors surfaced to the UI.
var (
	ErrNoProfile       = errors.New("core: no verified profile; sign in and refresh")
	ErrNotLoggedIn     = errors.New("core: not signed in")
	ErrNotConnected    = errors.New("core: not connected")
	ErrAlreadyRunning  = errors.New("core: already connected")
	ErrUDPToNaive      = errors.New("core: naive cannot carry UDP")
	ErrUnknownGroup    = errors.New("core: group must be tcp or udp")
	ErrClientTooOld    = errors.New("core: profile requires a newer client")
	ErrTransportAbsent = errors.New("core: transport not available on this node")
	ErrUnknownNode     = errors.New("core: location not in profile")
	ErrNoKillSwitch    = errors.New("core: kill switch is not supported on this platform")
)

// API is the subset of apiclient used by the manager (mockable).
type API interface {
	Register(ctx context.Context, key ed25519.PrivateKey, label string) (*apitypes.TokenResponse, error)
	Login(ctx context.Context, number string, key ed25519.PrivateKey, label string) (*apitypes.TokenResponse, error)
	Refresh(ctx context.Context, refresh string, key ed25519.PrivateKey) (*apitypes.TokenResponse, error)
	Logout(ctx context.Context, access, refresh string) error
	Keyset(ctx context.Context) (*sign.Envelope, error)
	Profile(ctx context.Context, access string) (*sign.Envelope, error)
}

// Runner abstracts the supervised core for tests.
type Runner interface {
	Run(ctx context.Context) error
	State() (supervisor.State, int, error)
}

// Options configure a Manager.
type Options struct {
	ClientVersion string
	RuntimeDir    string
	SingBoxPath   string
	SingBoxSHA256 string
	AllowUnpinned bool
	AllowInsecure bool
	TunStack      string
	RootKeys      []sign.PublicKey
	CoreOutput    io.Writer // redacting writer into the local log ring
	Logger        *slog.Logger
	Now           func() time.Time
	// NewRunner builds the core runner; defaults to the real supervisor.
	NewRunner func(supervisor.Config) (Runner, error)
	// ReadyTimeout bounds waiting for the Clash API after start.
	ReadyTimeout time.Duration
	// HostHasIPv6 reports whether the OS has a usable IPv6 stack (default:
	// try binding a UDP socket to [::1]).
	HostHasIPv6 func() bool

	// CoreExtraFiles are files the core loads at runtime (e.g. libcronet for
	// the in-tree naive outbound), pinned by SHA-256 like the core itself.
	CoreExtraFiles map[string]string
	// NaiveMode is NaiveInTree (default) or NaiveExternal.
	NaiveMode   string
	NaivePath   string
	NaiveSHA256 string
	// XrayPath and XraySHA256: the Xray process for the VLESS transports.
	// Empty: XHTTP and Vision are not offered.
	XrayPath   string
	XraySHA256 string
	// PaymentHosts overrides DefaultPaymentHosts (confirmation URL hosts).
	PaymentHosts []string

	// KillSwitch is the OS firewall engine; nil if unsupported.
	KillSwitch killswitch.Engine
	// DaemonPath is veilond's own binary (Windows program rule).
	DaemonPath string
	// APIBaseURLs are allowed through the kill switch (profile refresh).
	APIBaseURLs []string
	// Resolve resolves host names before the tunnel is up.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// TunName must match the generator default.
	TunName string
	// UpdateHTTP downloads update artifacts (tests inject a client).
	UpdateHTTP *http.Client
	// UpdateInbox is the silent updater's inbox; empty = none.
	UpdateInbox string
	// Crashes queues crash reports of veilond and the cores;
	// nil disables crash reporting.
	Crashes *crashq.Queue
}

// DetectIPv6 is the default HostHasIPv6 probe. Only Linux kernels can lack
// IPv6 in practice; other platforms always report true.
func DetectIPv6() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	c, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp6", "[::1]:0")
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Manager is the veilond brain. All exported methods are goroutine-safe.
type Manager struct {
	opt   Options
	store *state.Store
	api   API

	mu         sync.Mutex
	state      ConnState
	lastErr    string
	prof       *profile.Profile
	trans      map[profile.TransportID]transport.Transport
	cancel     context.CancelFunc
	done       chan struct{}
	clash      *clashapi.Client
	coreVer    string
	activeNode string
	ksOn       bool // kill switch engaged by this daemon (or found engaged)
	upd        updateState
	probe      *socksProber
	refreshCh  chan struct{}
	pay        payState

	crashFlushing atomic.Bool
}

// New creates a manager.
func New(opt Options, store *state.Store, api API) *Manager {
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.CoreOutput == nil {
		opt.CoreOutput = io.Discard
	}
	if opt.ReadyTimeout == 0 {
		opt.ReadyTimeout = 15 * time.Second
	}
	if opt.HostHasIPv6 == nil {
		opt.HostHasIPv6 = DetectIPv6
	}
	if opt.Resolve == nil {
		opt.Resolve = DefaultResolve
	}
	if opt.NaiveMode == "" {
		opt.NaiveMode = NaiveInTree
	}
	if opt.TunName == "" {
		opt.TunName = "veilon0"
	}
	if opt.NewRunner == nil {
		opt.NewRunner = func(c supervisor.Config) (Runner, error) { return supervisor.New(c) }
	}
	m := &Manager{opt: opt, store: store, api: api, state: Disconnected, trans: map[profile.TransportID]transport.Transport{}, refreshCh: make(chan struct{}, 1)}
	for _, t := range transport.All() {
		if nv, ok := t.(*transport.Naive); ok {
			nv.External = opt.NaiveMode == NaiveExternal
		}
		m.trans[t.ID()] = t
	}
	return m
}

// ---------- account & profile ----------

// Register creates an anonymous account and returns its number (shown once).
func (m *Manager) Register(ctx context.Context) (string, error) {
	if m.api == nil {
		return "", ErrUnsupported
	}
	key, err := m.store.DeviceKey()
	if err != nil {
		return "", err
	}
	tok, err := m.api.Register(ctx, key, "")
	if err != nil {
		return "", err
	}
	if err := m.saveTokens(tok); err != nil {
		return "", err
	}
	if err := m.store.SaveAccountNumber(tok.AccountNumber); err != nil {
		return "", err
	}
	return tok.AccountNumber, m.RefreshProfile(ctx)
}

// Login with an existing account number.
func (m *Manager) Login(ctx context.Context, number string) error {
	if m.api == nil {
		return ErrUnsupported
	}
	key, err := m.store.DeviceKey()
	if err != nil {
		return err
	}
	tok, err := m.api.Login(ctx, number, key, "")
	if err != nil {
		return err
	}
	if err := m.saveTokens(tok); err != nil {
		return err
	}
	if err := m.store.SaveAccountNumber(number); err != nil {
		return err
	}
	return m.RefreshProfile(ctx)
}

// Logout disconnects, revokes the session and wipes local account data.
func (m *Manager) Logout(ctx context.Context) error {
	_ = m.Disconnect(ctx)
	if tok, err := m.store.LoadTokens(); err == nil && m.api != nil {
		_ = m.api.Logout(ctx, tok.Access, tok.Refresh) // best effort
	}
	m.mu.Lock()
	m.prof = nil
	m.mu.Unlock()
	return m.store.ClearSession()
}

func (m *Manager) saveTokens(t *apitypes.TokenResponse) error {
	return m.store.SaveTokens(state.Tokens{
		Access: t.AccessToken, Refresh: t.RefreshToken,
		AccessExp: m.opt.Now().Add(time.Duration(t.ExpiresIn) * time.Second).Unix(),
	})
}

// accessToken returns a valid access token, refreshing if it expires soon.
func (m *Manager) accessToken(ctx context.Context) (string, error) {
	tok, err := m.store.LoadTokens()
	if err != nil {
		return "", ErrNotLoggedIn
	}
	if m.opt.Now().Add(60*time.Second).Unix() < tok.AccessExp {
		return tok.Access, nil
	}
	if m.api == nil {
		return "", ErrUnsupported
	}
	key, err := m.store.DeviceKey()
	if err != nil {
		return "", err
	}
	nt, err := m.api.Refresh(ctx, tok.Refresh, key)
	if err != nil {
		return "", fmt.Errorf("core: refresh session: %w", err)
	}
	if err := m.saveTokens(nt); err != nil {
		return "", err
	}
	return nt.AccessToken, nil
}

// RefreshProfile downloads keyset + profile, verifies both, and caches them.
func (m *Manager) RefreshProfile(ctx context.Context) error {
	if m.api == nil {
		return ErrUnsupported
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return err
	}
	ksEnv, err := m.api.Keyset(ctx)
	if err != nil {
		return err
	}
	prev, _ := m.store.LoadKeyset()
	ks, err := sign.VerifyKeyset(sign.NewVerifier(m.opt.RootKeys, m.opt.Now), ksEnv, prev.Version)
	if err != nil {
		return fmt.Errorf("core: keyset: %w", err)
	}
	env, err := m.api.Profile(ctx, access)
	if err != nil {
		return err
	}
	p, err := m.verifyProfile(ks, env)
	if err != nil {
		return err
	}
	if err := m.store.SaveKeyset(state.KeysetRecord{Envelope: ksEnv, Version: ks.Version}); err != nil {
		return err
	}
	if err := m.store.SaveProfile(env); err != nil {
		return err
	}
	m.mu.Lock()
	m.prof = p
	m.mu.Unlock()
	return nil
}

func (m *Manager) verifyProfile(ks *sign.Keyset, env *sign.Envelope) (*profile.Profile, error) {
	var p profile.Profile
	if err := sign.VerifierFromKeyset(ks, m.opt.Now).VerifyInto(env, sign.PurposeProfile, &p); err != nil {
		return nil, fmt.Errorf("core: profile signature: %w", err)
	}
	if err := p.Validate(m.opt.Now()); err != nil {
		return nil, err
	}
	need, _ := profile.ParseSemver(p.MinClientVersion)
	have, err := profile.ParseSemver(m.opt.ClientVersion)
	if err != nil || have.Compare(need) < 0 {
		return nil, ErrClientTooOld
	}
	return &p, nil
}

// LoadCachedProfile re-verifies the cached profile (offline start).
func (m *Manager) LoadCachedProfile() error {
	rec, err := m.store.LoadKeyset()
	if err != nil {
		return ErrNoProfile
	}
	ks, err := sign.VerifyKeyset(sign.NewVerifier(m.opt.RootKeys, m.opt.Now), rec.Envelope, rec.Version)
	if err != nil {
		return err
	}
	env, err := m.store.LoadProfile()
	if err != nil {
		return ErrNoProfile
	}
	p, err := m.verifyProfile(ks, env)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.prof = p
	m.mu.Unlock()
	return nil
}

// ---------- connection ----------

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func freeLoopbackPort() (int, error) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Connect starts the core for nodeID (empty = settings or first node).
func (m *Manager) Connect(ctx context.Context, nodeID string) error {
	m.mu.Lock()
	if m.state == Connected || m.state == Connecting || m.state == Reconnecting {
		m.mu.Unlock()
		return ErrAlreadyRunning
	}
	p := m.prof
	m.mu.Unlock()
	if p == nil {
		if err := m.LoadCachedProfile(); err != nil {
			return ErrNoProfile
		}
		m.mu.Lock()
		p = m.prof
		m.mu.Unlock()
	}
	settings := m.store.LoadSettings()
	if nodeID == "" {
		nodeID = settings.NodeID
	}
	if nodeID != "" && !hasNode(p, nodeID) {
		nodeID = ""
	}
	if nodeID == "" {
		nodeID = p.Nodes[0].ID
	}
	node := findNode(p, nodeID)
	if err := state.ValidateSplitRules(settings.SplitRules); err != nil {
		return err
	}
	if settings.KillSwitch && m.opt.KillSwitch == nil {
		return ErrNoKillSwitch
	}

	// Resolve everything the core and naive need while DNS still works (or
	// from the last known addresses when the kill switch already blocks it).
	servers := nodeHosts(node)
	apis := apiHosts(m.opt.APIBaseURLs)
	res, err := m.resolveHosts(ctx, nil, append(append([]string(nil), servers...), apis...))
	if err != nil {
		return err
	}
	if settings.KillSwitch {
		for _, h := range servers {
			if len(res.addrs[h]) == 0 {
				return fmt.Errorf("core: cannot resolve server %s", h)
			}
		}
	}

	port, err := freeLoopbackPort()
	if err != nil {
		return err
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	secret := randomHex(32)
	probePort, err := freeLoopbackPort()
	if err != nil {
		return err
	}
	probe := socksProber{addr: fmt.Sprintf("127.0.0.1:%d", probePort), password: randomHex(24), timeout: 6 * time.Second}
	disabled := map[profile.TransportID]bool{}
	for k, v := range settings.DisabledTransport {
		disabled[profile.TransportID(k)] = v
	}
	naive, err := m.prepareNaive(node, disabled, res)
	if err != nil {
		return err
	}
	if m.opt.NaiveMode == NaiveExternal && naive == nil {
		// No usable external endpoint: keep naive out of the core config
		// rather than letting the in-tree outbound run without its runtime.
		disabled[profile.TransportNaive] = true
	}
	var directMark uint32
	if settings.KillSwitch && runtime.GOOS == "linux" {
		directMark = killswitch.DefaultDirectMark
	}
	xplan, err := m.prepareXray(node, disabled, res, directMark)
	if err != nil {
		return err
	}
	hostNoIPv6 := !m.opt.HostHasIPv6()
	opts := singbox.ClientOptions{
		NodeID: nodeID, TunName: m.opt.TunName, TunStack: m.opt.TunStack,
		ClashAPIListen: listen, ClashAPISecret: secret,
		CacheFilePath: filepath.Join(m.opt.RuntimeDir, "cache.db"),
		Disabled:      disabled, AllowLAN: settings.AllowLAN, AllowInsecure: m.opt.AllowInsecure,
		HostNoIPv6:  hostNoIPv6,
		UserRules:   state.RouteRules(settings.SplitRules),
		ProbeListen: probe.addr, ProbePassword: probe.password,
		PinnedHosts: res.pinned(servers),
	}
	dns := settings.DNSOrDefault()
	opts.DNS = &singbox.DNSChoice{Provider: dns.Provider, CustomURL: dns.CustomURL}
	if naive != nil {
		opts.NaiveExternal = naive.ext
	}
	if xplan != nil {
		opts.Xray = xplan.plan.Endpoints
	}
	opts.DirectMark = directMark
	opts.BypassSuffixes = bypassZones(settings, runtime.GOOS)
	if len(opts.BypassSuffixes) > 0 {
		if err := os.MkdirAll(m.opt.RuntimeDir, 0o700); err != nil {
			return err
		}
		// The bundled geosite-category-ru rule set; without it the
		// zone suffixes still apply.
		if path, err := rulesets.WriteCategoryRU(m.opt.RuntimeDir); err == nil {
			opts.BypassRuleSets = []singbox.RuleSetFile{{Tag: rulesets.CategoryRUTag, Path: path}}
		} else {
			m.opt.Logger.Warn("category-ru rule set unavailable; zone suffixes only", "err", err)
		}
	}
	cfg, err := singbox.BuildClient(p, opts)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.opt.RuntimeDir, 0o700); err != nil {
		return err
	}
	cfgPath := filepath.Join(m.opt.RuntimeDir, "core.json")
	clash, err := clashapi.New(listen, secret)
	if err != nil {
		return err
	}
	runner, err := m.opt.NewRunner(supervisor.Config{
		Bin: m.opt.SingBoxPath, ExpectedSHA256: m.opt.SingBoxSHA256, AllowUnpinned: m.opt.AllowUnpinned,
		ExtraFiles:  m.opt.CoreExtraFiles,
		Args:        func() []string { return []string{"run", "-c", cfgPath, "--disable-color"} },
		BeforeStart: func() error { return writeFile0600(cfgPath, raw) },
		Env:         []string{}, // minimal environment for the core
		Output:      m.opt.CoreOutput,
		Logger:      m.opt.Logger,
		OnState: func(s supervisor.State, err error) {
			m.onCoreState(s, err)
		},
		OnCrash: m.crashHook(crash.ComponentSingBox),
	})
	if err != nil {
		return err
	}
	var naiveRun, xrayRun Runner
	if naive != nil {
		if naiveRun, err = m.naiveRunner(naive); err != nil {
			return err
		}
	}
	if xplan != nil {
		if xrayRun, err = m.xrayRunner(xplan); err != nil {
			return err
		}
	}

	// Kill switch before anything starts: from here until an explicit
	// disconnect nothing leaves the host outside the tunnel.
	if settings.KillSwitch {
		spec := killswitch.Spec{
			TunName: m.opt.TunName, TunAddr4: netip.MustParseAddr("172.19.0.1"),
			Servers: res.all(servers), APIs: res.all(apis),
			AllowLAN: settings.AllowLAN, HostIPv6: !hostNoIPv6,
			CorePath: m.opt.SingBoxPath, DaemonPath: m.opt.DaemonPath,
			DirectMark: opts.DirectMark,
		}
		if err := m.opt.KillSwitch.Engage(spec); err != nil {
			return fmt.Errorf("core: kill switch: %w", err)
		}
		m.mu.Lock()
		m.ksOn = true
		m.mu.Unlock()
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.state, m.lastErr = Connecting, ""
	m.cancel, m.done, m.clash, m.activeNode = cancel, done, clash, nodeID
	m.probe = &probe
	m.mu.Unlock()
	var wg sync.WaitGroup
	if naiveRun != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := naiveRun.Run(runCtx); err != nil {
				m.opt.Logger.Warn("naive stopped", "err", err)
			}
			_ = os.Remove(naive.cfgPath)
		}()
	}
	if xrayRun != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := xrayRun.Run(runCtx); err != nil {
				m.opt.Logger.Warn("xray stopped", "err", err)
			}
			_ = os.Remove(xplan.cfgPath)
		}()
	}
	readyCtx, rc := context.WithTimeout(ctx, m.opt.ReadyTimeout)
	defer rc()
	var runErr atomic.Pointer[error]
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := runner.Run(runCtx); err != nil {
			// e.g. a tampered or untrusted binary: never retried.
			m.opt.Logger.Error("core stopped permanently", "err", err)
			m.setFailed(err)
			runErr.Store(&err)
			rc() // do not wait for a core that will never start
		}
		_ = os.Remove(cfgPath)
	}()
	go func() { wg.Wait(); close(done) }()

	ver, err := clash.WaitReady(readyCtx)
	// The config contains credentials: remove it as soon as the core has read it.
	_ = os.Remove(cfgPath)
	if e := runErr.Load(); e != nil {
		err = *e
	}
	if err != nil {
		// Fail closed: the kill switch (if on) stays engaged.
		_ = m.stop(context.Background())
		m.setFailed(fmt.Errorf("core did not become ready: %w", err))
		return err
	}
	if naive != nil {
		nctx, nc := context.WithTimeout(ctx, m.opt.ReadyTimeout)
		if err := waitListening(nctx, naive.ext.Port); err != nil {
			m.opt.Logger.Warn("external naive not listening; transport stays unhealthy", "err", err)
		}
		nc()
		_ = os.Remove(naive.cfgPath)
	}
	if xplan != nil {
		xctx, xc := context.WithTimeout(ctx, m.opt.ReadyTimeout)
		for _, ep := range xplan.plan.Endpoints {
			if err := waitListening(xctx, ep.Port); err != nil {
				m.opt.Logger.Warn("xray not listening; VLESS transports stay unhealthy", "err", err)
				break
			}
		}
		xc()
		// The config holds the node credentials: remove it once read.
		_ = os.Remove(xplan.cfgPath)
	}
	if settings.NodeID != nodeID {
		settings.NodeID = nodeID
		_ = m.store.SaveSettings(settings)
	}
	if !settings.AutoSwitch {
		m.applyManualSelection(ctx, settings)
	}
	m.mu.Lock()
	m.coreVer = ver
	if m.state == Connecting {
		m.state = Connected
	}
	m.mu.Unlock()
	go m.healthLoop(runCtx, p, nodeID)
	return nil
}

// SwitchNode changes the location. While connected, the new node's
// addresses are resolved through the running tunnel first, then the core is
// restarted without releasing the kill switch.
func (m *Manager) SwitchNode(ctx context.Context, nodeID string) error {
	m.mu.Lock()
	p := m.prof
	connected := m.cancel != nil
	m.mu.Unlock()
	if p == nil {
		if err := m.LoadCachedProfile(); err != nil {
			return ErrNoProfile
		}
		m.mu.Lock()
		p = m.prof
		m.mu.Unlock()
	}
	if !hasNode(p, nodeID) {
		return ErrUnknownNode
	}
	st := m.store.LoadSettings()
	st.NodeID = nodeID
	if err := m.store.SaveSettings(st); err != nil {
		return err
	}
	if !connected {
		return nil
	}
	// Warm the cache while the tunnel still resolves names.
	_, _ = m.resolveHosts(ctx, nil, nodeHosts(findNode(p, nodeID)))
	if err := m.stop(ctx); err != nil {
		return err
	}
	return m.Connect(ctx, nodeID)
}

func findNode(p *profile.Profile, id string) *profile.Node {
	for i := range p.Nodes {
		if p.Nodes[i].ID == id {
			return &p.Nodes[i]
		}
	}
	return &p.Nodes[0]
}

func hasNode(p *profile.Profile, id string) bool {
	for _, n := range p.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func writeFile0600(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // runtime dir from config
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (m *Manager) onCoreState(s supervisor.State, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch s {
	case supervisor.StateBackoff:
		if m.state == Connected {
			m.state = Reconnecting
		}
	case supervisor.StateRunning:
		if m.state == Reconnecting {
			m.state = Connected // selection is restored from cache_file by the core
		}
	case supervisor.StateFailed:
		m.state = Failed
		if err != nil {
			m.lastErr = err.Error()
		}
	}
}

func (m *Manager) setFailed(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = Failed
	m.lastErr = err.Error()
}

func (m *Manager) applyManualSelection(ctx context.Context, st state.Settings) {
	if st.ManualTCP != "" {
		_ = m.SelectTransport(ctx, "tcp", profile.TransportID(st.ManualTCP))
	}
	if st.ManualUDP != "" {
		_ = m.SelectTransport(ctx, "udp", profile.TransportID(st.ManualUDP))
	}
}

// Disconnect is the user's explicit disconnect: it stops the core and
// releases the kill switch.
func (m *Manager) Disconnect(ctx context.Context) error {
	if err := m.stop(ctx); err != nil {
		return err
	}
	return m.releaseKillSwitch()
}

func (m *Manager) releaseKillSwitch() error {
	m.mu.Lock()
	on := m.ksOn
	m.mu.Unlock()
	if m.opt.KillSwitch == nil {
		return nil
	}
	if !on {
		// A previous daemon instance may have left it engaged (crash).
		if active, err := m.opt.KillSwitch.Active(); err != nil || !active {
			return nil
		}
	}
	if err := m.opt.KillSwitch.Release(); err != nil {
		return fmt.Errorf("core: release kill switch: %w", err)
	}
	m.mu.Lock()
	m.ksOn = false
	m.mu.Unlock()
	return nil
}

// stop stops the core (and external naive) but keeps the kill switch.
func (m *Manager) stop(ctx context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	if cancel == nil {
		m.state = Disconnected
		m.mu.Unlock()
		return nil
	}
	m.state = Disconnecting
	m.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.mu.Lock()
	m.state, m.cancel, m.done, m.clash, m.coreVer = Disconnected, nil, nil, nil, ""
	for _, t := range m.trans {
		_ = t.Stop(ctx)
	}
	m.mu.Unlock()
	return nil
}

// SelectTransport switches the TCP or UDP group manually and remembers it.
func (m *Manager) SelectTransport(ctx context.Context, group string, id profile.TransportID) error {
	var sel string
	switch group {
	case "tcp":
		sel = singbox.TagSelectorTCP
	case "udp":
		if !id.SupportsUDP() {
			return ErrUDPToNaive
		}
		sel = singbox.TagSelectorUDP
	default:
		return ErrUnknownGroup
	}
	tag := singbox.TagFor(id)
	if tag == "" {
		return ErrTransportAbsent
	}
	m.mu.Lock()
	clash := m.clash
	m.mu.Unlock()
	if clash == nil {
		return ErrNotConnected
	}
	if err := clash.Select(ctx, sel, tag); err != nil {
		var apiErr *clashapi.Error
		if errors.As(err, &apiErr) && apiErr.Status == 400 {
			return ErrTransportAbsent
		}
		return err
	}
	st := m.store.LoadSettings()
	if group == "tcp" {
		st.ManualTCP = string(id)
	} else {
		st.ManualUDP = string(id)
	}
	st.AutoSwitch = false // a manual choice wins until auto is re-enabled
	return m.store.SaveSettings(st)
}

// SetTransportEnabled toggles a transport; reconnects if needed.
func (m *Manager) SetTransportEnabled(ctx context.Context, id profile.TransportID, enabled bool) error {
	if singbox.TagFor(id) == "" {
		return ErrTransportAbsent
	}
	st := m.store.LoadSettings()
	st.DisabledTransport[string(id)] = !enabled
	if st.ManualTCP == string(id) && !enabled {
		st.ManualTCP = ""
	}
	if st.ManualUDP == string(id) && !enabled {
		st.ManualUDP = ""
	}
	if err := m.store.SaveSettings(st); err != nil {
		return err
	}
	return m.reconnectIfConnected(ctx)
}

func (m *Manager) reconnectIfConnected(ctx context.Context) error {
	m.mu.Lock()
	connected := m.state == Connected || m.state == Reconnecting
	node := m.activeNode
	m.mu.Unlock()
	if !connected {
		return nil
	}
	if err := m.stop(ctx); err != nil { // keep the kill switch engaged
		return err
	}
	return m.Connect(ctx, node)
}

// ---------- health ----------

type clashProber struct {
	c       *clashapi.Client
	url     string
	timeout time.Duration
}

func (p clashProber) Through(ctx context.Context, tag string) (time.Duration, error) {
	return p.c.Delay(ctx, tag, p.url, p.timeout)
}

func (m *Manager) healthLoop(ctx context.Context, p *profile.Profile, nodeID string) {
	var node *profile.Node
	for i := range p.Nodes {
		if p.Nodes[i].ID == nodeID {
			node = &p.Nodes[i]
		}
	}
	if node == nil {
		return
	}
	interval := time.Duration(p.Fallback.ProbeIntervalSec) * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	auto := newAutoSwitcher(p.Fallback)
	allDown := 0
	for {
		m.mu.Lock()
		clash := m.clash
		m.mu.Unlock()
		if clash != nil {
			// Probe all transports in parallel: a blackholed transport must
			// not delay the others' health (and the auto-switch decision).
			m.mu.Lock()
			sp := m.probe
			m.mu.Unlock()
			var pr transport.Prober = clashProber{c: clash, url: p.Fallback.ProbeURL, timeout: 6 * time.Second}
			if sp != nil {
				spc := *sp
				spc.url = p.Fallback.ProbeURL
				pr = spc
			}
			var wg sync.WaitGroup
			for _, tr := range m.trans {
				if tr.Configured(node) {
					wg.Add(1)
					go func(tr transport.Transport) {
						defer wg.Done()
						pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
						defer cancel()
						tr.Probe(pctx, node, pr)
					}(tr)
				}
			}
			wg.Wait()
			// Every transport failing repeatedly usually means the node's
			// parameters changed (keys rotated, ports moved): fetch the new
			// profile right away.
			down, seen := true, false
			for _, tr := range m.trans {
				if tr.Configured(node) {
					seen = true
					if h := tr.Health(); h.Reachable || h.Samples == 0 {
						down = false
					}
				}
			}
			if seen && down {
				allDown++
			} else {
				allDown = 0
			}
			if allDown >= max(2, p.Fallback.FailThreshold) {
				allDown = 0
				m.nudgeRefresh()
			}
			if p.Fallback.AutoSwitch && m.store.LoadSettings().AutoSwitch {
				actx, cancel := context.WithTimeout(ctx, 5*time.Second)
				auto.step(actx, m, clash, node, m.opt.Now())
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ---------- status ----------

// TransportStatus is per-transport UI data.
type TransportStatus struct {
	ID           profile.TransportID    `json:"id"`
	Configured   bool                   `json:"configured"`
	Enabled      bool                   `json:"enabled"`
	Capabilities transport.Capabilities `json:"capabilities"`
	Health       transport.Health       `json:"health"`
}

// Automatic split tunnelling states (Status.BypassRU).
const (
	BypassOn         = "on"
	BypassOff        = "off"
	BypassViaTunnelK = "tunnel_kill_switch" // macOS + kill switch: pf cannot let only the core's direct traffic out
)

// bypassZones returns the zones to send around the VPN.
func bypassZones(s state.Settings, goos string) []string {
	if bypassState(s, goos) != BypassOn {
		return nil
	}
	return singbox.RUZones
}

func bypassState(s state.Settings, goos string) string {
	switch {
	case !s.BypassRUEnabled():
		return BypassOff
	case s.KillSwitch && goos == "darwin":
		// pf cannot tell the core's direct traffic from any other app's,
		// so under the kill switch these sites go through the tunnel
		// instead of failing.
		return BypassViaTunnelK
	}
	return BypassOn
}

// Status is the UI snapshot.
type Status struct {
	State       ConnState         `json:"state"`
	Error       string            `json:"error,omitempty"`
	NodeID      string            `json:"node_id,omitempty"`
	CoreVersion string            `json:"core_version,omitempty"`
	ActiveTCP   string            `json:"active_tcp,omitempty"`
	ActiveUDP   string            `json:"active_udp,omitempty"`
	UDPAvail    bool              `json:"udp_available"`
	Transports  []TransportStatus `json:"transports"`
	LoggedIn    bool              `json:"logged_in"`
	HasProfile  bool              `json:"has_profile"`
	// KillSwitchActive: traffic outside the tunnel is blocked right now
	// (also while disconnected after a crash, until Disconnect).
	KillSwitchActive bool `json:"kill_switch_active"`
	AutoSwitch       bool `json:"auto_switch"`
	NaiveExternal    bool `json:"naive_external"`
	// BypassRU: on | off | tunnel_kill_switch (see bypassState).
	BypassRU string `json:"bypass_ru"`
}

// Status returns the current snapshot (queries selectors when connected).
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	st := Status{State: m.state, Error: m.lastErr, NodeID: m.activeNode, CoreVersion: m.coreVer, HasProfile: m.prof != nil}
	clash, p := m.clash, m.prof
	m.mu.Unlock()
	_, err := m.store.LoadTokens()
	st.LoggedIn = err == nil
	settings := m.store.LoadSettings()
	st.AutoSwitch = settings.AutoSwitch && (p == nil || p.Fallback.AutoSwitch)
	st.NaiveExternal = m.opt.NaiveMode == NaiveExternal
	st.BypassRU = bypassState(settings, runtime.GOOS)
	m.mu.Lock()
	st.KillSwitchActive = m.ksOn
	m.mu.Unlock()
	if !st.KillSwitchActive && m.opt.KillSwitch != nil {
		st.KillSwitchActive, _ = m.opt.KillSwitch.Active()
	}
	var node *profile.Node
	if p != nil {
		id := st.NodeID
		if id == "" {
			id = settings.NodeID
		}
		for i := range p.Nodes {
			if p.Nodes[i].ID == id || (id == "" && i == 0) {
				node = &p.Nodes[i]
				break
			}
		}
	}
	for _, id := range profile.AllTransports {
		tr := m.trans[id]
		ts := TransportStatus{ID: id, Capabilities: tr.Capabilities(), Health: tr.Health(), Enabled: !settings.DisabledTransport[string(id)]}
		if node != nil {
			ts.Configured = tr.Configured(node)
		}
		st.Transports = append(st.Transports, ts)
	}
	if clash != nil && st.State == Connected {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if g, err := clash.Group(qctx, singbox.TagSelectorTCP); err == nil {
			st.ActiveTCP = tagToID(g.Now)
		}
		if g, err := clash.Group(qctx, singbox.TagSelectorUDP); err == nil {
			st.ActiveUDP = tagToID(g.Now)
			st.UDPAvail = true
		}
	}
	return st
}

func tagToID(tag string) string {
	for _, id := range profile.AllTransports {
		if singbox.TagFor(id) == tag {
			return string(id)
		}
	}
	return tag
}

// ProfileSummary is the profile without credentials (safe for the UI).
type ProfileSummary struct {
	Subscription profile.Subscription `json:"subscription"`
	Nodes        []NodeSummary        `json:"nodes"`
	ExpiresAt    int64                `json:"expires_at"`
}

// NodeSummary omits all secrets.
type NodeSummary struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Region     string                `json:"region"`
	Transports []profile.TransportID `json:"transports"`
}

// Profile returns a credential-free summary.
func (m *Manager) Profile() (*ProfileSummary, error) {
	m.mu.Lock()
	p := m.prof
	m.mu.Unlock()
	if p == nil {
		if err := m.LoadCachedProfile(); err != nil {
			return nil, ErrNoProfile
		}
		m.mu.Lock()
		p = m.prof
		m.mu.Unlock()
	}
	out := &ProfileSummary{Subscription: p.Subscription, ExpiresAt: p.ExpiresAt}
	for _, n := range p.Nodes {
		ns := NodeSummary{ID: n.ID, Name: n.Name, Region: n.Region}
		for _, tr := range transport.All() {
			if tr.Configured(&n) {
				ns.Transports = append(ns.Transports, tr.ID())
			}
		}
		out.Nodes = append(out.Nodes, ns)
	}
	return out, nil
}

// AccountNumber returns the locally stored account number.
func (m *Manager) AccountNumber() (string, error) { return m.store.AccountNumber() }

// Settings returns user settings.
func (m *Manager) Settings() state.Settings { return m.store.LoadSettings() }

// UpdateSettings stores user settings, applying kill switch changes
// immediately and reconnecting when the core config changes.
func (m *Manager) UpdateSettings(ctx context.Context, in state.Settings) error {
	if err := state.ValidateSplitRules(in.SplitRules); err != nil {
		return err
	}
	if in.KillSwitch && m.opt.KillSwitch == nil {
		return ErrNoKillSwitch
	}
	cur := m.store.LoadSettings()
	if in.BypassRU == nil { // older UIs do not send it: keep the current value
		in.BypassRU = cur.BypassRU
	}
	if in.DNS == nil {
		in.DNS = cur.DNS
	}
	if in.AutoConnect == nil {
		in.AutoConnect = cur.AutoConnect
	}
	crashOptIn := in.CrashReports && !cur.CrashReports
	if d := in.DNSOrDefault(); d.Provider != "" {
		if err := (singbox.DNSChoice{Provider: d.Provider, CustomURL: d.CustomURL}).Validate(); err != nil {
			return fmt.Errorf("%w: %w", state.ErrInvalidSettings, err)
		}
		if d.Provider != singbox.DNSCustom {
			d.CustomURL = ""
		}
		in.DNS = &d
	}
	before := bypassState(cur, runtime.GOOS)
	needReconnect := cur.AllowLAN != in.AllowLAN || !splitEqual(cur.SplitRules, in.SplitRules) ||
		(in.KillSwitch && !cur.KillSwitch) || // engage with fresh addresses
		before != bypassState(in, runtime.GOOS) || cur.DNSOrDefault() != in.DNSOrDefault()
	ksOff := cur.KillSwitch && !in.KillSwitch
	if in.AutoSwitch && !cur.AutoSwitch {
		cur.ManualTCP, cur.ManualUDP = "", ""
	}
	cur.ClearLogsOnExit, cur.AllowLAN, cur.Language, cur.CrashReports = in.ClearLogsOnExit, in.AllowLAN, in.Language, in.CrashReports
	cur.KillSwitch, cur.AutoSwitch, cur.SplitRules = in.KillSwitch, in.AutoSwitch, append([]state.SplitRule(nil), in.SplitRules...)
	cur.BypassRU, cur.DNS, cur.AutoConnect = in.BypassRU, in.DNS, in.AutoConnect
	if err := m.store.SaveSettings(cur); err != nil {
		return err
	}
	if crashOptIn {
		m.flushCrashesAsync()
	}
	if ksOff {
		if err := m.releaseKillSwitch(); err != nil {
			return err
		}
	}
	if needReconnect {
		return m.reconnectIfConnected(ctx)
	}
	return nil
}

func splitEqual(a, b []state.SplitRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
