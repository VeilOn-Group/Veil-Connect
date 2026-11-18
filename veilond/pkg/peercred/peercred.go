// Package peercred identifies the process on the other end of a local
// (AF_UNIX) connection using kernel-provided credentials, and decides
// whether it may use veilond's control API (ADR-0060).
//
// The kernel, not the client, supplies the identity:
//   - Linux: SO_PEERCRED (uid, gid, pid) and SO_PEERGROUPS (supplementary
//     groups), both captured at connect(2) time, so no PID-reuse race;
//   - macOS: LOCAL_PEERCRED (xucred: uid and up to 16 groups) and
//     LOCAL_PEERPID;
//   - Windows: SIO_AF_UNIX_GETPEERPID, then the peer process token (user SID
//     and enabled group SIDs). The socket file DACL is the primary,
//     kernel-enforced gate on Windows; the token check is defense in depth.
//
// Every failure is fail-closed: an unknown peer is rejected.
package peercred

import (
	"errors"
	"net"
	"slices"
)

// ErrUnsupported is returned on platforms without a peer-credential API;
// callers must reject the connection.
var ErrUnsupported = errors.New("peercred: unsupported platform")

// Cred is the kernel-reported identity of a peer.
type Cred struct {
	PID int32 // informational (0 if unknown)

	// Unix.
	UID  uint32
	GIDs []uint32 // primary gid first, then supplementary groups

	// Windows.
	SID       string   // token user SID
	GroupSIDs []string // enabled, non-deny-only group SIDs
}

// Policy lists who may use the control API besides privileged principals
// (root / LocalSystem / elevated Administrators) and the daemon's own
// account, which are always allowed.
type Policy struct {
	UIDs []uint32 // unix
	GIDs []uint32 // unix: membership in any of these groups is enough
	SIDs []string // windows: user or group SIDs
}

// Permit reports whether c may use the API.
func (p Policy) Permit(c *Cred) bool {
	if c == nil {
		return false
	}
	if privileged(c) {
		return true
	}
	if slices.Contains(p.UIDs, c.UID) && c.SID == "" {
		return true
	}
	if c.SID == "" {
		for _, g := range c.GIDs {
			if slices.Contains(p.GIDs, g) {
				return true
			}
		}
		return false
	}
	if slices.Contains(p.SIDs, c.SID) {
		return true
	}
	for _, g := range c.GroupSIDs {
		if slices.Contains(p.SIDs, g) {
			return true
		}
	}
	return false
}

// Of returns the credentials of the peer of c.
func Of(c *net.UnixConn) (*Cred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	var cred *Cred
	var cerr error
	if err := raw.Control(func(fd uintptr) { cred, cerr = fromFD(fd) }); err != nil {
		return nil, err
	}
	if cerr != nil {
		return nil, cerr
	}
	return cred, nil
}
