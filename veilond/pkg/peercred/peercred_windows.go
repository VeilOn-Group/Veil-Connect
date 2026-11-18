//go:build windows

package peercred

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is SIO_AF_UNIX_GETPEERPID = _WSAIOR(IOC_VENDOR, 256).
const sioAFUnixGetPeerPID = windows.IOC_OUT | windows.IOC_VENDOR | 256

// Well-known SIDs.
const (
	SIDLocalSystem    = "S-1-5-18"
	SIDAdministrators = "S-1-5-32-544"
)

func fromFD(fd uintptr) (*Cred, error) {
	accepted := time.Now()
	var pid uint32
	var n uint32
	out := (*byte)(unsafe.Pointer(&pid)) //nolint:gosec // WSAIoctl out-buffer for a DWORD
	if err := windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0, out, uint32(unsafe.Sizeof(pid)), &n, nil, 0); err != nil {
		return nil, fmt.Errorf("peercred: peer pid: %w", err)
	}
	if pid == 0 {
		return nil, errors.New("peercred: unknown peer")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, fmt.Errorf("peercred: open peer: %w", err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best effort
	// PID reuse guard: the process holding the PID now must have existed
	// when the connection was accepted and must still be running.
	var created, exited, kt, ut windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kt, &ut); err != nil {
		return nil, err
	}
	if time.Unix(0, created.Nanoseconds()).After(accepted) {
		return nil, errors.New("peercred: peer pid was reused")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != 259 /* STILL_ACTIVE */ {
		return nil, errors.New("peercred: peer exited")
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return nil, fmt.Errorf("peercred: peer token: %w", err)
	}
	defer tok.Close() //nolint:errcheck // best effort
	u, err := tok.GetTokenUser()
	if err != nil {
		return nil, err
	}
	c := &Cred{PID: int32(pid), SID: u.User.Sid.String()} //nolint:gosec // windows pids fit
	gs, err := tok.GetTokenGroups()
	if err != nil {
		return nil, err
	}
	for _, g := range gs.AllGroups() {
		if g.Attributes&windows.SE_GROUP_ENABLED != 0 && g.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0 {
			c.GroupSIDs = append(c.GroupSIDs, g.Sid.String())
		}
	}
	return c, nil
}

var selfSID = sync.OnceValue(func() string {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
})

// privileged: LocalSystem, the daemon's own account, or a token in which
// Administrators is enabled (that is, elevated).
func privileged(c *Cred) bool {
	if c.SID == "" {
		return false
	}
	if c.SID == SIDLocalSystem || (selfSID() != "" && c.SID == selfSID()) {
		return true
	}
	for _, g := range c.GroupSIDs {
		if g == SIDAdministrators {
			return true
		}
	}
	return false
}

// VerifyServer is the client-side check: veilond runs as LocalSystem. The
// socket directory itself is protected by its DACL (ADR-0060).
func VerifyServer(c *Cred, _ string) error {
	if c == nil || c.SID != SIDLocalSystem {
		return errors.New("peercred: socket is not served by LocalSystem")
	}
	return nil
}
