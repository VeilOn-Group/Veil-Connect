//go:build unix

package localapi

import (
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/veilon/veilon-app/veilond/internal/trustpath"
)

func prepareSocketDir(dir string, o UnixOptions) error {
	// Group members need to traverse the directory; nobody else does.
	perm := uint32(0o700)
	if o.GID > 0 {
		perm = 0o750
	}
	return trustpath.EnsureDir(dir, trustpath.DirOptions{Perm: perm, GID: o.GID})
}

func secureSocket(path string, o UnixOptions) error {
	// 0660: the UI runs as a regular user in the socket's group (ADR-0006).
	if err := os.Chmod(path, 0o660); err != nil { //nolint:gosec // group access is intended
		return err
	}
	if o.GID > 0 {
		if err := os.Lchown(path, -1, o.GID); err != nil {
			return fmt.Errorf("localapi: chown socket to gid %d: %w", o.GID, err)
		}
	}
	return nil
}

// ResolveGroups maps group names (or numeric ids) to gids.
func ResolveGroups(names []string) (gids []uint32, sids []string, err error) {
	for _, n := range names {
		id := n
		if _, perr := strconv.ParseUint(n, 10, 32); perr != nil {
			g, lerr := user.LookupGroup(n)
			if lerr != nil {
				return nil, nil, fmt.Errorf("localapi: group %q: %w", n, lerr)
			}
			id = g.Gid
		}
		v, perr := strconv.ParseUint(id, 10, 32)
		if perr != nil {
			return nil, nil, perr
		}
		gids = append(gids, uint32(v))
	}
	return gids, nil, nil
}
