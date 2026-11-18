// Package supervisor runs the sing-box core as a child process:
// binary hash verification before every start, exponential backoff with
// jitter, process-group termination (no zombies), graceful stop with timeout.
package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/veilon/veil-shared/pkg/crash"
	"github.com/veilon/veilon-app/veilond/internal/trustpath"
)

// State of the supervised process.
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateBackoff  State = "backoff"
	StateFailed   State = "failed" // unrecoverable (e.g. tampered binary)
)

// ErrBinaryTampered means the core binary does not match the pinned hash.
var ErrBinaryTampered = errors.New("supervisor: core binary hash mismatch")

// Config configures a Supervisor.
type Config struct {
	Bin            string
	ExpectedSHA256 string // hex; empty only allowed with AllowUnpinned (dev)
	AllowUnpinned  bool   // dev builds only
	// ExtraFiles maps additional files loaded by the binary (e.g. libcronet
	// next to sing-box) to their pinned SHA-256; verified before every start.
	ExtraFiles  map[string]string
	Args        func() []string // evaluated before every start
	BeforeStart func() error    // e.g. write the config file
	Env         []string        // minimal environment for the child
	Output      io.Writer       // receives stdout+stderr (must be a redacting writer)
	MinBackoff  time.Duration   // default 1s
	MaxBackoff  time.Duration   // default 60s
	StableAfter time.Duration   // reset backoff after running this long; default 30s
	StopTimeout time.Duration   // SIGTERM -> SIGKILL; default 5s
	OnState     func(State, error)
	// OnCrash is called when the process crashed (not for error exits or
	// stops we asked for): kind is crash.KindPanic with the Go traceback
	// from its output, or crash.KindSignal with the fault and the last
	// output lines. The text is raw: callers must sanitize it.
	OnCrash func(kind, text string)
	Logger  *slog.Logger
}

// Supervisor manages one child process.
type Supervisor struct {
	cfg Config

	mu      sync.Mutex
	state   State
	lastErr error
	pid     int
}

// New validates cfg and returns a Supervisor.
func New(cfg Config) (*Supervisor, error) {
	if cfg.Bin == "" || cfg.Args == nil || cfg.Output == nil {
		return nil, errors.New("supervisor: Bin, Args and Output are required")
	}
	if cfg.ExpectedSHA256 == "" && !cfg.AllowUnpinned {
		return nil, errors.New("supervisor: ExpectedSHA256 is required")
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 60 * time.Second
	}
	if cfg.StableAfter <= 0 {
		cfg.StableAfter = 30 * time.Second
	}
	if cfg.StopTimeout <= 0 {
		cfg.StopTimeout = 5 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{cfg: cfg, state: StateStopped}, nil
}

// State returns the current state, pid (0 if not running) and last error.
func (s *Supervisor) State() (State, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.pid, s.lastErr
}

func (s *Supervisor) set(st State, err error, pid int) {
	s.mu.Lock()
	s.state, s.lastErr, s.pid = st, err, pid
	s.mu.Unlock()
	if s.cfg.OnState != nil {
		s.cfg.OnState(st, err)
	}
}

// VerifyBinary checks that the binary sits in a path only privileged
// principals can modify (otherwise the hash below would be a
// time-of-check/time-of-use race) and its pinned SHA-256.
func VerifyBinary(path, wantHex string) error {
	if err := trustpath.CheckFile(path); err != nil {
		return fmt.Errorf("%w: %w", ErrBinaryTampered, err)
	}
	return verifyHash(path, wantHex)
}

func verifyHash(path, wantHex string) error {
	f, err := os.Open(path) //nolint:gosec // path of the pinned core binary from config
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantHex) {
		return fmt.Errorf("%w: got %s", ErrBinaryTampered, got)
	}
	return nil
}

// Run supervises until ctx is cancelled. It returns nil on clean shutdown or
// ErrBinaryTampered (never restarts a tampered binary).
func (s *Supervisor) Run(ctx context.Context) error {
	backoff := s.cfg.MinBackoff
	for {
		if ctx.Err() != nil {
			s.set(StateStopped, nil, 0)
			return nil
		}
		started := time.Now()
		err := s.runOnce(ctx)
		if errors.Is(err, ErrBinaryTampered) {
			s.set(StateFailed, err, 0)
			return err
		}
		if ctx.Err() != nil {
			s.set(StateStopped, nil, 0)
			return nil
		}
		if time.Since(started) >= s.cfg.StableAfter {
			backoff = s.cfg.MinBackoff
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff)/4+1)) //nolint:gosec // jitter, not security
		s.cfg.Logger.Warn("core exited, restarting", "err", err, "backoff", wait.String())
		s.set(StateBackoff, err, 0)
		select {
		case <-ctx.Done():
			s.set(StateStopped, nil, 0)
			return nil
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > s.cfg.MaxBackoff {
			backoff = s.cfg.MaxBackoff
		}
	}
}

func (s *Supervisor) runOnce(ctx context.Context) error {
	s.set(StateStarting, nil, 0)
	if !s.cfg.AllowUnpinned || s.cfg.ExpectedSHA256 != "" {
		if err := VerifyBinary(s.cfg.Bin, s.cfg.ExpectedSHA256); err != nil {
			return err
		}
	} else if err := trustpath.CheckFile(s.cfg.Bin); err != nil {
		return fmt.Errorf("%w: %w", ErrBinaryTampered, err)
	}
	for path, sum := range s.cfg.ExtraFiles {
		if err := VerifyBinary(path, sum); err != nil {
			return fmt.Errorf("%w (%s)", err, filepath.Base(path))
		}
	}
	if s.cfg.BeforeStart != nil {
		if err := s.cfg.BeforeStart(); err != nil {
			return fmt.Errorf("supervisor: before start: %w", err)
		}
	}
	cmd := exec.CommandContext(ctx, s.cfg.Bin, s.cfg.Args()...) //nolint:gosec // binary verified by SHA-256 above
	cmd.Env = s.cfg.Env
	out := s.cfg.Output
	var tail *tailBuffer
	if s.cfg.OnCrash != nil {
		// In-memory copy of the last output, read only after a crash.
		tail = newTail(tailBytes)
		out = io.MultiWriter(s.cfg.Output, tail)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	configureProcess(cmd)
	cmd.Cancel = func() error { return terminateGroup(cmd) }
	cmd.WaitDelay = s.cfg.StopTimeout // then the runtime kills it (SIGKILL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("supervisor: start: %w", err)
	}
	s.set(StateRunning, nil, cmd.Process.Pid)
	err := cmd.Wait()
	killGroup(cmd) // reap anything left in the group (no zombies/orphans)
	if err != nil && tail != nil && ctx.Err() == nil {
		if kind, text, ok := classify(err, tail.String()); ok {
			s.cfg.OnCrash(kind, text)
		}
	}
	return err
}

// tailBytes is how much output is kept for a crash report.
const tailBytes = 48 << 10

// classify decides whether an exit was a crash.
func classify(err error, tail string) (kind, text string, ok bool) {
	if tb, ok := crash.FromGoOutput(tail); ok {
		return crash.KindPanic, tb, true
	}
	name := faultName(err)
	if name == "" {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(tail, "\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return crash.KindSignal, "[signal " + name + "]\n" + strings.Join(lines, "\n"), true
}

// tailBuffer keeps the last n bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func newTail(n int) *tailBuffer { return &tailBuffer{n: n} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.n; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
