package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/sign"
	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/state"
	"github.com/veilon/veilon-app/veilond/internal/supervisor"
)

var testNow = time.Unix(1_800_000_100, 0)

type fakeAPI struct {
	root, prof *sign.Signer
	p          profile.Profile
	refreshes  int
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	mk := func(id string, pp sign.Purpose) *sign.Signer {
		seed, _ := sign.GenerateSeed()
		s, err := sign.NewSigner(id, pp, seed)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return &fakeAPI{root: mk("root-1", sign.PurposeKeyset), prof: mk("prof-1", sign.PurposeProfile), p: profile.Fixture()}
}

func (f *fakeAPI) tokens(number string) *apitypes.TokenResponse {
	return &apitypes.TokenResponse{AccessToken: "acc", RefreshToken: "ref", ExpiresIn: 900, AccountNumber: number}
}
func (f *fakeAPI) Register(context.Context, ed25519.PrivateKey, string) (*apitypes.TokenResponse, error) {
	return f.tokens("1111222233334444"), nil
}
func (f *fakeAPI) Login(_ context.Context, n string, _ ed25519.PrivateKey, _ string) (*apitypes.TokenResponse, error) {
	if n != "1111222233334444" {
		return nil, errors.New("bad number")
	}
	return f.tokens(""), nil
}
func (f *fakeAPI) Refresh(context.Context, string, ed25519.PrivateKey) (*apitypes.TokenResponse, error) {
	f.refreshes++
	return f.tokens(""), nil
}
func (f *fakeAPI) Logout(context.Context, string, string) error { return nil }
func (f *fakeAPI) Keyset(context.Context) (*sign.Envelope, error) {
	return f.root.Sign(sign.Keyset{Version: 1, Keys: []sign.PublicKey{{ID: f.prof.KeyID, Purpose: sign.PurposeProfile, Key: f.prof.PublicKey()}}})
}
func (f *fakeAPI) Profile(context.Context, string) (*sign.Envelope, error) { return f.prof.Sign(f.p) }

// fakeCore emulates the sing-box process: reads the config written by
// BeforeStart and serves a Clash API on the configured loopback port.
type fakeCore struct {
	cfg      supervisor.Config
	mu       sync.Mutex
	selected map[string]string
	perm     os.FileMode
	seenCfg  singbox.Config
}

func (c *fakeCore) config() singbox.Config { c.mu.Lock(); defer c.mu.Unlock(); return c.seenCfg }

func (c *fakeCore) State() (supervisor.State, int, error) { return supervisor.StateRunning, 1, nil }

func (c *fakeCore) Run(ctx context.Context) error {
	if err := c.cfg.BeforeStart(); err != nil {
		return err
	}
	path := c.cfg.Args()[2]
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	raw, _ := os.ReadFile(path)
	var seen singbox.Config
	_ = json.Unmarshal(raw, &seen)
	ca := seen.Experimental.ClashAPI
	c.mu.Lock()
	c.perm = st.Mode().Perm()
	c.seenCfg = seen
	c.selected = map[string]string{singbox.TagSelectorTCP: singbox.TagVLESS, singbox.TagSelectorUDP: singbox.TagHysteria2}
	c.mu.Unlock()
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+ca.Secret {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /version", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"sing-box 1.13.21"}`))
	}))
	mux.HandleFunc("GET /proxies/{name}", auth(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		now, ok := c.selected[r.PathValue("name")]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "Selector", "now": now})
	}))
	mux.HandleFunc("PUT /proxies/{name}", auth(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.PathValue("name") == singbox.TagSelectorUDP && b["name"] == singbox.TagNaive {
			w.WriteHeader(400) // sing-box would reject: naive is not a member
			return
		}
		c.selected[r.PathValue("name")] = b["name"]
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /proxies/{name}/delay", auth(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"delay":42}`))
	}))
	l, err := net.Listen("tcp", ca.ExternalController)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	_ = srv.Serve(l)
	return nil
}

func newManager(t *testing.T, api *fakeAPI) (*Manager, *fakeCore, string) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := t.TempDir()
	core := &fakeCore{}
	m := New(Options{
		ClientVersion: "0.1.0", RuntimeDir: runtime, SingBoxPath: "/nonexistent", SingBoxSHA256: "00",
		RootKeys: []sign.PublicKey{{ID: api.root.KeyID, Purpose: sign.PurposeKeyset, Key: api.root.PublicKey()}},
		Now:      func() time.Time { return testNow },
		NewRunner: func(c supervisor.Config) (Runner, error) {
			core.cfg = c
			return core, nil
		},
		ReadyTimeout: 5 * time.Second,
		Resolve:      fakeResolve,
	}, store, api)
	return m, core, runtime
}

// fakeResolve resolves the fixture names without touching the network.
func fakeResolve(_ context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	switch host {
	case "node1.example.net":
		return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
	case "node2.example.net":
		return []netip.Addr{netip.MustParseAddr("203.0.113.20")}, nil
	}
	return nil, errors.New("nxdomain")
}

func TestRegisterConnectSwitchDisconnect(t *testing.T) {
	api := newFakeAPI(t)
	m, core, runtime := newManager(t, api)
	ctx := context.Background()

	num, err := m.Register(ctx)
	if err != nil || num != "1111222233334444" {
		t.Fatalf("register: %q %v", num, err)
	}
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatalf("connect: %v", err)
	}
	core.mu.Lock()
	perm := core.perm
	core.mu.Unlock()
	if perm != 0o600 {
		t.Fatalf("core config perms %v, want 0600", perm)
	}
	if _, err := os.Stat(runtime + "/core.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("core config with credentials must be removed after the core is ready")
	}
	seen := core.config()
	if err := singbox.AssertClientNoLog(&seen); err != nil {
		t.Fatalf("NoLog violation in runtime config: %v", err)
	}
	st := m.Status(ctx)
	if st.State != Connected || st.ActiveTCP != "vless" || st.ActiveUDP != "hysteria2" || !st.UDPAvail {
		t.Fatalf("status %+v", st)
	}
	if err := m.SelectTransport(ctx, "udp", profile.TransportNaive); !errors.Is(err, ErrUDPToNaive) {
		t.Fatalf("naive for UDP must be refused locally, got %v", err)
	}
	if err := m.SelectTransport(ctx, "tcp", profile.TransportNaive); err != nil {
		t.Fatal(err)
	}
	if m.Status(ctx).ActiveTCP != "naive" {
		t.Fatal("manual switch not applied")
	}
	if m.Settings().ManualTCP != "naive" {
		t.Fatal("manual choice not remembered")
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if s := m.Status(ctx); s.State != Disconnected {
		t.Fatalf("state %s", s.State)
	}
	// Reconnect restores the manual choice.
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if m.Status(ctx).ActiveTCP != "naive" {
		t.Fatal("manual selection not restored on reconnect")
	}
	_ = m.Disconnect(ctx)
}

func TestProfileSummaryHasNoSecrets(t *testing.T) {
	api := newFakeAPI(t)
	m, _, _ := newManager(t, api)
	if _, err := m.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	for _, secret := range []string{api.p.Nodes[0].Transports.VLESS.UUID, "fixture-hy2-password", "fixture-naive-password", "fixture-obfs"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("summary leaks %q", secret)
		}
	}
}

func TestRejectsForeignSignedProfile(t *testing.T) {
	api := newFakeAPI(t)
	m, _, _ := newManager(t, api)
	seed, _ := sign.GenerateSeed()
	api.prof, _ = sign.NewSigner("prof-1", sign.PurposeProfile, seed) // keyset now lists a different key
	other := newFakeAPI(t)
	api.root = other.root // keyset signed by an untrusted root
	if _, err := m.Register(context.Background()); err == nil {
		t.Fatal("profile/keyset from untrusted root must be rejected")
	}
	if err := m.Connect(context.Background(), ""); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("connect without verified profile must fail, got %v", err)
	}
}

func TestClientTooOld(t *testing.T) {
	api := newFakeAPI(t)
	api.p.MinClientVersion = "9.0.0"
	m, _, _ := newManager(t, api)
	if _, err := m.Register(context.Background()); !errors.Is(err, ErrClientTooOld) {
		t.Fatalf("want ErrClientTooOld, got %v", err)
	}
}

func TestTokenRefresh(t *testing.T) {
	api := newFakeAPI(t)
	m, _, _ := newManager(t, api)
	if _, err := m.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.opt.Now = func() time.Time { return testNow.Add(20 * time.Minute) } // access expired
	if err := m.RefreshProfile(context.Background()); err == nil || api.refreshes != 1 {
		// profile itself expires at +24h, so only the token refresh matters here
		if api.refreshes != 1 {
			t.Fatalf("expected one token refresh, got %d (err %v)", api.refreshes, err)
		}
	}
}
