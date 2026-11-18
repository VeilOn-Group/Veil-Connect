// Package uibridge connects the unprivileged desktop UI process to veilond
// (ADR-0047). The Wails AssetServer mounts Middleware; the web frontend calls
// fetch("/veilond/v1/...") and the bridge forwards to veilond's unix socket.
//
// The asset server is in-process (no network listener), so only the app's
// own WebView can reach the bridge. The bridge strips browser headers before
// forwarding (veilond refuses requests carrying Origin) and only forwards an
// allowlist of local API paths.
package uibridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/veilon/veilon-app/veilond/pkg/peercred"
)

// Prefix is the path prefix the frontend uses.
const Prefix = "/veilond/"

// allowed lists forwardable local API routes (method + path prefix).
var allowed = []struct{ method, path string }{
	{"GET", "/v1/status"}, {"POST", "/v1/connect"}, {"POST", "/v1/disconnect"},
	{"POST", "/v1/transport/select"}, {"POST", "/v1/transport/enabled"}, {"POST", "/v1/location"},
	{"GET", "/v1/account/export"}, {"POST", "/v1/account/delete"}, {"GET", "/v1/update"}, {"POST", "/v1/update/download"},
	{"POST", "/v1/update/install"},
	{"GET", "/v1/billing/offers"}, {"POST", "/v1/billing/checkout"}, {"GET", "/v1/billing/order"},
	{"GET", "/v1/profile"}, {"POST", "/v1/profile/refresh"},
	{"POST", "/v1/account/register"}, {"POST", "/v1/account/login"}, {"POST", "/v1/account/logout"}, {"GET", "/v1/account"},
	{"GET", "/v1/settings"}, {"PUT", "/v1/settings"}, {"GET", "/v1/dns/presets"},
	{"GET", "/v1/privacy/what-we-store"}, {"GET", "/v1/logs"}, {"POST", "/v1/logs/wipe"},
}

// allowedTrees are forwarded with any sub-path; veilond validates the
// exact shape (ticket ids and actions) before relaying to the API.
var allowedTrees = []struct{ method, path string }{
	{"GET", "/v1/support/tickets"}, {"POST", "/v1/support/tickets"}, {"GET", "/v1/support/diagnostics"},
	{"GET", "/v1/crash-reports"}, {"POST", "/v1/crash-reports"},
}

func permitted(method, path string) bool {
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		return false
	}
	// Crash reports of the UI process are submitted by the UI process
	// itself (SubmitCrash), never by the web view.
	if method == http.MethodPost && path == "/v1/crash-reports" {
		return false
	}
	for _, a := range allowedTrees {
		if a.method == method && (path == a.path || strings.HasPrefix(path, a.path+"/")) {
			return true
		}
	}
	for _, a := range allowed {
		if a.method == method && (path == a.path || strings.HasPrefix(path, a.path+"?")) {
			return true
		}
	}
	return false
}

// Actions are UI-process commands exposed under /app/ (e.g. open windows).
type Actions map[string]func()

// Bridge forwards requests to veilond.
type Bridge struct {
	proxy   *httputil.ReverseProxy
	client  *http.Client
	actions Actions
	routes  map[string]http.Handler
}

// HandleApp serves path (under /app/v1/) inside the UI process, e.g. the
// login item switch, which is per user and not veilond's business.
func (b *Bridge) HandleApp(path string, h http.Handler) {
	if b.routes == nil {
		b.routes = map[string]http.Handler{}
	}
	b.routes[path] = h
}

// New creates a bridge to the unix socket at sock.
func New(sock string, actions Actions) *Bridge {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", sock)
			if err != nil {
				return nil, err
			}
			// The UI sends logins through this socket: make sure it is
			// served by the privileged veilond and not by a local user who
			// managed to bind the path (ADR-0060).
			uc, ok := c.(*net.UnixConn)
			if !ok {
				_ = c.Close()
				return nil, errors.New("uibridge: not a unix socket")
			}
			cred, err := peercred.Of(uc)
			if err == nil {
				err = peercred.VerifyServer(cred, sock)
			}
			if err != nil {
				_ = c.Close()
				return nil, err
			}
			return c, nil
		},
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          4,
	}
	target, _ := url.Parse("http://veilond")
	rp := &httputil.ReverseProxy{
		Transport: tr,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, strings.TrimSuffix(Prefix, "/"))
			pr.Out.Host = "veilond"
			for _, h := range []string{"Origin", "Referer", "Cookie", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User", "User-Agent"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Del("X-Forwarded-For")
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"veilond_unavailable","message":"VeilOn service is not running"}`))
		},
	}
	return &Bridge{proxy: rp, client: &http.Client{Transport: tr, Timeout: 10 * time.Second}, actions: actions}
}

// ServeHTTP handles /veilond/* and /app/*.
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case b.routes[r.URL.Path] != nil:
		b.routes[r.URL.Path].ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/app/"):
		name := strings.TrimPrefix(r.URL.Path, "/app/")
		act, ok := b.actions[name]
		if r.Method != http.MethodPost || !ok {
			http.NotFound(w, r)
			return
		}
		act()
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, Prefix):
		p := strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(Prefix, "/"))
		if !permitted(r.Method, p) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		b.proxy.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// GetJSON reads a veilond endpoint from the UI process itself (not on
// behalf of the web view), e.g. the payment page address to open in the
// system browser. path need not be in the web allowlist.
func (b *Bridge) GetJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://veilond"+path, nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("uibridge: %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out)
}

// Middleware adapts the bridge to Wails' AssetOptions.Middleware signature
// (func(next http.Handler) http.Handler: verified in wails v3.0.0-beta.34).
func (b *Bridge) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, Prefix) || strings.HasPrefix(r.URL.Path, "/app/") {
			b.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PaymentURLAllowed is the UI-process copy of veilond's payment page check
// (https, provider hosts or their subdomains, no credentials or port).
// Kept here because the desktop module cannot import veilond internals.
func PaymentURLAllowed(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil || p.Port() != "" || p.Opaque != "" {
		return false
	}
	h := strings.ToLower(p.Hostname())
	for _, a := range []string{"yoomoney.ru", "yookassa.ru", "pay.heleket.com"} {
		if h == a || strings.HasSuffix(h, "."+a) {
			return true
		}
	}
	return false
}
