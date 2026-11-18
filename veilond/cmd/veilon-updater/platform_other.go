//go:build !windows

package main

import (
	"fmt"
	"os"
)

const (
	defaultConfig    = "/etc/veilon/veilond.yaml"
	platformCommands = ""
)

func platformCommand(string, string) (bool, error) { return false, nil }

// prepareInbox: on Linux and macOS the package creates the inbox
// (root:veilon 0770); the helper only checks it is a real directory.
func prepareInbox(p string) error {
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("update inbox %s is not a directory", p)
	}
	return nil
}
