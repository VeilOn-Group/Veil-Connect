//go:build linux

package peercred

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOfReportsKernelIdentity(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan *Cred, 1)
	go func() {
		c, err := l.AcceptUnix()
		if err != nil {
			done <- nil
			return
		}
		defer c.Close()
		cred, _ := Of(c)
		done <- cred
	}()
	cc, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	cred := <-done
	if cred == nil {
		t.Fatal("no credentials")
	}
	if cred.UID != uint32(os.Getuid()) || cred.PID != int32(os.Getpid()) || cred.GIDs[0] != uint32(os.Getgid()) { //nolint:gosec // test
		t.Fatalf("cred %+v", cred)
	}
	groups, _ := os.Getgroups()
	for _, g := range groups {
		if !slices.Contains(cred.GIDs, uint32(g)) { //nolint:gosec // test
			t.Fatalf("supplementary group %d missing from %v", g, cred.GIDs)
		}
	}
	if !privileged(cred) {
		t.Fatal("own process must count as the daemon account")
	}
	// The server side is our own process in a 0700 temp dir: trusted.
	if err := VerifyServer(cred, sock); err != nil {
		t.Fatalf("VerifyServer: %v", err)
	}
}
