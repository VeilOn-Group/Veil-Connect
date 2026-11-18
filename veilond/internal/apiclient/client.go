// Package apiclient talks to veilon-api with SPKI pinning and endpoint
// rotation. It never sends device or usage identifiers beyond
// what each endpoint requires.
package apiclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veil-shared/pkg/sign"
)

// Options configure the client.
type Options struct {
	BaseURLs      []string
	SPKIPins      []string // base64(SHA-256(SubjectPublicKeyInfo))
	AllowUnpinned bool     // dev only (validated by config)
	Timeout       time.Duration
	UserAgent     string // generic, no version or OS details
}

// Client is safe for concurrent use.
type Client struct {
	opts Options
	hc   *http.Client
	mu   sync.Mutex
	pref int // index of the last working base URL
}

type proxyKey struct{}

// WithProxy returns a context whose API requests go through proxy (only a
// loopback SOCKS5 URL is honoured; the SPKI pin still applies end to end).
func WithProxy(ctx context.Context, proxy *url.URL) context.Context {
	return context.WithValue(ctx, proxyKey{}, proxy)
}

func proxyFromContext(r *http.Request) (*url.URL, error) {
	u, _ := r.Context().Value(proxyKey{}).(*url.URL)
	if u == nil {
		return nil, nil
	}
	if u.Scheme != "socks5" || !strings.HasPrefix(u.Host, "127.0.0.1:") {
		return nil, errors.New("apiclient: only a loopback socks5 proxy is allowed")
	}
	return u, nil
}

// ErrPinMismatch indicates a possible MITM.
var ErrPinMismatch = errors.New("apiclient: server key does not match pinned SPKI")

// New creates a client.
func New(o Options) (*Client, error) {
	if len(o.BaseURLs) == 0 {
		return nil, errors.New("apiclient: no base URLs")
	}
	pins := make([][]byte, 0, len(o.SPKIPins))
	for _, p := range o.SPKIPins {
		b, err := base64.StdEncoding.DecodeString(p)
		if err != nil || len(b) != sha256.Size {
			return nil, fmt.Errorf("apiclient: bad SPKI pin %q", p)
		}
		pins = append(pins, b)
	}
	if len(pins) == 0 && !o.AllowUnpinned {
		return nil, errors.New("apiclient: SPKI pins required")
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.UserAgent == "" {
		o.UserAgent = "VeilOn"
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(pins) > 0 {
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			for _, cert := range cs.PeerCertificates {
				sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
				for _, p := range pins {
					if subtle.ConstantTimeCompare(sum[:], p) == 1 {
						return nil
					}
				}
			}
			return ErrPinMismatch
		}
	}
	tr := &http.Transport{
		// Direct by default: the API must be reachable when the VPN is off;
		// when the VPN is on, the TUN routes it through the tunnel. A request
		// context may carry a local SOCKS proxy (WithProxy): veilond uses the
		// core's authenticated "direct" probe user when the tunnel is down.
		Proxy:               proxyFromContext,
		TLSClientConfig:     tlsCfg,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        4,
	}
	return &Client{opts: o, hc: &http.Client{Transport: tr, Timeout: o.Timeout}}, nil
}

// SPKIPin computes the pin of a certificate (tooling and tests).
func SPKIPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// APIError is a structured API error.
type APIError struct {
	Status int
	Body   apitypes.Error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api %d %s: %s", e.Status, e.Body.Code, e.Body.Message)
}

// do tries base URLs starting with the last good one. Only network/TLS-level
// failures rotate; HTTP errors are returned as-is.
func (c *Client) do(ctx context.Context, method, path, bearer string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	c.mu.Lock()
	start := c.pref
	c.mu.Unlock()
	var lastErr error
	n := len(c.opts.BaseURLs)
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		err := c.once(ctx, c.opts.BaseURLs[idx], method, path, bearer, body, out)
		if err == nil {
			c.mu.Lock()
			c.pref = idx
			c.mu.Unlock()
			return nil
		}
		// An HTTP-level error came from our API: do not rotate. A pin
		// mismatch rotates (another endpoint may be reachable without MITM)
		// but is surfaced if every endpoint fails.
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lastErr = err
	}
	return fmt.Errorf("apiclient: all endpoints failed: %w", lastErr)
}

func (c *Client) once(ctx context.Context, base, method, path, bearer string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		e := &APIError{Status: resp.StatusCode}
		if json.Unmarshal(data, &e.Body) != nil || e.Body.Code == "" {
			e.Body.Code, e.Body.Message = apitypes.CodeInternal, http.StatusText(resp.StatusCode)
		}
		return e
	}
	if out != nil && len(data) > 0 {
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], data...)
			return nil
		}
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) proof(ctx context.Context, key ed25519.PrivateKey) (apitypes.DeviceProof, error) {
	var ch apitypes.ChallengeResponse
	if err := c.do(ctx, http.MethodPost, "/v1/auth/challenge", "", struct{}{}, &ch); err != nil {
		return apitypes.DeviceProof{}, err
	}
	sig := ed25519.Sign(key, apitypes.DeviceAuthMessage(ch.Nonce))
	return apitypes.DeviceProof{
		DevicePublicKey: apitypes.EncodeKey(key.Public().(ed25519.PublicKey)),
		Nonce:           ch.Nonce,
		Signature:       apitypes.EncodeKey(sig),
	}, nil
}

// Register creates an anonymous account; the response carries the account
// number exactly once.
func (c *Client) Register(ctx context.Context, key ed25519.PrivateKey, label string) (*apitypes.TokenResponse, error) {
	p, err := c.proof(ctx, key)
	if err != nil {
		return nil, err
	}
	var out apitypes.TokenResponse
	return &out, c.do(ctx, http.MethodPost, "/v1/auth/register", "", apitypes.RegisterRequest{DeviceProof: p, DeviceLabel: label}, &out)
}

// Login with an account number.
func (c *Client) Login(ctx context.Context, number string, key ed25519.PrivateKey, label string) (*apitypes.TokenResponse, error) {
	p, err := c.proof(ctx, key)
	if err != nil {
		return nil, err
	}
	var out apitypes.TokenResponse
	return &out, c.do(ctx, http.MethodPost, "/v1/auth/login", "", apitypes.LoginRequest{AccountNumber: number, DeviceProof: p, DeviceLabel: label}, &out)
}

// Refresh rotates tokens.
func (c *Client) Refresh(ctx context.Context, refresh string, key ed25519.PrivateKey) (*apitypes.TokenResponse, error) {
	p, err := c.proof(ctx, key)
	if err != nil {
		return nil, err
	}
	var out apitypes.TokenResponse
	return &out, c.do(ctx, http.MethodPost, "/v1/auth/refresh", "", apitypes.RefreshRequest{RefreshToken: refresh, DeviceProof: p}, &out)
}

// Logout revokes the refresh token server-side.
func (c *Client) Logout(ctx context.Context, access, refresh string) error {
	return c.do(ctx, http.MethodPost, "/v1/auth/logout", access, map[string]string{"refresh_token": refresh}, nil)
}

// Me returns the account summary.
func (c *Client) Me(ctx context.Context, access string) (*apitypes.MeResponse, error) {
	var out apitypes.MeResponse
	return &out, c.do(ctx, http.MethodGet, "/v1/me", access, nil, &out)
}

// Keyset fetches the signed keyset (public, no auth).
func (c *Client) Keyset(ctx context.Context) (*sign.Envelope, error) {
	var env sign.Envelope
	return &env, c.do(ctx, http.MethodGet, "/v1/keyset", "", nil, &env)
}

// Profile fetches the signed profile.
func (c *Client) Profile(ctx context.Context, access string) (*sign.Envelope, error) {
	var env sign.Envelope
	return &env, c.do(ctx, http.MethodGet, "/v1/profile", access, nil, &env)
}

// ExportAccount returns everything the server stores about the account
// (raw JSON, shown to the user as is).
func (c *Client) ExportAccount(ctx context.Context, access string) (json.RawMessage, error) {
	var out json.RawMessage
	return out, c.do(ctx, http.MethodGet, "/v1/account/export", access, nil, &out)
}

// DeleteAccount erases the account on the server.
func (c *Client) DeleteAccount(ctx context.Context, access string) error {
	return c.do(ctx, http.MethodDelete, "/v1/account", access, map[string]string{"confirm": "DELETE"}, nil)
}

// LatestRelease fetches signed release metadata; nil when none is published.
func (c *Client) LatestRelease(ctx context.Context, platform, channel string) (*sign.Envelope, error) {
	var env sign.Envelope
	if err := c.do(ctx, http.MethodGet, "/v1/releases/latest?platform="+url.QueryEscape(platform)+"&channel="+url.QueryEscape(channel), "", nil, &env); err != nil {
		return nil, err
	}
	if len(env.Payload) == 0 {
		return nil, nil
	}
	return &env, nil
}

// Offers lists the plans for sale with their prices per provider.
func (c *Client) Offers(ctx context.Context) ([]apitypes.BillingOffer, error) {
	var out []apitypes.BillingOffer
	return out, c.do(ctx, http.MethodGet, "/v1/billing/offers", "", nil, &out)
}

// Checkout starts a payment of a plan through a provider.
func (c *Client) Checkout(ctx context.Context, access, planID, provider string) (*apitypes.CheckoutResponse, error) {
	var out apitypes.CheckoutResponse
	return &out, c.do(ctx, http.MethodPost, "/v1/billing/checkout", access, apitypes.CheckoutRequest{PlanID: planID, Provider: provider}, &out)
}

// Order returns the state of one of the account's orders.
func (c *Client) Order(ctx context.Context, access, id string) (*apitypes.BillingOrder, error) {
	var out apitypes.BillingOrder
	return &out, c.do(ctx, http.MethodGet, "/v1/billing/orders/"+url.PathEscape(id), access, nil, &out)
}

// Support. Results are passed to the UI as raw JSON: the
// daemon does not interpret or store conversations.

// SupportGet performs a GET under /v1/support.
func (c *Client) SupportGet(ctx context.Context, access, path string) (json.RawMessage, error) {
	var out json.RawMessage
	return out, c.do(ctx, http.MethodGet, "/v1/support"+path, access, nil, &out)
}

// SupportPost performs a JSON POST under /v1/support.
func (c *Client) SupportPost(ctx context.Context, access, path string, in any) (json.RawMessage, error) {
	var out json.RawMessage
	return out, c.do(ctx, http.MethodPost, "/v1/support"+path, access, in, &out)
}

// SendCrashReport posts a sanitized crash report. The session
// only lets the server rate-limit; the report is stored without it.
func (c *Client) SendCrashReport(ctx context.Context, access string, r apitypes.CrashReport) error {
	return c.do(ctx, http.MethodPost, "/v1/crash-reports", access, r, nil)
}
