package localapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/veilon/veilon-app/veilond/pkg/peercred"
)

// UnixOptions configures the control socket.
type UnixOptions struct {
	// GID (unix, > 0) owns the socket and its directory; members may connect.
	GID int
	// Policy lists principals allowed besides root/LocalSystem/elevated
	// administrators and the daemon's own account.
	Policy peercred.Policy
}

// peerListener authenticates every connection with kernel-supplied peer
// credentials and drops unauthorised peers before any byte is read.
// File permissions (unix) and the socket DACL (Windows) are the first gate;
// this check is authoritative and fail-closed.
type peerListener struct {
	net.Listener
	policy peercred.Policy
	log    *slog.Logger
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			_ = c.Close()
			continue
		}
		cred, err := peercred.Of(uc)
		if err == nil && l.policy.Permit(cred) {
			return c, nil
		}
		reason := "peer not allowed"
		if err != nil {
			reason = "peer credentials unavailable"
		}
		l.log.Warn("local API connection rejected", "reason", reason)
		_ = c.Close()
	}
}

// ServeUnix serves on a unix socket until ctx ends.
//
// The socket directory is created (or verified) as trusted: owned by
// root/the daemon, not writable by anyone else, every ancestor likewise.
// A pre-existing non-socket at path is refused instead of removed, so a
// planted file or symlink is never followed with privileges.
func (s *Server) ServeUnix(ctx context.Context, path string, o UnixOptions) error {
	if err := prepareSocketDir(filepath.Dir(path), o); err != nil {
		return fmt.Errorf("localapi: socket directory: %w", err)
	}
	switch fi, err := os.Lstat(path); {
	case err == nil && fi.Mode().Type() == fs.ModeSocket:
		if err := os.Remove(path); err != nil {
			return err
		}
	case err == nil:
		return fmt.Errorf("localapi: %s exists and is not a socket", path)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return err
	}
	if err := secureSocket(path, o); err != nil {
		_ = l.Close()
		return err
	}
	return s.serve(ctx, &peerListener{Listener: l, policy: o.Policy, log: s.log}, false)
}
