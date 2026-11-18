package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionsAndRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	k1, err := s.DeviceKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := s.DeviceKey()
	if !k1.Equal(k2) {
		t.Fatal("device key must be stable")
	}
	if err := s.SaveTokens(Tokens{Access: "a", Refresh: "r", AccessExp: 1}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir perms %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		info, _ := e.Info()
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s perms %v, want 0600", e.Name(), info.Mode().Perm())
		}
	}
	tok, err := s.LoadTokens()
	if err != nil || tok.Refresh != "r" {
		t.Fatalf("tokens %+v err %v", tok, err)
	}
	if !s.LoadSettings().ClearLogsOnExit {
		t.Fatal("default must clear logs on exit")
	}
}

func TestClearSessionRotatesDevice(t *testing.T) {
	s, _ := Open(t.TempDir())
	k1, _ := s.DeviceKey()
	_ = s.SaveAccountNumber("1234")
	if err := s.ClearSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AccountNumber(); !errors.Is(err, ErrNotFound) {
		t.Fatal("account number must be removed")
	}
	k2, _ := s.DeviceKey()
	if k1.Equal(k2) {
		t.Fatal("device key must rotate on logout")
	}
}

func TestKeysetRecordRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadKeyset(); err == nil {
		t.Fatal("missing keyset loaded")
	}
	if err := s.SaveKeyset(KeysetRecord{Version: 7}); err != nil {
		t.Fatal(err)
	}
	r, err := s.LoadKeyset()
	if err != nil || r.Version != 7 {
		t.Fatalf("keyset version lost: %+v %v", r, err)
	}
}
