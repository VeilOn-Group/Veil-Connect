package transport

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/profile"
	"github.com/veilon/veil-shared/pkg/singbox"
)

func TestCapabilities(t *testing.T) {
	for _, tr := range All() {
		if tr.Capabilities().SupportsUDP != tr.ID().SupportsUDP() {
			t.Errorf("%s: capability disagrees with profile.SupportsUDP", tr.ID())
		}
		if singbox.TagFor(tr.ID()) != tr.Tag() {
			t.Errorf("%s: tag mismatch", tr.ID())
		}
	}
}

// TestOutboundMatchesGenerator ensures each transport's config slice is the
// one the shared generator emits (single source of truth).
func TestOutboundMatchesGenerator(t *testing.T) {
	p := profile.Fixture()
	opts := singbox.ClientOptions{ClashAPIListen: "127.0.0.1:1", ClashAPISecret: "x", Xray: map[profile.TransportID]*singbox.ExternalSOCKS{
		profile.TransportXHTTP: {Port: 2001, Username: "a", Password: "b"},
		profile.TransportVLESS: {Port: 2002, Username: "c", Password: "d"},
	}}
	cfg, err := singbox.BuildClient(&p, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range All() {
		ob, err := tr.Outbound(&p.Nodes[0], opts)
		if err != nil {
			t.Fatal(err)
		}
		var gen *singbox.Outbound
		for i := range cfg.Outbounds {
			if cfg.Outbounds[i].Tag == tr.Tag() {
				gen = &cfg.Outbounds[i]
			}
		}
		if gen == nil || !reflect.DeepEqual(*gen, ob) {
			t.Errorf("%s: transport outbound differs from generator", tr.ID())
		}
	}
}

type fakeProber struct {
	d   time.Duration
	err error
}

func (f fakeProber) Through(context.Context, string) (time.Duration, error) { return f.d, f.err }

func TestProbeRecordsWindow(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	port, _ := strconv.Atoi(l.Addr().(*net.TCPAddr).AddrPort().String()[len("127.0.0.1:"):])
	p := profile.Fixture()
	p.Nodes[0].Transports.VLESS.Server = "127.0.0.1"
	p.Nodes[0].Transports.VLESS.Port = uint16(port)
	v := NewVLESS()
	ctx := context.Background()
	v.Probe(ctx, &p.Nodes[0], fakeProber{d: 50 * time.Millisecond})
	v.Probe(ctx, &p.Nodes[0], fakeProber{d: 70 * time.Millisecond})
	h := v.Probe(ctx, &p.Nodes[0], fakeProber{err: errors.New("timeout")})
	if h.Reachable || h.Samples != 3 || h.Loss < 0.33 || h.Loss > 0.34 || h.JitterMS != 20 {
		t.Fatalf("unexpected health %+v", h)
	}
	for i := 0; i < 20; i++ {
		v.Probe(ctx, &p.Nodes[0], fakeProber{d: 10 * time.Millisecond})
	}
	if m := v.Metrics(); len(m.Results) != m.Size {
		t.Fatalf("window not bounded: %d", len(m.Results))
	}
}

func h(lat int, ok bool) Health { return Health{Reachable: ok, LatencyMS: lat, Samples: 1} }

func policy() profile.Fallback {
	return profile.Fallback{ProbeIntervalSec: 10, SwitchMarginPct: 25, ConsecutiveProbes: 3, MinDwellSec: 60, FailThreshold: 2, ProbeURL: "https://x"}
}

func TestHysteresisNoFlapping(t *testing.T) {
	t0 := time.Unix(0, 0)
	c := NewController(policy(), profile.TransportVLESS, t0)
	all := []profile.TransportID{profile.TransportVLESS, profile.TransportHysteria2, profile.TransportNaive}
	// Candidate only 10% better: never switch.
	for i := 1; i <= 20; i++ {
		d := c.Evaluate(t0.Add(time.Duration(i)*10*time.Second), map[profile.TransportID]Health{
			profile.TransportVLESS: h(100, true), profile.TransportHysteria2: h(90, true), profile.TransportNaive: h(200, true),
		}, all)
		if d.Switch {
			t.Fatalf("flapped at step %d", i)
		}
	}
}

func TestHysteresisNeedsStreakAndDwell(t *testing.T) {
	t0 := time.Unix(0, 0)
	c := NewController(policy(), profile.TransportVLESS, t0)
	all := []profile.TransportID{profile.TransportVLESS, profile.TransportHysteria2}
	better := map[profile.TransportID]Health{profile.TransportVLESS: h(200, true), profile.TransportHysteria2: h(50, true)}
	// Three wins but within dwell (t < 60s): no switch.
	for i := 1; i <= 3; i++ {
		if c.Evaluate(t0.Add(time.Duration(i)*10*time.Second), better, all).Switch {
			t.Fatal("switched before min dwell")
		}
	}
	// A worse sample resets the streak.
	c.Evaluate(t0.Add(40*time.Second), map[profile.TransportID]Health{profile.TransportVLESS: h(200, true), profile.TransportHysteria2: h(190, true)}, all)
	var d Decision
	for i := 7; i <= 9; i++ {
		d = c.Evaluate(t0.Add(time.Duration(i)*10*time.Second), better, all)
	}
	if !d.Switch || d.To != profile.TransportHysteria2 {
		t.Fatalf("expected switch after streak+dwell, got %+v", d)
	}
}

func TestHysteresisFailoverAndUDPGroup(t *testing.T) {
	t0 := time.Unix(0, 0)
	c := NewController(policy(), profile.TransportHysteria2, t0)
	udpCandidates := []profile.TransportID{profile.TransportHysteria2, profile.TransportVLESS} // naive never offered for UDP
	down := map[profile.TransportID]Health{
		profile.TransportHysteria2: h(0, false), profile.TransportVLESS: h(300, true), profile.TransportNaive: h(10, true),
	}
	if c.Evaluate(t0.Add(time.Second), down, udpCandidates).Switch {
		t.Fatal("must wait FailThreshold")
	}
	d := c.Evaluate(t0.Add(2*time.Second), down, udpCandidates)
	if !d.Switch || d.To != profile.TransportVLESS {
		t.Fatalf("expected failover to vless (naive is faster but not UDP-capable), got %+v", d)
	}
}
