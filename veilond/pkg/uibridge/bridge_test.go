package uibridge

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/veilon/veilon-app/veilond/pkg/autostart"
)

func fakeVeilond(t *testing.T) (string, chan *http.Request) {
	sock := filepath.Join(t.TempDir(), "v.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *http.Request, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Clone(context.Background())
		_, _ = io.WriteString(w, `{"state":"disconnected"}`)
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock, got
}

func TestForwardsAndStripsBrowserHeaders(t *testing.T) {
	sock, got := fakeVeilond(t)
	b := New(sock, nil)
	r := httptest.NewRequest("GET", "/veilond/v1/status", nil)
	r.Header.Set("Origin", "wails://wails")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("User-Agent", "WebKit")
	w := httptest.NewRecorder()
	b.Middleware(http.NotFoundHandler()).ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "disconnected") {
		t.Fatalf("proxy %d %s", w.Code, w.Body.String())
	}
	in := <-got
	if in.URL.Path != "/v1/status" || in.Header.Get("Origin") != "" || in.Header.Get("Sec-Fetch-Site") != "" || in.Header.Get("User-Agent") != "" {
		t.Fatalf("forwarded request not sanitized: path=%s headers=%v", in.URL.Path, in.Header)
	}
}

func TestAllowlistAndActions(t *testing.T) {
	sock, _ := fakeVeilond(t)
	opened := false
	b := New(sock, Actions{"open-support": func() { opened = true }})
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/veilond/v1/connections", 403},
		{"DELETE", "/veilond/v1/status", 403},
		{"GET", "/veilond/../etc/passwd", 403},
		// The payment page address is read by the UI process only.
		{"GET", "/veilond/v1/billing/payment-url", 403},
		{"POST", "/veilond/v1/support/tickets/../../account/delete", 403},
		{"DELETE", "/veilond/v1/support/tickets/x", 403},
		{"GET", "/app/open-support", 404},
		{"POST", "/app/unknown", 404},
		{"POST", "/app/open-support", 204},
		{"GET", "/veilond/v1/crash-reports", 200},
		{"POST", "/veilond/v1/crash-reports/" + strings.Repeat("a", 32) + "/send", 200},
		// Only the UI process submits crash text (SubmitCrash).
		{"POST", "/veilond/v1/crash-reports", 403},
	} {
		w := httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, w.Code, c.want)
		}
	}
	if !opened {
		t.Fatal("action not invoked")
	}
}

func TestVeilondDown(t *testing.T) {
	b := New(filepath.Join(t.TempDir(), "missing.sock"), nil)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, httptest.NewRequest("GET", "/veilond/v1/status", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "veilond_unavailable") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestPassThroughAssets(t *testing.T) {
	b := New("/nonexistent", nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "asset") })
	w := httptest.NewRecorder()
	b.Middleware(next).ServeHTTP(w, httptest.NewRequest("GET", "/index.html", nil))
	if w.Body.String() != "asset" {
		t.Fatal("assets must pass through")
	}
}

func TestGetJSONFromUIProcess(t *testing.T) {
	sock, got := fakeVeilond(t)
	b := New(sock, nil)
	var out struct {
		State string `json:"state"`
	}
	if err := b.GetJSON(context.Background(), "/v1/billing/payment-url", &out); err != nil || out.State != "disconnected" {
		t.Fatalf("%v %+v", err, out)
	}
	if r := <-got; r.URL.Path != "/v1/billing/payment-url" {
		t.Fatal(r.URL.Path)
	}
}

func TestPaymentURLAllowedMirrorsCore(t *testing.T) {
	for u, want := range map[string]bool{
		"https://yoomoney.ru/checkout/x":     true,
		"https://pay.heleket.com/pay/x":      true,
		"https://yoomoney.ru.evil.example/x": false,
		"http://pay.heleket.com/pay/x":       false,
		"file:///etc/passwd":                 false,
	} {
		if PaymentURLAllowed(u) != want {
			t.Errorf("%q", u)
		}
	}
}

func TestAutostartRoute(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG login item")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	b := New("/nonexistent", nil)
	e := autostart.Entry{ID: "net.veilon.app", Name: "VeilOn", Exe: "/usr/bin/veilon", Args: []string{"--hidden"}}
	b.HandleApp(AutostartPath, AutostartHandler(e))
	do := func(method, body string) (int, map[string]bool) {
		w := httptest.NewRecorder()
		b.Middleware(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequest(method, AutostartPath, strings.NewReader(body)))
		var st map[string]bool
		_ = json.Unmarshal(w.Body.Bytes(), &st)
		return w.Code, st
	}
	if c, st := do("GET", ""); c != 200 || st["enabled"] || !st["supported"] {
		t.Fatal(c, st)
	}
	if c, st := do("PUT", `{"enabled":true}`); c != 200 || !st["enabled"] {
		t.Fatal(c, st)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "autostart", "net.veilon.app.desktop")); err != nil {
		t.Fatal(err)
	}
	if c, _ := do("PUT", `{}`); c != 400 {
		t.Fatal("enabled is required")
	}
	if c, st := do("PUT", `{"enabled":false}`); c != 200 || st["enabled"] {
		t.Fatal(c, st)
	}
}

func TestCrashOutputAndSubmit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "VeilOn")
	if prev, err := InstallCrashOutput(dir); err != nil || prev != "" {
		t.Fatal(prev, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui.trace"), []byte("panic: ui\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, err := InstallCrashOutput(dir)
	if err != nil || prev != "panic: ui\n" {
		t.Fatal(prev, err)
	}
	sock, got := fakeVeilond(t)
	if err := New(sock, nil).SubmitCrash(context.Background(), "0.8.0", prev); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.Method != "POST" || r.URL.Path != "/v1/crash-reports" {
		t.Fatal(r.Method, r.URL.Path)
	}
}
