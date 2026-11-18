//go:build !linux && !darwin && !windows

package peercred

func fromFD(uintptr) (*Cred, error) { return nil, ErrUnsupported }

func privileged(*Cred) bool { return false }

// VerifyServer fails closed on unsupported platforms.
func VerifyServer(*Cred, string) error { return ErrUnsupported }
