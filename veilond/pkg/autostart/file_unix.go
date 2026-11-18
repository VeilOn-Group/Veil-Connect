//go:build !windows

package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// path returns the login item file and its content for this OS.
func path(e Entry) (string, string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		return filepath.Join(home, "Library", "LaunchAgents", e.ID+".plist"), launchAgent(e), nil
	case "android", "ios":
		return "", "", ErrUnsupported
	}
	dir, err := os.UserConfigDir() // $XDG_CONFIG_HOME or ~/.config
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, "autostart", e.ID+".desktop"), desktopFile(e), nil
}

// Enabled reports whether the login item exists and points at e.Exe.
func Enabled(e Entry) (bool, error) {
	if err := e.validate(); err != nil {
		return false, err
	}
	p, want, err := path(e)
	if err != nil {
		return false, err
	}
	got, err := os.ReadFile(p) //nolint:gosec // per-user login item path
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(got) == want, nil
}

// Enable writes the login item (0644, in the user's own directory).
func Enable(e Entry) error {
	if err := e.validate(); err != nil {
		return err
	}
	p, content, err := path(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil { //nolint:gosec // read by the session manager
		return err
	}
	return os.Rename(tmp, p)
}

// Disable removes the login item.
func Disable(e Entry) error {
	if err := e.validate(); err != nil {
		return err
	}
	p, _, err := path(e)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
