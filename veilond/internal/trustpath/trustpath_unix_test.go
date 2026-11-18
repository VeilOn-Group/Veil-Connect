//go:build unix

package trustpath

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFileAndDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bin")
	if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(f); err != nil {
		t.Fatalf("trusted file rejected: %v", err)
	}
	if err := os.Chmod(f, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(f); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("world-writable file accepted: %v", err)
	}
	if err := os.Chmod(f, 0o755); err != nil {
		t.Fatal(err)
	}
	// A group/other-writable parent (without sticky bit) taints the file.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(f); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("file in world-writable dir accepted: %v", err)
	}
	if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(filepath.Join(dir, "bin")); err != nil {
		// parent is sticky: others cannot replace our entry
		t.Fatalf("sticky parent rejected: %v", err)
	}
	if err := CheckDir(dir); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("world-writable leaf dir accepted: %v", err)
	}
	if err := CheckDir(f); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("file accepted as dir: %v", err)
	}
}

func TestEnsureDir(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "a", "b")
	if err := EnsureDir(p, DirOptions{Perm: 0o750}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
	// Loose existing mode is tightened.
	if err := os.Chmod(p, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(p, DirOptions{Perm: 0o700}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(p, DirOptions{Perm: 0o777}); err == nil {
		t.Fatal("group/other-writable mode accepted")
	}
}

func TestSymlinkedFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "real")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := filepath.Join(dir, "link")
	if err := os.Symlink(f, l); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(l); err != nil {
		t.Fatalf("trusted symlink rejected: %v", err)
	}
}

// Ownership by another (unprivileged) user can only be tested as root.
func TestForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "bin")
	if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(f, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(f); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("file owned by nobody accepted: %v", err)
	}
	d := filepath.Join(dir, "pre")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(d, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(d, DirOptions{Perm: 0o700}); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("pre-created foreign dir adopted: %v", err)
	}
}
