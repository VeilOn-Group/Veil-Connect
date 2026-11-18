package core

import (
	"context"
	"errors"
	"testing"

	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/state"
)

// TestDNSChoice: Quad9 by default, a new choice reconnects with the new
// resolver, an invalid custom URL is refused and the old choice kept.
func TestDNSChoice(t *testing.T) {
	m, core, _ := newManager(t, newFakeAPI(t))
	ctx := context.Background()
	if _, err := m.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Connect(ctx, ""); err != nil {
		t.Fatal(err)
	}
	remote := func() singbox.DNSServer { return core.config().DNS.Servers[0] }
	if s := remote(); s.TLS == nil || s.TLS.ServerName != "dns.quad9.net" || s.Detour != singbox.TagSelectorTCP {
		t.Fatalf("default resolver: %+v", s)
	}
	st := m.Settings()
	st.DNS = &state.DNS{Provider: singbox.DNSCloudflare, CustomURL: "https://ignored.example/q"}
	if err := m.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	if s := remote(); s.Server != "1.1.1.1" || s.TLS.ServerName != "cloudflare-dns.com" {
		t.Fatalf("cloudflare not applied (reconnect): %+v", s)
	}
	if m.Settings().DNS.CustomURL != "" {
		t.Fatal("custom URL kept for a preset")
	}
	st = m.Settings()
	st.DNS = &state.DNS{Provider: singbox.DNSCustom, CustomURL: "https://192.168.0.1/dns-query"}
	if err := m.UpdateSettings(ctx, st); !errors.Is(err, state.ErrInvalidSettings) {
		t.Fatalf("private custom DoH: %v", err)
	}
	if m.Settings().DNS.Provider != singbox.DNSCloudflare {
		t.Fatal("invalid choice replaced the saved one")
	}
	st = m.Settings()
	st.DNS = nil // older UI: keep the current choice
	st.AllowLAN = !st.AllowLAN
	if err := m.UpdateSettings(ctx, st); err != nil || m.Settings().DNS.Provider != singbox.DNSCloudflare {
		t.Fatalf("older UI dropped the choice: %v", err)
	}
	st = m.Settings()
	st.DNS = &state.DNS{Provider: singbox.DNSCustom, CustomURL: "https://doh.example.net/dns-query"}
	if err := m.UpdateSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	if s := remote(); s.Server != "doh.example.net" || s.DomainResolver != singbox.TagDNSBootstrap {
		t.Fatalf("custom: %+v", s)
	}
}
