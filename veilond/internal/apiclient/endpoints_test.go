package apiclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
)

type seen struct {
	method, path, auth string
	body               map[string]any
}

func endpointServer(t *testing.T) (*Client, *[]seen) {
	t.Helper()
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{method: r.Method, path: r.URL.RequestURI(), auth: r.Header.Get("Authorization")}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &s.body)
		got = append(got, s)
		switch {
		case r.URL.Path == "/v1/auth/challenge":
			_ = json.NewEncoder(w).Encode(apitypes.ChallengeResponse{Nonce: "n1"})
		case r.URL.Path == "/v1/releases/latest" && r.URL.Query().Get("platform") == "none":
			w.WriteHeader(204)
		case r.URL.Path == "/v1/releases/latest", r.URL.Path == "/v1/keyset", r.URL.Path == "/v1/profile":
			_, _ = io.WriteString(w, `{"payload":{"a":1},"purpose":"p","canon":"jcs","sig_alg":"ed25519","key_id":"k","sig":"s"}`)
		case r.URL.Path == "/v1/billing/offers":
			_, _ = io.WriteString(w, `[{"plan_id":"pro"}]`)
		case r.URL.Path == "/v1/fail":
			w.WriteHeader(502)
			_, _ = io.WriteString(w, "<html>bad gateway</html>")
		default:
			_, _ = io.WriteString(w, `{"ok":true,"account_number":"1234","access_token":"A"}`)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURLs: []string{srv.URL}, AllowUnpinned: true, Timeout: 5 * time.Second, UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return c, &got
}

func TestAllEndpoints(t *testing.T) {
	c, got := endpointServer(t)
	ctx := context.Background()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	last := func() seen { return (*got)[len(*got)-1] }

	if tok, err := c.Register(ctx, key, "pc"); err != nil || tok.AccountNumber != "1234" || last().path != "/v1/auth/register" {
		t.Fatalf("register %+v %v %+v", tok, err, last())
	}
	if _, err := c.Login(ctx, "1234", key, "pc"); err != nil || last().body["account_number"] != "1234" {
		t.Fatalf("login %v", err)
	}
	if _, err := c.Refresh(ctx, "R", key); err != nil || last().body["refresh_token"] != "R" || last().body["signature"] == nil {
		t.Fatalf("refresh %v %+v", err, last())
	}
	if err := c.Logout(ctx, "A", "R"); err != nil || last().auth != "Bearer A" {
		t.Fatalf("logout %v", err)
	}
	if env, err := c.Keyset(ctx); err != nil || len(env.Payload) == 0 {
		t.Fatalf("keyset %v", err)
	}
	if env, err := c.Profile(ctx, "A"); err != nil || env.KeyID != "k" {
		t.Fatalf("profile %v", err)
	}
	if raw, err := c.ExportAccount(ctx, "A"); err != nil || !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("export returned %q, %v", raw, err)
	}
	if err := c.DeleteAccount(ctx, "A"); err != nil || last().method != "DELETE" || last().body["confirm"] != "DELETE" {
		t.Fatalf("delete %v %+v", err, last())
	}
	if env, err := c.LatestRelease(ctx, "linux-amd64", "stable"); err != nil || env == nil || last().path != "/v1/releases/latest?platform=linux-amd64&channel=stable" {
		t.Fatalf("release %v %v", env, err)
	}
	if env, err := c.LatestRelease(ctx, "none", "beta"); err != nil || env != nil {
		t.Fatalf("no release: %v %v", env, err)
	}
	if offers, err := c.Offers(ctx); err != nil || len(offers) != 1 {
		t.Fatalf("offers %v %v", offers, err)
	}
	if _, err := c.Checkout(ctx, "A", "pro", "heleket"); err != nil || last().body["provider"] != "heleket" {
		t.Fatalf("checkout %v", err)
	}
	if _, err := c.Order(ctx, "A", "a/b"); err != nil || last().path != "/v1/billing/orders/a%2Fb" {
		t.Fatalf("order %v %s", err, last().path)
	}
	if raw, err := c.SupportGet(ctx, "A", "/tickets"); err != nil || len(raw) == 0 || last().path != "/v1/support/tickets" {
		t.Fatalf("support get returned %q, %v", raw, err)
	}
	if raw, err := c.SupportPost(ctx, "A", "/tickets", map[string]string{"subject": "s"}); err != nil || len(raw) == 0 || last().body["subject"] != "s" {
		t.Fatalf("support post returned %q, %v", raw, err)
	}
	if err := c.SendCrashReport(ctx, "A", apitypes.CrashReport{ID: "x"}); err != nil || last().path != "/v1/crash-reports" {
		t.Fatalf("crash %v", err)
	}
	// A non-JSON error body becomes a generic API error.
	err := c.do(ctx, http.MethodGet, "/v1/fail", "", nil, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 502 || ae.Body.Code != apitypes.CodeInternal || !strings.Contains(ae.Error(), "502") {
		t.Fatalf("error mapping: %v", err)
	}
}

func TestOptionsAndProxy(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("no base URLs accepted")
	}
	if _, err := New(Options{BaseURLs: []string{"https://x"}, SPKIPins: []string{"short"}}); err == nil {
		t.Fatal("bad pin accepted")
	}
	r := httptest.NewRequest("GET", "/", nil)
	if u, err := proxyFromContext(r); u != nil || err != nil {
		t.Fatal("no proxy by default")
	}
	good, _ := url.Parse("socks5://127.0.0.1:1080")
	if u, err := proxyFromContext(r.WithContext(WithProxy(r.Context(), good))); err != nil || u != good {
		t.Fatalf("loopback proxy: %v", err)
	}
	for _, s := range []string{"http://127.0.0.1:8080", "socks5://10.0.0.1:1080"} {
		bad, _ := url.Parse(s)
		if _, err := proxyFromContext(r.WithContext(WithProxy(r.Context(), bad))); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	c, _ := New(Options{BaseURLs: []string{"http://127.0.0.1:1"}, AllowUnpinned: true, Timeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Keyset(ctx); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := c.Register(context.Background(), key, ""); err == nil {
		t.Fatal("register without a server")
	}
}
