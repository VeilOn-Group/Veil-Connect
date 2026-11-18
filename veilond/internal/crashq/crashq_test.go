package crashq

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/veilon/veil-shared/pkg/crash"
)

const trace = "panic: connect 198.51.100.77 failed\n\ngoroutine 1 [running]:\nmain.run(0xc000010000)\n\t/home/bob/src/main.go:10 +0x1f\n"

func TestQueue(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crash")
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	q, err := Open(dir, "0.8.0", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode().Perm())
	}
	r, ok, err := q.Add(crash.ComponentSingBox, crash.KindPanic, "1.13.21", trace)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, r.ID+".json"))
	for _, bad := range []string{"198.51.100.77", "/home/bob", "0xc000010000"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("%q stored on disk: %s", bad, raw)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, r.ID+".json")); st.Mode().Perm() != 0o600 {
		t.Fatal("report must be 0600")
	}
	got, err := q.Get(r.ID)
	if err != nil || got.Day != "2026-11-02" || got.AppVersion != "0.8.0" || got.ComponentVersion != "1.13.21" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := q.Get("../../etc/passwd"); err == nil {
		t.Fatal("path traversal")
	}

	// Cap: only the newest MaxQueued stay.
	for i := 0; i < crash.MaxQueued+3; i++ {
		if _, _, err := q.Add(crash.ComponentVeilond, crash.KindPanic, "", trace); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(q.List()); n != crash.MaxQueued {
		t.Fatalf("kept %d", n)
	}

	// Expiry after MaxAgeDays.
	now = now.AddDate(0, 0, crash.MaxAgeDays+1)
	if n := len(q.List()); n != 0 {
		t.Fatalf("expired reports kept: %d", n)
	}

	if err := q.Delete(r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted report must be gone", err)
	}
}

func TestInstallSelfPicksUpPreviousCrash(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(dir, "0.8.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, selfTrace), []byte("starting\n"+trace), 0o600); err != nil {
		t.Fatal(err)
	}
	queued, err := q.InstallSelf()
	if err != nil || !queued {
		t.Fatal(queued, err)
	}
	l := q.List()
	if len(l) != 1 || l[0].Component != crash.ComponentVeilond || !strings.HasPrefix(l[0].Text, "panic: connect [IP] failed") {
		t.Fatalf("%+v", l)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, selfTrace)); len(b) != 0 {
		t.Fatal("trace file must be truncated")
	}
	// A clean previous run leaves an empty file: nothing queued.
	if queued, _ := q.InstallSelf(); queued {
		t.Fatal("empty trace queued")
	}
}
