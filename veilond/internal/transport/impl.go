package transport

import (
	"context"
	"fmt"

	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/singbox"
)

// VLESS (+REALITY) over Vision or XHTTP. Both run in the local Xray
// process; sing-box reaches it through an authenticated loopback SOCKS
// endpoint.
type VLESS struct {
	base
	id profile.TransportID
}

// NewVLESS returns the VLESS Vision transport.
func NewVLESS() *VLESS { return &VLESS{newBase(), profile.TransportVLESS} }

// NewXHTTP returns the VLESS XHTTP transport (the default transport).
func NewXHTTP() *VLESS { return &VLESS{newBase(), profile.TransportXHTTP} }

func (t *VLESS) ID() profile.TransportID { return t.id }
func (t *VLESS) Tag() string             { return singbox.TagFor(t.id) }
func (*VLESS) Capabilities() Capabilities {
	return Capabilities{SupportsUDP: true, Obfuscation: "reality"}
}

func (t *VLESS) settings(n *profile.Node) *profile.VLESS {
	if t.id == profile.TransportXHTTP {
		return n.Transports.XHTTP
	}
	return n.Transports.VLESS
}

func (t *VLESS) Configured(n *profile.Node) bool {
	v := t.settings(n)
	return v != nil && v.Enabled
}
func (t *VLESS) Outbound(n *profile.Node, o singbox.ClientOptions) (singbox.Outbound, error) {
	ext := o.Xray[t.id]
	if !t.Configured(n) || ext == nil {
		return singbox.Outbound{}, ErrNotConfigured
	}
	return singbox.XrayOutbound(t.Tag(), ext), nil
}
func (*VLESS) Start(context.Context) error { return nil }
func (*VLESS) Stop(context.Context) error  { return nil }

// Probe: TCP connect to the node, then HTTPS through the outbound.
func (t *VLESS) Probe(ctx context.Context, n *profile.Node, p Prober) Health {
	if !t.Configured(n) {
		return t.record(0, ErrNotConfigured)
	}
	v := t.settings(n)
	if _, err := tcpConnect(ctx, v.Server, v.Port); err != nil {
		return t.record(0, fmt.Errorf("tcp connect: %w", err))
	}
	d, err := p.Through(ctx, t.Tag())
	return t.record(d, err)
}

// Hysteria2 (QUIC, obfs salamander).
type Hysteria2 struct{ base }

// NewHysteria2 returns the Hysteria 2 transport.
func NewHysteria2() *Hysteria2 { return &Hysteria2{newBase()} }

func (*Hysteria2) ID() profile.TransportID { return profile.TransportHysteria2 }
func (*Hysteria2) Tag() string             { return singbox.TagHysteria2 }
func (*Hysteria2) Capabilities() Capabilities {
	return Capabilities{SupportsUDP: true, Obfuscation: "salamander"}
}
func (*Hysteria2) Configured(n *profile.Node) bool {
	return n.Transports.Hysteria2 != nil && n.Transports.Hysteria2.Enabled
}
func (t *Hysteria2) Outbound(n *profile.Node, o singbox.ClientOptions) (singbox.Outbound, error) {
	if !t.Configured(n) {
		return singbox.Outbound{}, ErrNotConfigured
	}
	return singbox.Hysteria2Outbound(n.Transports.Hysteria2, o.AllowInsecure)
}
func (*Hysteria2) Start(context.Context) error { return nil }
func (*Hysteria2) Stop(context.Context) error  { return nil }

// Probe: an HTTPS request through the hysteria2 outbound requires a completed
// QUIC handshake (salamander + TLS 1.3/h3): this is the QUIC+HTTP probe.
func (t *Hysteria2) Probe(ctx context.Context, n *profile.Node, p Prober) Health {
	if !t.Configured(n) {
		return t.record(0, ErrNotConfigured)
	}
	d, err := p.Through(ctx, t.Tag())
	return t.record(d, err)
}

// Naive (HTTP/2 over TLS, TCP only).
type Naive struct {
	base
	// External is true in external mode: the official
	// naive binary runs next to the core and is supervised by the manager;
	// the core reaches it through an authenticated local SOCKS5 outbound.
	External bool
}

// NewNaive returns the Naive transport.
func NewNaive() *Naive { return &Naive{base: newBase()} }

func (*Naive) ID() profile.TransportID { return profile.TransportNaive }
func (*Naive) Tag() string             { return singbox.TagNaive }
func (t *Naive) Capabilities() Capabilities {
	return Capabilities{SupportsUDP: false, Obfuscation: "https", RequiresExternalProcess: t.External}
}
func (*Naive) Configured(n *profile.Node) bool {
	return n.Transports.Naive != nil && n.Transports.Naive.Enabled
}

// Outbound returns the in-tree outbound; external mode is rendered by the
// manager through singbox.ClientOptions.NaiveExternal.
func (t *Naive) Outbound(n *profile.Node, o singbox.ClientOptions) (singbox.Outbound, error) {
	if !t.Configured(n) {
		return singbox.Outbound{}, ErrNotConfigured
	}
	return singbox.NaiveOutbound(n.Transports.Naive, o.NaiveExternal), nil
}

// Start and Stop are no-ops: the external process (if any) is owned by the
// core manager so that it shares the core's lifetime and kill switch.
func (*Naive) Start(context.Context) error { return nil }
func (*Naive) Stop(context.Context) error  { return nil }

func (t *Naive) Probe(ctx context.Context, n *profile.Node, p Prober) Health {
	if !t.Configured(n) {
		return t.record(0, ErrNotConfigured)
	}
	if _, err := tcpConnect(ctx, n.Transports.Naive.Server, n.Transports.Naive.Port); err != nil {
		return t.record(0, fmt.Errorf("tcp connect: %w", err))
	}
	d, err := p.Through(ctx, t.Tag())
	return t.record(d, err)
}

// All returns the three transports in canonical order.
func All() []Transport { return []Transport{NewXHTTP(), NewVLESS(), NewHysteria2(), NewNaive()} }
