// Command veilonctl is a small CLI for veilond's local API (headless Linux,
// debugging, e2e tests).
//
//	veilonctl status
//	veilonctl register | login <number> | logout | account [--reveal]
//	veilonctl refresh | profile
//	veilonctl connect [node] | disconnect
//	veilonctl select tcp|udp vless|hysteria2|naive
//	veilonctl enable|disable vless|hysteria2|naive
//	veilonctl logs [n] | wipe-logs | what-we-store
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// request maps a command line to a local API call; ok is false for a
// usage error.
func request(args []string) (method, path string, body any, ok bool) {
	if len(args) == 0 {
		return "", "", nil, false
	}
	need := func(n int) bool { return len(args) >= n }
	method = http.MethodGet
	switch args[0] {
	case "status":
		path = "/v1/status"
	case "register":
		method, path = http.MethodPost, "/v1/account/register"
	case "login":
		if !need(2) {
			return "", "", nil, false
		}
		method, path, body = http.MethodPost, "/v1/account/login", map[string]string{"account_number": args[1]}
	case "logout":
		method, path = http.MethodPost, "/v1/account/logout"
	case "account":
		path = "/v1/account"
		if len(args) > 1 && args[1] == "--reveal" {
			path += "?reveal=1"
		}
	case "refresh":
		method, path = http.MethodPost, "/v1/profile/refresh"
	case "profile":
		path = "/v1/profile"
	case "connect":
		method, path = http.MethodPost, "/v1/connect"
		if len(args) > 1 {
			body = map[string]string{"node_id": args[1]}
		}
	case "disconnect":
		method, path = http.MethodPost, "/v1/disconnect"
	case "select":
		if !need(3) {
			return "", "", nil, false
		}
		method, path, body = http.MethodPost, "/v1/transport/select", map[string]string{"group": args[1], "transport": args[2]}
	case "enable", "disable":
		if !need(2) {
			return "", "", nil, false
		}
		method, path, body = http.MethodPost, "/v1/transport/enabled", map[string]any{"transport": args[1], "enabled": args[0] == "enable"}
	case "logs":
		path = "/v1/logs"
		if len(args) > 1 {
			path += "?n=" + args[1]
		}
	case "wipe-logs":
		method, path = http.MethodPost, "/v1/logs/wipe"
	case "what-we-store":
		path = "/v1/privacy/what-we-store"
	default:
		return "", "", nil, false
	}
	return method, path, body, true
}

func run(argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("veilonctl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sock := fs.String("socket", envOr("VEILOND_SOCKET", "/run/veilon/sock/veilond.sock"), "veilond socket")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	method, path, body, ok := request(fs.Args())
	if !ok {
		fmt.Fprintln(stderr, "usage: veilonctl status|register|login <n>|logout|account [--reveal]|refresh|profile|connect [node]|disconnect|select tcp|udp <t>|enable <t>|disable <t>|logs [n]|wipe-logs|what-we-store")
		return 2
	}
	c := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", *sock)
		},
	}}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, "http://veilond"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "veilonctl:", err)
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") == nil {
		out = pretty.Bytes()
	}
	fmt.Fprintln(stdout, string(out))
	if resp.StatusCode/100 != 2 {
		return 1
	}
	return 0
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
