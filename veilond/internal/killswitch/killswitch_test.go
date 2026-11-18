package killswitch

import (
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func spec() Spec {
	return Spec{
		TunName: "veilon0", TunAddr4: netip.MustParseAddr("172.19.0.1"),
		Servers:  []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("203.0.113.10")},
		APIs:     []netip.Addr{netip.MustParseAddr("198.51.100.5")},
		HostIPv6: true, CorePath: `C:\Program Files\VeilOn\sing-box.exe`, DaemonPath: `C:\Program Files\VeilOn\veilond.exe`,
		DirectMark: DefaultDirectMark,
	}
}

func TestNFTRuleset(t *testing.T) {
	rs, err := (&NFTables{}).Ruleset(spec())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"policy drop;", `oifname "lo" accept`, `oifname "veilon0" accept`, "meta mark 0x5649 accept",
		"ip daddr { 198.51.100.5, 203.0.113.10 } accept", "ip6 daddr { 2001:db8::10 } accept",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("missing %q in:\n%s", want, rs)
		}
	}
	if strings.Contains(rs, "10.0.0.0/8") {
		t.Error("LAN must be blocked unless AllowLAN")
	}
	s := spec()
	s.HostIPv6, s.AllowLAN = false, true
	rs, _ = (&NFTables{}).Ruleset(s)
	if strings.Contains(rs, "ip6 ") || strings.Contains(rs, "icmpv6") {
		t.Error("IPv6 rules must be omitted on IPv6-less hosts")
	}
	if !strings.Contains(rs, "10.0.0.0/8") {
		t.Error("AllowLAN must add private ranges")
	}
	s.TunName = `x"; flush ruleset; "`
	if _, err := (&NFTables{}).Ruleset(s); err == nil {
		t.Error("injection via tun name accepted")
	}
}

func TestPFRuleset(t *testing.T) {
	rs, err := (&PF{}).Ruleset(spec())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(rs), "\n")
	if lines[len(lines)-1] != "block drop out quick all" {
		t.Fatalf("last rule must block everything else:\n%s", rs)
	}
	if !strings.Contains(rs, "pass out quick inet from 172.19.0.1 to any") || !strings.Contains(rs, "{ 198.51.100.5 203.0.113.10 }") {
		t.Fatalf("pf rules:\n%s", rs)
	}
}

func TestWindowsCommands(t *testing.T) {
	cmds, err := (&WindowsFirewall{}).Commands(spec())
	if err != nil {
		t.Fatal(err)
	}
	joined := make([]string, len(cmds))
	for i, c := range cmds {
		joined[i] = strings.Join(c, " ")
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{"localip=172.19.0.1", `program=C:\Program Files\VeilOn\sing-box.exe`, "remoteip=198.51.100.5,203.0.113.10,2001:db8::10"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.HasSuffix(joined[len(joined)-1], "firewallpolicy blockinbound,blockoutbound") {
		t.Fatal("policy switch must come last, after allow rules exist")
	}
}

// TestNFTLive engages the real nftables kill switch inside a throwaway
// network namespace and checks what is reachable. Needs root, ip, nft.
func TestNFTLive(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	for _, b := range []string{"ip", "nft"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip(b + " not found")
		}
	}
	ns := "veilon-ks-test"
	run := func(args ...string) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	_ = exec.Command("ip", "netns", "del", ns).Run()
	_ = exec.Command("ip", "link", "del", "ksv0").Run()
	run("ip", "netns", "add", ns)
	t.Cleanup(func() {
		_ = exec.Command("ip", "netns", "del", ns).Run()
		_ = exec.Command("ip", "link", "del", "ksv0").Run()
	})
	run("ip", "link", "add", "ksv0", "type", "veth", "peer", "name", "ksv1")
	run("ip", "link", "set", "ksv1", "netns", ns)
	run("ip", "addr", "add", "10.211.0.1/24", "dev", "ksv0")
	run("ip", "addr", "add", "10.211.0.9/24", "dev", "ksv0")
	run("ip", "link", "set", "ksv0", "up")
	run("ip", "netns", "exec", ns, "ip", "addr", "add", "10.211.0.2/24", "dev", "ksv1")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "ksv1", "up")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")

	// Host listeners: .1 plays the VPN node, .9 an arbitrary internet host.
	for _, a := range []string{"10.211.0.1:18080", "10.211.0.9:18080"} {
		l, err := net.Listen("tcp", a)
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })}
		go func() { _ = srv.Serve(l) }()
		t.Cleanup(func() { _ = srv.Close() })
	}
	reach := func(addr string) bool {
		return exec.Command("ip", "netns", "exec", ns, "curl", "-s", "-m", "2", "-o", "/dev/null", "http://"+addr+"/").Run() == nil
	}
	if !reach("10.211.0.1:18080") || !reach("10.211.0.9:18080") {
		t.Fatal("baseline: both hosts must be reachable before the kill switch")
	}
	wrapper := filepath.Join(t.TempDir(), "nft-in-ns")
	_ = os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec "+ns+" nft \"$@\"\n"), 0o700)
	ks := &NFTables{Bin: wrapper}
	s := Spec{TunName: "veilon0", Servers: []netip.Addr{netip.MustParseAddr("10.211.0.1")}}
	if err := ks.Engage(s); err != nil {
		t.Fatal(err)
	}
	if err := ks.Engage(s); err != nil {
		t.Fatalf("engage must be idempotent: %v", err)
	}
	if on, _ := ks.Active(); !on {
		t.Fatal("not active")
	}
	time.Sleep(100 * time.Millisecond)
	if !reach("10.211.0.1:18080") {
		t.Error("VPN node must stay reachable")
	}
	if reach("10.211.0.9:18080") {
		t.Error("LEAK: non-VPN destination reachable with kill switch engaged")
	}
	if err := ks.Release(); err != nil {
		t.Fatal(err)
	}
	if err := ks.Release(); err != nil {
		t.Fatalf("release must be idempotent: %v", err)
	}
	if !reach("10.211.0.9:18080") {
		t.Error("traffic must flow again after release")
	}
}
