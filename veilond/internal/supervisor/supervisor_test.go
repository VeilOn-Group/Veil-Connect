//go:build linux || darwin

package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestHelperProcess is the fake "core" (re-executed test binary).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("VEILON_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "exit1":
		fmt.Println("FATAL[0000] start service: bind: address already in use")
		os.Exit(1)
	case "panic":
		fmt.Println("INFO starting")
		panic("core boom near 198.51.100.7")
	case "go-abort":
		// The Go runtime catches the signal and prints "SIGABRT: abort".
		fmt.Println("about to abort")
		_ = syscall.Kill(os.Getpid(), syscall.SIGABRT)
		time.Sleep(time.Minute)
	case "abort":
		// A non-Go core (naive) dies by the signal itself.
		_ = syscall.Exec("/bin/sh", []string{"sh", "-c", "echo about to abort; kill -ABRT $$"}, nil)
		os.Exit(9)
	case "sleep":
		time.Sleep(time.Minute)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Minute)
	case "spawn":
		// The supervisor passes a minimal env (no PATH), so use an absolute path.
		c := exec.Command("/bin/sleep", "60")
		if err := c.Start(); err != nil {
			fmt.Println("spawn failed:", err)
			os.Exit(3)
		}
		fmt.Printf("grandchild=%d\n", c.Process.Pid)
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func selfHash(t *testing.T) (string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(exe)
	defer f.Close()
	h := sha256.New()
	_, _ = io.Copy(h, f)
	return exe, hex.EncodeToString(h.Sum(nil))
}

func helperCfg(t *testing.T, mode string, out io.Writer) Config {
	exe, sum := selfHash(t)
	return Config{
		Bin: exe, ExpectedSHA256: sum,
		Args:        func() []string { return []string{"-test.run=TestHelperProcess", "--", mode} },
		Env:         []string{"VEILON_HELPER=1"},
		Output:      out,
		MinBackoff:  10 * time.Millisecond,
		MaxBackoff:  40 * time.Millisecond,
		StopTimeout: 300 * time.Millisecond,
	}
}

func TestRestartsWithBackoff(t *testing.T) {
	var starts atomic.Int32
	cfg := helperCfg(t, "exit1", io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cfg.BeforeStart = func() error {
		if starts.Add(1) >= 3 {
			cancel()
		}
		return nil
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if starts.Load() < 3 {
		t.Fatalf("expected restarts, got %d", starts.Load())
	}
	if st, _, _ := s.State(); st != StateStopped {
		t.Fatalf("state %s", st)
	}
}

func TestRefusesTamperedBinary(t *testing.T) {
	cfg := helperCfg(t, "sleep", io.Discard)
	cfg.ExpectedSHA256 = strings.Repeat("0", 64)
	started := false
	cfg.BeforeStart = func() error { started = true; return nil }
	s, _ := New(cfg)
	err := s.Run(context.Background())
	if !errors.Is(err, ErrBinaryTampered) || started {
		t.Fatalf("want tamper error without start, got %v started=%v", err, started)
	}
	if st, _, _ := s.State(); st != StateFailed {
		t.Fatalf("state %s", st)
	}
}

func TestRefusesTamperedExtraFile(t *testing.T) {
	cfg := helperCfg(t, "sleep", io.Discard)
	lib := t.TempDir() + "/libcronet.so"
	_ = os.WriteFile(lib, []byte("original"), 0o600)
	cfg.ExtraFiles = map[string]string{lib: strings.Repeat("0", 64)}
	s, _ := New(cfg)
	if err := s.Run(context.Background()); !errors.Is(err, ErrBinaryTampered) {
		t.Fatalf("tampered libcronet must stop the core, got %v", err)
	}
}

func TestRequiresPinnedHash(t *testing.T) {
	cfg := helperCfg(t, "sleep", io.Discard)
	cfg.ExpectedSHA256 = ""
	if _, err := New(cfg); err == nil {
		t.Fatal("unpinned core must be rejected unless AllowUnpinned")
	}
}

func TestStopKillsStubbornChild(t *testing.T) {
	out := &syncBuf{}
	cfg := helperCfg(t, "ignore-term", out)
	s, _ := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, func() bool { return strings.Contains(out.String(), "ready") })
	_, pid, _ := s.State()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop a child ignoring SIGTERM")
	}
	if pid != 0 && processAlive(pid) {
		t.Fatalf("child %d still alive", pid)
	}
}

func TestNoOrphanGrandchildren(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process-group semantics tested on linux")
	}
	out := &syncBuf{}
	s, _ := New(helperCfg(t, "spawn", out))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	var gc int
	waitFor(t, func() bool {
		for _, l := range strings.Split(out.String(), "\n") {
			if v, ok := strings.CutPrefix(l, "grandchild="); ok {
				gc, _ = strconv.Atoi(v)
				return true
			}
		}
		return false
	})
	cancel()
	<-done
	waitFor(t, func() bool { return !processAlive(gc) })
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil {
		return false
	}
	// A zombie still answers kill(0); check /proc state.
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(b))
	return len(fields) > 2 && fields[2] != "Z"
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// TestRefusesWritableLocation: a correctly hashed library in a directory
// others can write is refused anyway (they could swap it between the hash
// check and the load: ADR-0060).
func TestRefusesWritableLocation(t *testing.T) {
	cfg := helperCfg(t, "sleep", io.Discard)
	dir := t.TempDir()
	lib := dir + "/libcronet.so"
	_ = os.WriteFile(lib, []byte("original"), 0o600)
	sum := sha256.Sum256([]byte("original"))
	cfg.ExtraFiles = map[string]string{lib: hex.EncodeToString(sum[:])}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	s, _ := New(cfg)
	if err := s.Run(context.Background()); !errors.Is(err, ErrBinaryTampered) {
		t.Fatalf("library in world-writable dir must stop the core, got %v", err)
	}
}

func TestOnCrash(t *testing.T) {
	for _, tc := range []struct{ mode, kind, want string }{
		{"panic", "panic", "panic: core boom near 198.51.100.7"},
		{"go-abort", "panic", "SIGABRT: abort"},
		{"abort", "signal", "[signal SIGABRT]\nabout to abort"},
		{"exit1", "", ""},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var mu sync.Mutex
			var kinds, texts []string
			cfg := helperCfg(t, tc.mode, io.Discard)
			ctx, cancel := context.WithCancel(context.Background())
			var starts atomic.Int32
			cfg.BeforeStart = func() error {
				if starts.Add(1) >= 2 {
					cancel()
				}
				return nil
			}
			cfg.OnCrash = func(kind, text string) {
				mu.Lock()
				kinds, texts = append(kinds, kind), append(texts, text)
				mu.Unlock()
			}
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Run(ctx)
			mu.Lock()
			defer mu.Unlock()
			if tc.kind == "" {
				if len(kinds) != 0 {
					t.Fatalf("error exit reported as crash: %v", kinds)
				}
				return
			}
			if len(kinds) == 0 || kinds[0] != tc.kind || !strings.HasPrefix(texts[0], tc.want) {
				t.Fatalf("kinds %v texts %q", kinds, texts)
			}
		})
	}
}
