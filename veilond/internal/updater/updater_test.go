package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/release"
	"github.com/veilon/veil-shared/pkg/sign"
)

type fakeInstaller struct {
	calls   []string
	content []byte
}

func (f *fakeInstaller) Install(_ context.Context, path string) error {
	f.calls = append(f.calls, path)
	f.content, _ = os.ReadFile(path)
	return nil
}

var now = time.Unix(1_800_000_000, 0)

type fixture struct {
	t        *testing.T
	root     *sign.Signer
	rel      *sign.Signer
	cfg      *Config
	inst     *fakeInstaller
	artifact string
	payload  []byte
}

func newFixture(t *testing.T) *fixture {
	mk := func(id string, p sign.Purpose) *sign.Signer {
		seed, _ := sign.GenerateSeed()
		s, err := sign.NewSigner(id, p, seed)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	f := &fixture{t: t, root: mk("root-1", sign.PurposeKeyset), rel: mk("rel-1", sign.PurposeRelease), inst: &fakeInstaller{}}
	dir := t.TempDir()
	f.cfg = &Config{
		Inbox: filepath.Join(dir, "inbox"), WorkDir: filepath.Join(dir, "work"),
		RootKeys: []sign.PublicKey{{ID: "root-1", Purpose: sign.PurposeKeyset, Key: f.root.PublicKey()}},
		Platform: "linux-amd64-deb", CurrentVersion: "0.6.0", Installer: f.inst, Now: func() time.Time { return now },
	}
	_ = os.MkdirAll(f.cfg.Inbox, 0o770)
	f.payload = []byte("pretend .deb bytes")
	f.artifact = filepath.Join(dir, "veilon_0.7.0_amd64.deb")
	_ = os.WriteFile(f.artifact, f.payload, 0o644)
	return f
}

func (f *fixture) keyset(version int64) []byte {
	env, _ := f.root.Sign(sign.Keyset{Version: version, Keys: []sign.PublicKey{{ID: "rel-1", Purpose: sign.PurposeRelease, Key: f.rel.PublicKey()}}})
	b, _ := json.Marshal(env)
	return b
}

func (f *fixture) meta(version, platform string) release.Metadata {
	sum := sha256.Sum256(f.payload)
	return release.Metadata{Platform: platform, Channel: "stable", Version: version, URL: "https://updates.example/x.deb",
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(f.payload)), IssuedAt: now.Unix()}
}

func (f *fixture) submit(ks []byte, m release.Metadata, signer *sign.Signer) {
	env, _ := signer.Sign(m)
	b, _ := json.Marshal(env)
	if err := Submit(f.cfg.Inbox, ks, b, f.artifact); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) inboxEmpty() bool {
	e, _ := os.ReadDir(f.cfg.Inbox)
	return len(e) == 0
}

func TestApplyInstallsVerifiedUpdate(t *testing.T) {
	f := newFixture(t)
	if res, err := f.cfg.ApplyOnce(context.Background()); err != nil || res.Requested {
		t.Fatalf("empty inbox: %+v %v", res, err)
	}
	f.submit(f.keyset(3), f.meta("0.7.0", "linux-amd64-deb"), f.rel)
	res, err := f.cfg.ApplyOnce(context.Background())
	if err != nil || res.Installed != "0.7.0" {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if len(f.inst.calls) != 1 || string(f.inst.content) != string(f.payload) {
		t.Fatal("installer must get the verified copy")
	}
	if filepath.Dir(f.inst.calls[0]) != f.cfg.WorkDir {
		t.Fatal("installer must run on the private copy, never on the inbox file")
	}
	if !f.inboxEmpty() {
		t.Fatal("inbox not cleaned")
	}
	if _, err := os.Stat(f.inst.calls[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private copy not removed")
	}
	// The keyset version is remembered: an older keyset is refused later.
	f.submit(f.keyset(2), f.meta("0.8.0", "linux-amd64-deb"), f.rel)
	if _, err := f.cfg.ApplyOnce(context.Background()); err == nil {
		t.Fatal("keyset downgrade accepted")
	}
}

func TestApplyRejects(t *testing.T) {
	cases := map[string]func(f *fixture){
		"downgrade":     func(f *fixture) { f.submit(f.keyset(1), f.meta("0.5.0", "linux-amd64-deb"), f.rel) },
		"same version":  func(f *fixture) { f.submit(f.keyset(1), f.meta("0.6.0", "linux-amd64-deb"), f.rel) },
		"platform":      func(f *fixture) { f.submit(f.keyset(1), f.meta("0.7.0", "linux-amd64-rpm"), f.rel) },
		"unsigned meta": func(f *fixture) { f.submit(f.keyset(1), f.meta("0.7.0", "linux-amd64-deb"), f.root) },
		"foreign root": func(f *fixture) {
			seed, _ := sign.GenerateSeed()
			evil, _ := sign.NewSigner("root-1", sign.PurposeKeyset, seed)
			env, _ := evil.Sign(sign.Keyset{Version: 9, Keys: []sign.PublicKey{{ID: "rel-1", Purpose: sign.PurposeRelease, Key: f.rel.PublicKey()}}})
			b, _ := json.Marshal(env)
			f.submit(b, f.meta("0.7.0", "linux-amd64-deb"), f.rel)
		},
		"tampered artifact": func(f *fixture) {
			f.submit(f.keyset(1), f.meta("0.7.0", "linux-amd64-deb"), f.rel)
			_ = os.WriteFile(filepath.Join(f.cfg.Inbox, FileArtifact), []byte("evil bytes of same size"[:len(f.payload)]), 0o640)
		},
		"symlinked artifact": func(f *fixture) {
			if runtime.GOOS == "windows" {
				f.t.Skip("symlinks need privileges on Windows")
			}
			f.submit(f.keyset(1), f.meta("0.7.0", "linux-amd64-deb"), f.rel)
			p := filepath.Join(f.cfg.Inbox, FileArtifact)
			_ = os.Remove(p)
			_ = os.Symlink(f.artifact, p)
		},
		"missing release": func(f *fixture) {
			f.submit(f.keyset(1), f.meta("0.7.0", "linux-amd64-deb"), f.rel)
			_ = os.Remove(filepath.Join(f.cfg.Inbox, FileRelease))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			setup(f)
			res, err := f.cfg.ApplyOnce(context.Background())
			if err == nil || res.Installed != "" || len(f.inst.calls) != 0 {
				t.Fatalf("accepted: %+v %v", res, err)
			}
			if !f.inboxEmpty() {
				t.Fatal("a rejected request must still be removed (no service loop)")
			}
		})
	}
}

func TestForFormat(t *testing.T) {
	for _, f := range []string{"deb", "rpm", "msi", "pkg"} {
		if _, err := ForFormat(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := ForFormat("tar"); err == nil {
		t.Fatal("tarballs have no silent install")
	}
}
