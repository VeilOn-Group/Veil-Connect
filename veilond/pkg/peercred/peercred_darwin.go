//go:build darwin

package peercred

import "golang.org/x/sys/unix"

func fromFD(fd uintptr) (*Cred, error) {
	x, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return nil, err
	}
	c := &Cred{UID: x.Uid}
	n := min(int(x.Ngroups), len(x.Groups))
	for i := 0; i < n; i++ {
		c.GIDs = append(c.GIDs, x.Groups[i])
	}
	if pid, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
		c.PID = int32(pid) //nolint:gosec // pid_t
	}
	return c, nil
}
