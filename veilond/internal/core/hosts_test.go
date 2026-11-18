package core

import (
	"context"
	"testing"
)

func TestDefaultResolveUnmaps(t *testing.T) {
	addrs, err := DefaultResolve(context.Background(), "localhost")
	if err != nil {
		t.Skip("no resolver:", err)
	}
	for _, a := range addrs {
		if a.Is4In6() {
			t.Fatalf("4-in-6 address leaked: %v", a)
		}
	}
	if a, _ := DefaultResolve(context.Background(), "::ffff:10.0.0.1"); a[0].String() != "10.0.0.1" {
		t.Fatalf("literal not unmapped: %v", a)
	}
}
