// Package localapi is the UI <-> veilond control channel:
// HTTP/JSON over a unix socket (trusted directory, 0660 or a protected DACL,
// and a kernel peer-credential check on every connection) or, in
// dev builds only, 127.0.0.1 TCP with a bearer token and Host check. Requests that
// carry an Origin header are refused: browsers must never reach veilond
// (CSRF / DNS-rebinding protection).
package localapi

import (
	"github.com/veilon/veil-shared/pkg/singbox"

	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/veilon/veil-shared/pkg/inventory"
	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veilon-app/veilond/internal/core"
	"github.com/veilon/veilon-app/veilond/internal/logring"
	"github.com/veilon/veilon-app/veilond/internal/state"
)

// Server serves the local API.
type Server struct {
	m        *core.Manager
	ring     *logring.Ring
	log      *slog.Logger
	token    string // TCP fallback only
	tcpHosts map[string]bool
	summary  *inventory.PublicSummary
	diag     diagCache
}

// New creates a server.
func New(m *core.Manager, ring *logring.Ring, log *slog.Logger) (*Server, error) {
	inv, err := inventory.Embedded()
	if err != nil {
		return nil, err
	}
	return &Server{m: m, ring: ring, log: log, summary: &inv.PublicSummary, tcpHosts: map[string]bool{}}, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("POST /v1/connect", s.connect)
	mux.HandleFunc("POST /v1/disconnect", s.disconnect)
	mux.HandleFunc("POST /v1/transport/select", s.selectTransport)
	mux.HandleFunc("POST /v1/location", s.location)
	mux.HandleFunc("GET /v1/account/export", s.exportAccount)
	mux.HandleFunc("POST /v1/account/delete", s.deleteAccount)
	mux.HandleFunc("GET /v1/update", s.checkUpdate)
	mux.HandleFunc("POST /v1/update/download", s.downloadUpdate)
	mux.HandleFunc("POST /v1/update/install", s.installUpdate)
	mux.HandleFunc("POST /v1/transport/enabled", s.setEnabled)
	mux.HandleFunc("GET /v1/profile", s.profile)
	mux.HandleFunc("POST /v1/profile/refresh", s.refresh)
	mux.HandleFunc("POST /v1/account/register", s.register)
	mux.HandleFunc("POST /v1/account/login", s.login)
	mux.HandleFunc("POST /v1/account/logout", s.logout)
	mux.HandleFunc("GET /v1/account", s.account)
	mux.HandleFunc("GET /v1/settings", s.getSettings)
	mux.HandleFunc("GET /v1/dns/presets", s.dnsPresets)
	mux.HandleFunc("PUT /v1/settings", s.putSettings)
	mux.HandleFunc("GET /v1/privacy/what-we-store", s.whatWeStore)
	mux.HandleFunc("GET /v1/logs", s.logs)
	mux.HandleFunc("POST /v1/logs/wipe", s.wipeLogs)
	s.supportRoutes(mux)
	s.crashRoutes(mux)
	mux.HandleFunc("GET /v1/billing/offers", s.offers)
	mux.HandleFunc("POST /v1/billing/checkout", s.checkout)
	mux.HandleFunc("GET /v1/billing/order", s.currentOrder)
	mux.HandleFunc("GET /v1/billing/payment-url", s.paymentURL)
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			writeErr(w, http.StatusForbidden, "forbidden", "browser requests are not allowed")
			return
		}
		if viaTCP(r) {
			if !s.tcpHosts[r.Host] {
				writeErr(w, http.StatusForbidden, "forbidden", "bad host")
				return
			}
			got := r.Header.Get("X-Veilon-Token")
			if s.token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "token required")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		next.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

func viaTCP(r *http.Request) bool { v, _ := r.Context().Value(ctxKey{}).(bool); return v }

// ServeTCP serves the fallback channel; the token is written to tokenFile
// (0600). Dev builds only (config validation): any local process can reach
// a loopback port, so the token file permissions would be the only gate.
func (s *Server) ServeTCP(ctx context.Context, addr, tokenFile string) error {
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	s.token = hex.EncodeToString(b)
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	s.tcpHosts["127.0.0.1:"+port] = true
	s.tcpHosts["localhost:"+port] = true
	if err := os.WriteFile(tokenFile, []byte(s.token+"\n"+port+"\n"), 0o600); err != nil {
		return err
	}
	return s.serve(ctx, l, true)
}

func (s *Server) serve(ctx context.Context, l net.Listener, tcp bool) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.WithValue(context.Background(), ctxKey{}, tcp) },
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---- handlers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, c, msg string) {
	writeJSON(w, code, map[string]string{"code": c, "message": msg})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrNoCheckout):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, core.ErrBadCheckoutArg), errors.Is(err, core.ErrPaymentURL):
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, core.ErrNoProfile), errors.Is(err, core.ErrNotLoggedIn):
		writeErr(w, http.StatusPreconditionFailed, "precondition", err.Error())
	case errors.Is(err, core.ErrUDPToNaive), errors.Is(err, core.ErrUnknownGroup), errors.Is(err, core.ErrTransportAbsent),
		errors.Is(err, core.ErrUnknownNode), errors.Is(err, core.ErrNoKillSwitch), errors.Is(err, state.ErrInvalidSettings):
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, core.ErrAlreadyRunning), errors.Is(err, core.ErrNotConnected), errors.Is(err, core.ErrNoSilentInstall):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, core.ErrClientTooOld):
		writeErr(w, http.StatusUpgradeRequired, "upgrade_required", err.Error())
	default:
		s.log.Warn("local api error", "err", err)
		writeErr(w, http.StatusBadGateway, "error", err.Error())
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.m.Status(r.Context()))
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NodeID string `json:"node_id"`
	}
	if r.ContentLength > 0 {
		if err := decode(r, &in); err != nil {
			writeErr(w, 400, "bad_request", "invalid body")
			return
		}
	}
	if err := s.m.Connect(r.Context(), in.NodeID); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.m.Status(r.Context()))
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.m.Disconnect(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.m.Status(r.Context()))
}

func (s *Server) exportAccount(w http.ResponseWriter, r *http.Request) {
	raw, err := s.m.ExportAccount(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &in); err != nil || in.Confirm != "DELETE" {
		writeErr(w, 400, "bad_request", `send {"confirm":"DELETE"}`)
		return
	}
	if err := s.m.DeleteAccount(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	ch := r.URL.Query().Get("channel")
	if ch != "" && ch != "stable" && ch != "beta" {
		writeErr(w, 400, "bad_request", "channel")
		return
	}
	info, err := s.m.CheckUpdate(r.Context(), ch)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, info)
}

func (s *Server) downloadUpdate(w http.ResponseWriter, r *http.Request) {
	info, err := s.m.DownloadUpdate(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, info)
}

// installUpdate hands the downloaded update to the privileged helper.
// It takes no input: only the update veilond itself verified
// can be submitted.
func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	info, err := s.m.InstallUpdate(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 202, info)
}

// location switches the node (reconnects without releasing the kill switch
// when connected; otherwise only remembers the choice).
func (s *Server) location(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NodeID string `json:"node_id"`
	}
	if err := decode(r, &in); err != nil || in.NodeID == "" {
		writeErr(w, 400, "bad_request", "invalid body")
		return
	}
	if err := s.m.SwitchNode(r.Context(), in.NodeID); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.m.Status(r.Context()))
}

func (s *Server) selectTransport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Group     string `json:"group"`
		Transport string `json:"transport"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body")
		return
	}
	if err := s.m.SelectTransport(r.Context(), in.Group, profile.TransportID(in.Transport)); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.m.Status(r.Context()))
}

func (s *Server) setEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Transport string `json:"transport"`
		Enabled   bool   `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body")
		return
	}
	if err := s.m.SetTransportEnabled(r.Context(), profile.TransportID(in.Transport), in.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.m.Status(r.Context()))
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	p, err := s.m.Profile()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	// Refresh and, if the active location changed, reconnect.
	if _, err := s.m.RefreshAndApply(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	s.profile(w, r)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	n, err := s.m.Register(r.Context())
	if err != nil && n == "" {
		s.fail(w, err)
		return
	}
	// The number is returned once for the UI to show "save this number".
	resp := map[string]any{"account_number": n}
	if err != nil {
		resp["profile_error"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountNumber string `json:"account_number"`
	}
	if err := decode(r, &in); err != nil || in.AccountNumber == "" {
		writeErr(w, 400, "bad_request", "account_number required")
		return
	}
	if err := s.m.Login(r.Context(), in.AccountNumber); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.m.Logout(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	n, err := s.m.AccountNumber()
	if err != nil {
		writeJSON(w, 200, map[string]any{"logged_in": false})
		return
	}
	// Masked by default; the UI reveals the full number on explicit request.
	reveal := r.URL.Query().Get("reveal") == "1"
	shown := n
	if !reveal && len(n) > 4 {
		shown = "•••• •••• •••• " + n[len(n)-4:]
	}
	writeJSON(w, 200, map[string]any{"logged_in": true, "account_number": shown})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.m.Settings())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in state.Settings
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body")
		return
	}
	if err := s.m.UpdateSettings(r.Context(), in); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, s.m.Settings())
}

func (s *Server) whatWeStore(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.summary) }

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 1000 {
		n = 200
	}
	lines, err := s.ring.Tail(n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"lines": lines})
}

func (s *Server) wipeLogs(w http.ResponseWriter, r *http.Request) {
	if err := s.ring.Wipe(); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) offers(w http.ResponseWriter, r *http.Request) {
	o, err := s.m.Offers(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, o)
}

func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PlanID   string `json:"plan_id"`
		Provider string `json:"provider"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "bad_request", "invalid body")
		return
	}
	co, err := s.m.Checkout(r.Context(), in.PlanID, in.Provider)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 201, co)
}

func (s *Server) currentOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.m.CurrentOrder(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, o)
}

// paymentURL is read by the desktop UI process (not its web view) to open
// the provider page in the system browser.
func (s *Server) paymentURL(w http.ResponseWriter, _ *http.Request) {
	u, err := s.m.PaymentURL()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": u})
}

// dnsPresets lists the DoH providers the settings screen offers.
func (s *Server) dnsPresets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"default": singbox.DNSDefault, "presets": singbox.DoHPresets})
}
