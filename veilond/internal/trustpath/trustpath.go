// Package trustpath refuses files and directories that a less privileged
// local user could modify.
//
// veilond runs with network privileges and executes binaries, reads its
// config and writes state. If any of those paths (or a parent directory) is
// writable by an unprivileged user, the SHA-256 pin is only checked at one
// instant (time-of-check/time-of-use) and the user could swap the file,
// plant a symlink, or pre-create a directory and win root code execution or
// an arbitrary root write. Every such path must therefore be owned by a
// privileged principal and not writable by anyone else, all the way up to
// the filesystem root.
package trustpath

import "errors"

// ErrUntrusted marks a path that fails the ownership/permission rules.
var ErrUntrusted = errors.New("trustpath: untrusted path")

// DirOptions configures EnsureDir.
type DirOptions struct {
	// Perm is the unix mode for the directory (no group/other write).
	Perm uint32
	// GID (unix, > 0): group owner, e.g. the socket group (needs Perm & 0o050).
	GID int
	// ShareSIDs (windows): principals granted read/traverse (not inherited),
	// e.g. the "VeilOn Users" group for the socket directory.
	ShareSIDs []string
}
