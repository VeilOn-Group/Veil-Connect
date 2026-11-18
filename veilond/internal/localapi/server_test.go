package localapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/veilon/veilon-app/veilond/internal/core"
	"github.com/veilon/veilon-app/veilond/internal/logring"
	"github.com/veilon/veilon-app/veilond/internal/state"
)

func newServer(t *testing.T) (*Server, *logring.Ring) {
	t.Helper()
	st, _ := state.Open(t.TempDir())
	ring, err := logring.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	m := core.New(core.Options{ClientVersion: "0.1.0", RuntimeDir: t.TempDir()}, st, nil)
	s, err := New(m, ring, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return s, ring
}

func unixClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}, Timeout: 5 * time.Second}
}

func TestUnixSocketAndBrowserBlock(t *testing.T) {
	s, ring := newServer(t)
	sock := filepath.Join(t.TempDir(), "veilond.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.ServeUnix(ctx, sock, UnixOptions{}) }()
	waitSocket(t, sock)
	if st, _ := os.Stat(sock); st.Mode().Perm() != 0o660 {
		t.Fatalf("socket perms %v", st.Mode().Perm())
	}
	c := unixClient(sock)
	resp, err := c.Get("http://veilond/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	var st core.Status
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.State != core.Disconnected || len(st.Transports) != 4 {
		t.Fatalf("status %+v", st)
	}
	// Browsers (Origin header) are refused even on the socket.
	req, _ := http.NewRequest(http.MethodPost, "http://veilond/v1/connect", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("browser request got %d", resp.StatusCode)
	}
	// Connecting without a profile is a precondition error.
	resp, _ = c.Post("http://veilond/v1/connect", "application/json", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("connect without profile: %d", resp.StatusCode)
	}
	// What-we-store comes from the embedded inventory.
	resp, _ = c.Get("http://veilond/v1/privacy/what-we-store")
	var sum map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&sum)
	resp.Body.Close()
	if _, ok := sum["not_stored"]; !ok {
		t.Fatalf("summary missing: %v", sum)
	}
	// Logs wipe works.
	_, _ = ring.Write([]byte("line\n"))
	resp, _ = c.Post("http://veilond/v1/logs/wipe", "application/json", nil)
	resp.Body.Close()
	if l, _ := ring.Tail(10); len(l) != 0 {
		t.Fatal("logs not wiped")
	}
}

func TestTCPFallbackRequiresTokenAndHost(t *testing.T) {
	s, _ := newServer(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.ServeTCP(ctx, "127.0.0.1:0", tokenFile) }()
	var raw []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(tokenFile); err == nil && len(b) > 0 {
			raw = b
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	parts := strings.Fields(string(raw))
	if len(parts) != 2 {
		t.Fatalf("token file %q", raw)
	}
	if st, _ := os.Stat(tokenFile); st.Mode().Perm() != 0o600 {
		t.Fatal("token file must be 0600")
	}
	token, port := parts[0], parts[1]
	base := "http://127.0.0.1:" + port
	get := func(host, tok string) int {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/status", nil)
		if host != "" {
			req.Host = host
		}
		if tok != "" {
			req.Header.Set("X-Veilon-Token", tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token: %d", c)
	}
	if c := get("", "wrong"); c != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", c)
	}
	if c := get("evil.example:"+port, token); c != http.StatusForbidden {
		t.Fatalf("DNS rebinding host accepted: %d", c)
	}
	if c := get("", token); c != http.StatusOK {
		t.Fatalf("valid request: %d", c)
	}
}

func waitSocket(t *testing.T, p string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(p); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("socket not created")
}
