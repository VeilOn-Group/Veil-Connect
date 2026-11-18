package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type seen struct {
	method, path string
	body         map[string]any
}

func fakeDaemon(t *testing.T) (string, *[]seen) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var calls []seen
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{method: r.Method, path: r.URL.RequestURI()}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &s.body)
		calls = append(calls, s)
		if strings.HasPrefix(r.URL.Path, "/v1/fail") || r.URL.Path == "/v1/profile" {
			w.WriteHeader(412)
			_, _ = io.WriteString(w, `{"code":"precondition"}`)
			return
		}
		if r.URL.Path == "/v1/logs" {
			_, _ = io.WriteString(w, "not json")
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock, &calls
}

func TestCommands(t *testing.T) {
	sock, calls := fakeDaemon(t)
	cases := []struct {
		args         []string
		method, path string
		code         int
	}{
		{[]string{"status"}, "GET", "/v1/status", 0},
		{[]string{"register"}, "POST", "/v1/account/register", 0},
		{[]string{"login", "1234"}, "POST", "/v1/account/login", 0},
		{[]string{"logout"}, "POST", "/v1/account/logout", 0},
		{[]string{"account", "--reveal"}, "GET", "/v1/account?reveal=1", 0},
		{[]string{"account"}, "GET", "/v1/account", 0},
		{[]string{"refresh"}, "POST", "/v1/profile/refresh", 0},
		{[]string{"profile"}, "GET", "/v1/profile", 1},
		{[]string{"connect", "rs-1"}, "POST", "/v1/connect", 0},
		{[]string{"connect"}, "POST", "/v1/connect", 0},
		{[]string{"disconnect"}, "POST", "/v1/disconnect", 0},
		{[]string{"select", "udp", "hysteria2"}, "POST", "/v1/transport/select", 0},
		{[]string{"enable", "naive"}, "POST", "/v1/transport/enabled", 0},
		{[]string{"disable", "naive"}, "POST", "/v1/transport/enabled", 0},
		{[]string{"logs", "20"}, "GET", "/v1/logs?n=20", 0},
		{[]string{"wipe-logs"}, "POST", "/v1/logs/wipe", 0},
		{[]string{"what-we-store"}, "GET", "/v1/privacy/what-we-store", 0},
	}
	for _, c := range cases {
		var out bytes.Buffer
		code := run(append([]string{"-socket", sock}, c.args...), &out, &bytes.Buffer{})
		last := (*calls)[len(*calls)-1]
		if code != c.code || last.method != c.method || last.path != c.path {
			t.Errorf("%v: exit %d, %s %s", c.args, code, last.method, last.path)
		}
	}
	got := *calls
	if got[2].body["account_number"] != "1234" || got[12].body["enabled"] != true || got[13].body["enabled"] != false ||
		got[8].body["node_id"] != "rs-1" || got[11].body["transport"] != "hysteria2" {
		t.Fatalf("bodies: %+v", got)
	}
}

func TestUsageAndErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"login"}, {"select", "udp"}, {"enable"}, {"-x"}} {
		var e bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &e); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	var e bytes.Buffer
	if code := run([]string{"-socket", filepath.Join(t.TempDir(), "none"), "status"}, &bytes.Buffer{}, &e); code != 1 || !strings.Contains(e.String(), "veilonctl:") {
		t.Fatalf("no daemon: %d %q", code, e.String())
	}
	t.Setenv("VEILOND_SOCKET", "/x")
	if envOr("VEILOND_SOCKET", "d") != "/x" || envOr("VEILOND_NONE", "d") != "d" {
		t.Fatal("envOr")
	}
}
