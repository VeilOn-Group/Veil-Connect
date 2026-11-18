// Package clashapi is a minimal client for the sing-box Clash API, used by
// veilond to switch selectors and run delay probes.
//
// Endpoint behaviour verified against sing-box 1.13.21 (live and in
// experimental/clashapi/proxies.go):
//   - GET  /version                      -> {"version":"sing-box 1.13.21",...}
//   - GET  /proxies/{name}               -> {"type":"Selector","now":..,"all":[..]}
//   - PUT  /proxies/{selector} {"name"}  -> 204; unknown member -> 400
//   - GET  /proxies/{name}/delay?url=&timeout= -> {"delay":ms}; http:// URLs are
//     silently replaced by the default, timeout is parsed as int16 (ms)
//   - missing/invalid bearer secret      -> 401
//
// PRIVACY: the API also exposes connection listings with destinations. This
// package must never call that endpoint (enforced by TestNeverCallsConnections).
package clashapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one sing-box instance on loopback.
type Client struct {
	base   string
	secret string
	hc     *http.Client
}

// New creates a client for http://<listen>. listen must be 127.0.0.1:port.
func New(listen, secret string) (*Client, error) {
	if !strings.HasPrefix(listen, "127.0.0.1:") {
		return nil, errors.New("clashapi: controller must be on 127.0.0.1")
	}
	return &Client{
		base:   "http://" + listen,
		secret: secret,
		hc: &http.Client{
			Timeout: 40 * time.Second,
			// Loopback only: never use a system proxy for the control channel.
			Transport: &http.Transport{Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second},
		},
	}, nil
}

// Error is a non-2xx response.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("clashapi: HTTP %d: %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		return &Error{Status: resp.StatusCode, Message: m.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Version returns the core version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	err := c.do(ctx, http.MethodGet, "/version", nil, &v)
	return v.Version, err
}

// WaitReady polls /version until it answers or ctx ends.
func (c *Client) WaitReady(ctx context.Context) (string, error) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		v, err := c.Version(ctx)
		if err == nil {
			return v, nil
		}
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			return "", err // wrong secret: do not keep polling
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("clashapi: not ready: %w", err)
		case <-t.C:
		}
	}
}

// Group is a selector state.
type Group struct {
	Type string   `json:"type"`
	Name string   `json:"name"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

// Group returns a selector's state.
func (c *Client) Group(ctx context.Context, name string) (*Group, error) {
	var g Group
	if err := c.do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name), nil, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// Select switches selector to member.
func (c *Client) Select(ctx context.Context, selector, member string) error {
	return c.do(ctx, http.MethodPut, "/proxies/"+url.PathEscape(selector), map[string]string{"name": member}, nil)
}

// MaxDelayTimeout is the largest timeout sing-box accepts (int16 milliseconds).
const MaxDelayTimeout = 32767 * time.Millisecond

// Delay measures an HTTPS request through an outbound. probeURL must be https.
func (c *Client) Delay(ctx context.Context, outbound, probeURL string, timeout time.Duration) (time.Duration, error) {
	if !strings.HasPrefix(probeURL, "https://") {
		return 0, errors.New("clashapi: probe URL must be https (sing-box ignores http URLs)")
	}
	if timeout <= 0 || timeout > MaxDelayTimeout {
		timeout = MaxDelayTimeout
	}
	q := url.Values{"url": {probeURL}, "timeout": {fmt.Sprint(timeout.Milliseconds())}}
	var d struct {
		Delay int `json:"delay"`
	}
	if err := c.do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(outbound)+"/delay?"+q.Encode(), nil, &d); err != nil {
		return 0, err
	}
	return time.Duration(d.Delay) * time.Millisecond, nil
}
