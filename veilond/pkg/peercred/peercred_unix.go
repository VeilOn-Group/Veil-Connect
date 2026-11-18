//go:build linux || darwin

package peercred

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// privileged: root or the daemon's own account (the hardened Linux service
// runs as a dedicated non-root user with ambient capabilities).
func privileged(c *Cred) bool {
	return c.SID == "" && (c.UID == 0 || c.UID == uint32(os.Geteuid())) //nolint:gosec // uids are non-negative
}

// VerifyServer is the client-side check (UI -> veilond): the process
// listening on sock must be root or the owner of the socket's directory,
// and that directory must be trusted (not writable by others, every
// ancestor owned by root). It prevents a local user from impersonating
// veilond to capture logins typed into the UI.
func VerifyServer(c *Cred, sock string) error {
	dir, err := filepath.EvalSymlinks(filepath.Dir(sock))
	if err != nil {
		return err
	}
	var owner uint32
	for p := dir; ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("peercred: %s: no ownership info", p)
		}
		if p == dir {
			owner = st.Uid
			if fi.Mode().Perm()&0o022 != 0 {
				return fmt.Errorf("peercred: socket directory %s is writable by others", p)
			}
		} else if st.Uid != 0 || (fi.Mode().Perm()&0o022 != 0 && fi.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("peercred: untrusted ancestor %s", p)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if c.UID != 0 && c.UID != owner {
		return fmt.Errorf("peercred: socket served by uid %d, expected root or %d", c.UID, owner)
	}
	return nil
}
