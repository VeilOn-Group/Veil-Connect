package clashapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRejectsNonLoopback(t *testing.T) {
	if _, err := New("0.0.0.0:9090", "s"); err == nil {
		t.Fatal("non-loopback controller accepted")
	}
}

func TestMockSelectAndAuth(t *testing.T) {
	now := "a"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies/sel-tcp":
			_ = json.NewEncoder(w).Encode(Group{Type: "Selector", Name: "sel-tcp", Now: now, All: []string{"a", "b"}})
		case r.Method == http.MethodPut && r.URL.Path == "/proxies/sel-tcp":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["name"] != "a" && b["name"] != "b" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"message":"Selector update error: not found"}`))
				return
			}
			now = b["name"]
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	listen := strings.TrimPrefix(srv.URL, "http://")
	c, _ := New(listen, "good")
	ctx := context.Background()
	if err := c.Select(ctx, "sel-tcp", "b"); err != nil {
		t.Fatal(err)
	}
	g, err := c.Group(ctx, "sel-tcp")
	if err != nil || g.Now != "b" {
		t.Fatalf("group %+v err %v", g, err)
	}
	var apiErr *Error
	if err := c.Select(ctx, "sel-tcp", "zzz"); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("want 400, got %v", err)
	}
	bad, _ := New(listen, "wrong")
	if _, err := bad.WaitReady(ctx); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("want immediate 401, got %v", err)
	}
	if _, err := c.Delay(ctx, "a", "http://example.com", time.Second); err == nil {
		t.Fatal("http probe URL must be rejected")
	}
}

// TestNeverCallsConnections enforces ADR-0003: the connections endpoint lists
// destinations and must never be used by veilond.
func TestNeverCallsConnections(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	root := filepath.Join("..", "..")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
		return nil
	})
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), `"/connections`) {
			t.Errorf("%s references the Clash API connections endpoint", f)
		}
	}
}

// TestLiveSingBox runs against the pinned binary when SINGBOX_BIN is set.
func TestLiveSingBox(t *testing.T) {
	bin := os.Getenv("SINGBOX_BIN")
	if bin == "" {
		t.Skip("SINGBOX_BIN not set")
	}
	port := freePort(t)
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"log":{"level":"warn"},
	 "outbounds":[{"type":"direct","tag":"d1"},{"type":"direct","tag":"d2"},
	   {"type":"selector","tag":"sel-tcp","outbounds":["d1","d2"],"default":"d1"}],
	 "route":{"final":"sel-tcp"},
	 "experimental":{"clash_api":{"external_controller":"127.0.0.1:%d","secret":"live-test"}}}`, port)
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	c, _ := New(fmt.Sprintf("127.0.0.1:%d", port), "live-test")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := c.WaitReady(ctx)
	if err != nil || !strings.HasPrefix(v, "sing-box 1.13.") {
		t.Fatalf("version %q err %v", v, err)
	}
	if err := c.Select(ctx, "sel-tcp", "d2"); err != nil {
		t.Fatal(err)
	}
	g, err := c.Group(ctx, "sel-tcp")
	if err != nil || g.Now != "d2" || len(g.All) != 2 {
		t.Fatalf("group %+v err %v", g, err)
	}
	var apiErr *Error
	if err := c.Select(ctx, "sel-tcp", "nope"); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Fatalf("want 400 for unknown member, got %v", err)
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
