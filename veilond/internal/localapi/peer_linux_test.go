//go:build linux

package localapi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/veilon/veilon-app/veilond/pkg/peercred"
)

// curlAs runs curl against the socket as uid/gid 65534 (nobody) and
// returns its stdout and whether the request succeeded.
func curlAs(t *testing.T, sock string) (string, bool) {
	t.Helper()
	out, err := exec.Command("setpriv", "--reuid=65534", "--regid=65534", "--clear-groups",
		"curl", "-sS", "--max-time", "5", "--unix-socket", sock, "http://veilond/v1/status").CombinedOutput()
	return string(out), err == nil
}

// TestPeerPolicyEnforced proves that an unprivileged local user is refused
// by the kernel-credential check even when file permissions would let it
// connect, and accepted once its group is allowed.
func TestPeerPolicyEnforced(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to switch users")
	}
	for _, bin := range []string{"setpriv", "curl"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	base, err := os.MkdirTemp("", "veilon-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(base, "run", "veilond.sock")

	run := func(policy peercred.Policy) (string, bool) {
		s, _ := newServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = s.ServeUnix(ctx, sock, UnixOptions{GID: 65534, Policy: policy}) }()
		waitSocket(t, sock)
		return curlAs(t, sock)
	}

	// The socket group lets nobody open the file, but the policy does not
	// list the group: rejected before any request is read.
	if out, ok := run(peercred.Policy{}); ok || strings.Contains(out, "transports") {
		t.Fatalf("unauthorised peer was served: %s", out)
	}
	if out, ok := run(peercred.Policy{GIDs: []uint32{65534}}); !ok || !strings.Contains(out, "transports") {
		t.Fatalf("allowed group refused: %s", out)
	}
	fi, err := os.Stat(filepath.Dir(sock))
	if err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("socket dir mode %v %v", fi.Mode(), err)
	}
}

// TestRefusesPlantedFile: a regular file or symlink at the socket path is
// never removed or followed.
func TestRefusesPlantedFile(t *testing.T) {
	s, _ := newServer(t)
	dir := t.TempDir()
	sock := filepath.Join(dir, "veilond.sock")
	if err := os.Symlink("/etc/passwd", sock); err != nil {
		t.Fatal(err)
	}
	err := s.ServeUnix(context.Background(), sock, UnixOptions{})
	if err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("planted symlink accepted: %v", err)
	}
	if _, err := os.Lstat(sock); err != nil {
		t.Fatal("planted file was removed")
	}
}
