//go:build corebind

// Package corebind pins the module graph of the Android bind (see go.mod).
// It is never imported; gomobile binds the two packages listed below.
package corebind

import (
	_ "github.com/sagernet/gomobile/bind"
	_ "github.com/sagernet/sing-box/experimental/libbox"
	_ "github.com/veilon/veil-shared/mobile"
)
