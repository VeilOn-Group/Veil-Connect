//go:build windows

package killswitch

import "path/filepath"

// Default returns the platform engine.
func Default(stateDir string) Engine {
	return &WindowsFirewall{StateFile: filepath.Join(stateDir, "killswitch-policy")}
}
