//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Enabled reports whether the Run value exists and points at e.Exe.
func Enabled(e Entry) (bool, error) {
	if err := e.validate(); err != nil {
		return false, err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(e.Name)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == runValue(e), nil
}

// Enable writes the Run value for the current user.
func Enable(e Entry) error {
	if err := e.validate(); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue(e.Name, runValue(e))
}

// Disable removes the Run value.
func Disable(e Entry) error {
	if err := e.validate(); err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	if err := k.DeleteValue(e.Name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
