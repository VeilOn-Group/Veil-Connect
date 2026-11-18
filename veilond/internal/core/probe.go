package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// socksProber measures a transport by fetching the probe URL through the
// core's authenticated probe inbound; the user name selects the outbound
// (singbox.ClientOptions.ProbeListen). Unlike the Clash delay test it uses
// the same data path as user traffic for every transport.
type socksProber struct {
	addr, password, url string
	timeout             time.Duration
}

func (p socksProber) Through(ctx context.Context, tag string) (time.Duration, error) {
	proxy := &url.URL{Scheme: "socks5", User: url.UserPassword(tag, p.password), Host: p.addr}
	hc := &http.Client{
		Timeout: p.timeout,
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxy),
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("probe via %s: %w", tag, err)
	}
	d := time.Since(start)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("probe via %s: HTTP %d", tag, resp.StatusCode)
	}
	return d, nil
}
