// Package transport defines the Transport abstraction (VLESS, Hysteria 2,
// Naive). All three run inside the single sing-box core; a Transport owns its
// slice of the config, its capabilities, its probes and its local metrics.
// Metrics never leave the device (ADR-0004).
package transport

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/singbox"
)

// Capabilities describe what a transport can carry.
type Capabilities struct {
	SupportsUDP             bool   `json:"supports_udp"`
	Obfuscation             string `json:"obfuscation"`
	RequiresExternalProcess bool   `json:"requires_external_process"`
}

// Health is the latest local assessment.
type Health struct {
	Reachable bool      `json:"reachable"`
	LatencyMS int       `json:"latency_ms"`
	JitterMS  int       `json:"jitter_ms"`
	Loss      float64   `json:"loss"` // 0..1 over the window
	Samples   int       `json:"samples"`
	CheckedAt time.Time `json:"-"` // in-memory only, not exposed or persisted
	LastError string    `json:"last_error,omitempty"`
}

// Prober performs network probes. Implemented by the core manager (Clash API
// delay through an outbound) and by plain dialing.
type Prober interface {
	// Through measures an HTTPS request through the given outbound tag.
	Through(ctx context.Context, outboundTag string) (time.Duration, error)
}

// Transport is one tunnel protocol.
type Transport interface {
	ID() profile.TransportID
	Tag() string
	Capabilities() Capabilities
	// Configured reports whether the node offers this transport.
	Configured(n *profile.Node) bool
	// Outbound returns this transport's slice of the sing-box config.
	Outbound(n *profile.Node, opts singbox.ClientOptions) (singbox.Outbound, error)
	// Start/Stop manage auxiliary resources (external naive process in
	// fallback mode); no-ops for in-core transports.
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// Probe runs this transport's health checks and records the result.
	Probe(ctx context.Context, n *profile.Node, p Prober) Health
	// Health returns the latest recorded health.
	Health() Health
	// Metrics returns the local rolling window.
	Metrics() Window
}

// ErrNotConfigured is returned when a node lacks the transport.
var ErrNotConfigured = errors.New("transport: not configured on node")

// Window keeps the last N probe results in memory.
type Window struct {
	Size    int             `json:"size"`
	Results []time.Duration `json:"-"` // negative = failure
}

// base implements bookkeeping shared by all transports.
type base struct {
	mu     sync.Mutex
	window Window
	health Health
}

func newBase() base { return base{window: Window{Size: 10}} }

func (b *base) record(lat time.Duration, err error) Health {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := lat
	if err != nil {
		r = -1
	}
	b.window.Results = append(b.window.Results, r)
	if len(b.window.Results) > b.window.Size {
		b.window.Results = b.window.Results[len(b.window.Results)-b.window.Size:]
	}
	b.health = summarize(b.window.Results)
	b.health.CheckedAt = time.Now()
	if err != nil {
		b.health.LastError = err.Error()
	}
	return b.health
}

func (b *base) Health() Health {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.health
}

func (b *base) Metrics() Window {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Window{Size: b.window.Size, Results: append([]time.Duration(nil), b.window.Results...)}
}

// summarize computes p50 latency, mean absolute successive difference
// (jitter) and loss ratio over the window.
func summarize(rs []time.Duration) Health {
	var ok []time.Duration
	fails := 0
	for _, r := range rs {
		if r < 0 {
			fails++
		} else {
			ok = append(ok, r)
		}
	}
	h := Health{Samples: len(rs)}
	if len(rs) > 0 {
		h.Loss = float64(fails) / float64(len(rs))
	}
	if len(ok) == 0 {
		return h
	}
	// Reachable reflects the most recent sample.
	h.Reachable = rs[len(rs)-1] >= 0
	if len(ok) > 1 {
		var sum time.Duration
		for i := 1; i < len(ok); i++ {
			d := ok[i] - ok[i-1]
			if d < 0 {
				d = -d
			}
			sum += d
		}
		h.JitterMS = int((sum / time.Duration(len(ok)-1)).Milliseconds())
	}
	sorted := append([]time.Duration(nil), ok...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	h.LatencyMS = int(sorted[len(sorted)/2].Milliseconds())
	return h
}

// tcpConnect measures a TCP handshake to host:port (VLESS/Naive pre-check:
// distinguishes "port blocked" from "protocol blocked").
func tcpConnect(ctx context.Context, host string, port uint16) (time.Duration, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, itoa(port)))
	if err != nil {
		return 0, err
	}
	_ = c.Close()
	return time.Since(start), nil
}

func itoa(p uint16) string {
	const digits = "0123456789"
	if p == 0 {
		return "0"
	}
	var buf [5]byte
	i := len(buf)
	for p > 0 {
		i--
		buf[i] = digits[p%10]
		p /= 10
	}
	return string(buf[i:])
}
