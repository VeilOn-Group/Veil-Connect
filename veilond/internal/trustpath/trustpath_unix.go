//go:build unix

package trustpath

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func trustedOwner(uid uint32) bool {
	return uid == 0 || uid == uint32(os.Geteuid()) //nolint:gosec // euid is non-negative
}

// statOwner returns the owner and mode of p without following a final
// symlink.
func statOwner(p string) (os.FileInfo, uint32, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, 0, fmt.Errorf("%w: %s: no ownership info", ErrUntrusted, p)
	}
	return fi, st.Uid, nil
}

// checkAncestors validates every directory above p (p already resolved):
// owned by root or the daemon, and not writable by group/other unless the
// sticky bit is set (then others cannot rename or remove our entry).
func checkAncestors(p string) error {
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		fi, uid, err := statOwner(d)
		if err != nil {
			return err
		}
		if !trustedOwner(uid) {
			return fmt.Errorf("%w: %s is owned by uid %d", ErrUntrusted, d, uid)
		}
		// A symlink's own mode bits are meaningless; its owner and parent are
		// what protect it.
		if fi.Mode()&os.ModeSymlink == 0 && fi.Mode().Perm()&0o022 != 0 && fi.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("%w: %s is writable by group/other", ErrUntrusted, d)
		}
		if d == filepath.Dir(d) {
			return nil
		}
	}
}

func checkLeaf(p string, wantDir bool) error {
	fi, uid, err := statOwner(p)
	if err != nil {
		return err
	}
	if wantDir != fi.IsDir() || (!wantDir && !fi.Mode().IsRegular()) {
		return fmt.Errorf("%w: %s has unexpected type %s", ErrUntrusted, p, fi.Mode().Type())
	}
	if !trustedOwner(uid) {
		return fmt.Errorf("%w: %s is owned by uid %d", ErrUntrusted, p, uid)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%w: %s is writable by group/other", ErrUntrusted, p)
	}
	return nil
}

func resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Symlinks are fine as long as the link and its target are trusted:
	// the link's own directory is covered by checking the unresolved path's
	// ancestors too.
	if err := checkAncestors(abs); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// CheckFile verifies that path (a binary, library or config) and all its
// parent directories can only be modified by root or the daemon's account.
func CheckFile(path string) error {
	r, err := resolve(path)
	if err != nil {
		return err
	}
	if err := checkAncestors(r); err != nil {
		return err
	}
	return checkLeaf(r, false)
}

// CheckDir verifies a directory the same way.
func CheckDir(path string) error {
	r, err := resolve(path)
	if err != nil {
		return err
	}
	if err := checkAncestors(r); err != nil {
		return err
	}
	return checkLeaf(r, true)
}

// EnsureDir creates path if needed and verifies it is trusted. An existing
// directory owned by someone else is refused (it may hold planted symlinks),
// never silently adopted.
func EnsureDir(path string, o DirOptions) error {
	perm := os.FileMode(o.Perm)
	if perm == 0 {
		perm = 0o700
	}
	if perm&0o022 != 0 {
		return fmt.Errorf("trustpath: mode %o is writable by group/other", perm)
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	r, err := resolve(path)
	if err != nil {
		return err
	}
	if err := checkAncestors(r); err != nil {
		return err
	}
	_, uid, err := statOwner(r)
	if err != nil {
		return err
	}
	if !trustedOwner(uid) {
		return fmt.Errorf("%w: %s is owned by uid %d", ErrUntrusted, r, uid)
	}
	if o.GID > 0 {
		if err := os.Lchown(r, -1, o.GID); err != nil {
			return err
		}
	}
	if err := os.Chmod(r, perm); err != nil {
		return err
	}
	return checkLeaf(r, true)
}
