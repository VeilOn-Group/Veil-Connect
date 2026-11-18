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

func main() {
	sock := flag.String("socket", envOr("VEILOND_SOCKET", "/run/veilon/sock/veilond.sock"), "veilond socket")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
	}
	c := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", *sock)
		},
	}}
	method, path, body := http.MethodGet, "", any(nil)
	switch args[0] {
	case "status":
		path = "/v1/status"
	case "register":
		method, path = http.MethodPost, "/v1/account/register"
	case "login":
		need(args, 2)
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
		need(args, 3)
		method, path, body = http.MethodPost, "/v1/transport/select", map[string]string{"group": args[1], "transport": args[2]}
	case "enable", "disable":
		need(args, 2)
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
		usage()
	}
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
		fmt.Fprintln(os.Stderr, "veilonctl:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") == nil {
		out = pretty.Bytes()
	}
	fmt.Println(string(out))
	if resp.StatusCode/100 != 2 {
		os.Exit(1)
	}
}

func need(args []string, n int) {
	if len(args) < n {
		usage()
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: veilonctl status|register|login <n>|logout|account [--reveal]|refresh|profile|connect [node]|disconnect|select tcp|udp <t>|enable <t>|disable <t>|logs [n]|wipe-logs|what-we-store")
	os.Exit(2)
}
