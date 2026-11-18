//go:build !linux && !darwin && !windows

package killswitch

type unsupported struct{}

func (unsupported) Engage(Spec) error     { return ErrUnsupported }
func (unsupported) Release() error        { return nil }
func (unsupported) Active() (bool, error) { return false, nil }

// Default returns the platform engine.
func Default(stateDir string) Engine { return unsupported{} }
