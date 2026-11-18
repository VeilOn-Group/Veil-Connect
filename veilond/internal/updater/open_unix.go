//go:build !windows

package updater

import (
	"os"
	"syscall"
)

func openNoFollow(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec // inbox file, symlinks refused
}
