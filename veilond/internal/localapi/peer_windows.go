//go:build windows

package localapi

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/veilon/veilon-app/veilond/internal/trustpath"
)

func prepareSocketDir(dir string, o UnixOptions) error {
	return trustpath.EnsureDir(dir, trustpath.DirOptions{ShareSIDs: o.Policy.SIDs})
}

// secureSocket replaces the socket file's DACL: SYSTEM and Administrators
// full control, allowed principals read/write (needed to connect). Nobody
// else can open the socket at all; the peer-token check runs on top.
func secureSocket(path string, o UnixOptions) error {
	var b strings.Builder
	b.WriteString("D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	for _, s := range o.Policy.SIDs {
		b.WriteString("(A;;FRFW;;;" + s + ")")
	}
	return trustpath.SetDACL(path, b.String())
}

// ResolveGroups maps account or group names (or SID strings) to SIDs.
func ResolveGroups(names []string) (gids []uint32, sids []string, err error) {
	for _, n := range names {
		if strings.HasPrefix(n, "S-1-") {
			if _, err := windows.StringToSid(n); err != nil {
				return nil, nil, fmt.Errorf("localapi: SID %q: %w", n, err)
			}
			sids = append(sids, n)
			continue
		}
		sid, _, _, err := windows.LookupSID("", n)
		if err != nil {
			return nil, nil, fmt.Errorf("localapi: account %q: %w", n, err)
		}
		sids = append(sids, sid.String())
	}
	return nil, sids, nil
}

// EnsureLocalGroup creates a local group (e.g. "VeilOn Users", whose
// members may control the VPN) if it does not exist yet. Run by
// the LocalSystem services at start: the MSI needs no custom action.
func EnsureLocalGroup(name, comment string) error {
	if _, _, _, err := windows.LookupSID("", name); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, `C:\Windows\System32\net.exe`, "localgroup", name, "/add", "/comment:"+comment).CombinedOutput() //nolint:gosec // fixed program; name from the root-owned config
	// 1379: the group already exists.
	if err != nil && !strings.Contains(string(out), "1379") {
		return fmt.Errorf("localapi: create group %q: %w", name, err)
	}
	return nil
}
