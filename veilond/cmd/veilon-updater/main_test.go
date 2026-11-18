package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trustedDir is a directory only root can write (the updater refuses
// configs anyone else could change).
func trustedDir(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root: the config must be root-owned")
	}
	d, err := os.MkdirTemp("/var/tmp", "veilon-updater-test-")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(d, 0o755)
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func writeConfig(t *testing.T, dir, inbox string) string {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	cfg := fmt.Sprintf(`runtime_dir: %[1]s/run
state_dir: %[1]s/state
log_dir: %[1]s/log
singbox: {path: /usr/lib/veilon/sing-box, sha256: %[2]s}
xray: {path: /usr/lib/veilon/xray, sha256: %[3]s}
update: {inbox: %[4]s, work_dir: %[1]s/work}
api: {base_urls: ["https://api.example.net"], spki_pins: ["%[5]s", "%[7]s"]}
trust: {root_keys: [{id: root-1, key: %[6]s}]}
local_api: {socket: %[1]s/sock/veilond.sock}
`, dir, strings.Repeat("a", 64), strings.Repeat("b", 64), inbox, base64.StdEncoding.EncodeToString(make([]byte, 32)), base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	p := filepath.Join(dir, "veilond.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVersionAndUsage(t *testing.T) {
	var o, e bytes.Buffer
	if code := run([]string{"version"}, &o, &e); code != 0 || !strings.Contains(o.String(), Version) {
		t.Fatalf("%d %q", code, o.String())
	}
	if code := run([]string{"frobnicate"}, &o, &e); code != 1 || !strings.Contains(e.String(), "unknown command") {
		t.Fatalf("%d %q", code, e.String())
	}
	if code := run([]string{"-nope"}, &o, &e); code != 2 {
		t.Fatalf("bad flag %d", code)
	}
	if code := run([]string{"-config", filepath.Join(t.TempDir(), "none.yaml")}, &o, &e); code != 1 {
		t.Fatalf("missing config %d", code)
	}
}

// A build without a package format (a plain tar build, as in tests) has no
// silent installer: apply refuses after checking the config and paths.
func TestApplyWithoutPackageFormat(t *testing.T) {
	dir := trustedDir(t)
	inbox := filepath.Join(dir, "inbox")
	if err := os.Mkdir(inbox, 0o770); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir, inbox)
	var o, e bytes.Buffer
	if code := run([]string{"-config", cfg, "apply"}, &o, &e); code != 1 || !strings.Contains(e.String(), "no silent installer") {
		t.Fatalf("apply: %d %s", code, e.String())
	}
	if st, err := os.Stat(filepath.Join(dir, "work")); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("work dir: %v %v", st, err)
	}
}

func TestLoadRejects(t *testing.T) {
	dir := trustedDir(t)
	// The inbox is a file, not a directory.
	notDir := filepath.Join(dir, "inbox-file")
	_ = os.WriteFile(notDir, nil, 0o600)
	if _, err := load(writeConfig(t, dir, notDir)); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("inbox file: %v", err)
	}
	if _, err := load(writeConfig(t, dir, filepath.Join(dir, "missing"))); err == nil {
		t.Fatal("missing inbox accepted")
	}
	// A config others can write is refused.
	cfg := writeConfig(t, dir, notDir)
	_ = os.Chmod(cfg, 0o666)
	if _, err := load(cfg); err == nil || !strings.Contains(err.Error(), "config:") {
		t.Fatalf("writable config: %v", err)
	}
	// Update section required.
	_ = os.WriteFile(cfg, []byte(strings.Replace(readFile(t, writeConfig(t, dir, notDir)), "update:", "x_update:", 1)), 0o644)
	if _, err := load(cfg); err == nil {
		t.Fatal("config without update section accepted")
	}
}

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
