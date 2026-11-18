package core

import (
	"testing"

	"github.com/veilon/veil-shared/pkg/profile"
)

func TestNodeChanged(t *testing.T) {
	a := profile.Fixture()
	b := profile.Fixture()
	id := a.Nodes[0].ID
	if nodeChanged(&a, &b, id) {
		t.Fatal("identical profiles")
	}
	b.Nodes[0].Transports.VLESS.Reality.PublicKey = "rotated"
	if !nodeChanged(&a, &b, id) {
		t.Fatal("rotated REALITY key not detected")
	}
	c := profile.Fixture()
	c.Subscription.EndsOn = "2099-01-01"
	if nodeChanged(&a, &c, id) {
		t.Fatal("only the active location matters")
	}
	if !nodeChanged(&a, &profile.Profile{}, id) {
		t.Fatal("location removed")
	}
}
