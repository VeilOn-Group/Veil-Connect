//go:build darwin

package killswitch

// Default returns the platform engine.
func Default(stateDir string) Engine { return &PF{} }
