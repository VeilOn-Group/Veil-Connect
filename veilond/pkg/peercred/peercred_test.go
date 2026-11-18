package peercred

import (
	"os"
	"testing"
)

func TestPolicyPermit(t *testing.T) {
	p := Policy{UIDs: []uint32{1001}, GIDs: []uint32{2000}, SIDs: []string{"S-1-5-21-1-2-3-1500"}}
	cases := []struct {
		name string
		c    *Cred
		want bool
	}{
		{"nil", nil, false},
		{"allowed uid", &Cred{UID: 1001, GIDs: []uint32{1001}}, true},
		{"group member", &Cred{UID: 1002, GIDs: []uint32{1002, 2000}}, true},
		{"stranger", &Cred{UID: 1003, GIDs: []uint32{1003}}, false},
		{"windows group", &Cred{SID: "S-1-5-21-1-2-3-1001", GroupSIDs: []string{"S-1-5-21-1-2-3-1500"}}, true},
		{"windows stranger", &Cred{SID: "S-1-5-21-1-2-3-1002", GroupSIDs: []string{"S-1-5-32-545"}}, false},
		// A Windows-shaped credential never matches unix uid rules.
		{"sid with uid", &Cred{UID: 1001, SID: "S-1-5-21-1-2-3-1009"}, false},
	}
	if os.Geteuid() == 1003 {
		t.Skip("test uid collides with fixture")
	}
	for _, tc := range cases {
		if got := p.Permit(tc.c); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
