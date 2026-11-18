package core

import (
	"slices"
	"testing"

	"github.com/veilon/veil-shared/pkg/singbox"
	"github.com/veilon/veilon-app/veilond/internal/state"
)

func TestBypassZones(t *testing.T) {
	off := false
	def := state.DefaultSettings()
	legacy := state.Settings{} // saved by an older version: no field
	if !slices.Equal(bypassZones(def, "linux"), singbox.RUZones) || !slices.Equal(bypassZones(legacy, "windows"), singbox.RUZones) {
		t.Fatal("automatic split tunnelling must be on by default, also for old settings files")
	}
	if z := bypassZones(state.Settings{BypassRU: &off}, "linux"); z != nil {
		t.Fatal("switched off by the user")
	}
	ks := state.Settings{KillSwitch: true}
	if bypassZones(ks, "linux") == nil || bypassZones(ks, "windows") == nil {
		t.Fatal("Linux/Windows kill switches let the core's direct traffic out")
	}
	if bypassZones(ks, "darwin") != nil || bypassState(ks, "darwin") != BypassViaTunnelK {
		t.Fatal("macOS + kill switch: zones go through the tunnel instead of failing")
	}
}
