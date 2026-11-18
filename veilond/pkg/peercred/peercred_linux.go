//go:build linux

package peercred

import (
	"encoding/binary"
	"errors"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// soPeerGroups is SO_PEERGROUPS (Linux >= 4.13) on the asm-generic
// architectures VeilOn ships for (amd64, arm64, arm, 386).
const soPeerGroups = 59

func fromFD(fd uintptr) (*Cred, error) {
	uc, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return nil, err
	}
	c := &Cred{PID: uc.Pid, UID: uc.Uid, GIDs: []uint32{uc.Gid}}
	groups, err := peerGroups(int(fd))
	if err == nil {
		c.GIDs = append(c.GIDs, groups...)
	} else if !errors.Is(err, unix.ENOPROTOOPT) {
		return nil, err
	}
	// ENOPROTOOPT (kernel < 4.13): only the primary group is known, so
	// supplementary-group policies fail closed.
	return c, nil
}

func peerGroups(fd int) ([]uint32, error) {
	buf := make([]byte, 4*64)
	for range 4 {
		n := uint32(len(buf))                    //nolint:gosec // small buffer
		bufp := uintptr(unsafe.Pointer(&buf[0])) //nolint:gosec // getsockopt(2) out-buffer, kept alive below
		lenp := uintptr(unsafe.Pointer(&n))      //nolint:gosec // getsockopt(2) in/out length
		_, _, e := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fd), unix.SOL_SOCKET, soPeerGroups, bufp, lenp, 0)
		runtime.KeepAlive(buf)
		runtime.KeepAlive(&n)
		if e == unix.ERANGE {
			buf = make([]byte, int(n)+4*64)
			continue
		}
		if e != 0 {
			return nil, e
		}
		out := make([]uint32, 0, n/4)
		for i := 0; i+4 <= int(n); i += 4 {
			out = append(out, binary.NativeEndian.Uint32(buf[i:]))
		}
		return out, nil
	}
	return nil, unix.ERANGE
}
