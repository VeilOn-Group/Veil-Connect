// Package crashq keeps crash reports on the device until they are sent or
// expire (ADR-0077). Reports are sanitized (veil-shared/pkg/crash) before
// they touch the disk, so the queue never holds raw tracebacks. At most
// crash.MaxQueued reports are kept, none older than crash.MaxAgeDays.
//
// Nothing leaves the device unless the user opted in ("crash_reports") or
// presses "Send" on a report: sending is the core manager's job.
package crashq

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/veilon/veil-shared/pkg/apitypes"
	"github.com/veilon/veil-shared/pkg/crash"
)

// ErrNotFound is returned for an unknown report id.
var ErrNotFound = errors.New("crashq: no such report")

// selfTrace is where the Go runtime writes veilond's own fatal error.
const selfTrace = "veilond.trace"

var reID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Queue is a directory of sanitized reports (one JSON file each, 0600).
type Queue struct {
	dir        string
	appVersion string
	now        func() time.Time

	mu sync.Mutex
}

// Open creates the directory (0700) if needed.
func Open(dir, appVersion string, now func() time.Time) (*Queue, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory: owner-only traversal
		return nil, err
	}
	return &Queue{dir: dir, appVersion: appVersion, now: now}, nil
}

// Add sanitizes text and stores a report. It returns false (and stores
// nothing) when the sanitized text is empty.
func (q *Queue) Add(component, kind, componentVersion, text string) (apitypes.CrashReport, bool, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return apitypes.CrashReport{}, false, err
	}
	now := q.now()
	r := apitypes.CrashReport{
		ID: hex.EncodeToString(id), Component: component, Kind: kind,
		AppVersion: q.appVersion, ComponentVersion: componentVersion,
		OS: runtime.GOOS, Arch: runtime.GOARCH, Day: crash.Today(now),
		Text: crash.Sanitize(text),
	}
	if r.Text == "" {
		return r, false, nil
	}
	if err := crash.Validate(r, now); err != nil {
		return r, false, err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return r, false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := writeFile0600(filepath.Join(q.dir, r.ID+".json"), b); err != nil {
		return r, false, err
	}
	q.pruneLocked()
	return r, true, nil
}

// List returns the queued reports, newest first (expired ones are removed).
func (q *Queue) List() []apitypes.CrashReport {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pruneLocked()
}

// Get returns one report.
func (q *Queue) Get(id string) (apitypes.CrashReport, error) {
	if !reID.MatchString(id) {
		return apitypes.CrashReport{}, ErrNotFound
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r, err := q.read(id + ".json")
	if err != nil {
		return r, ErrNotFound
	}
	return r, nil
}

// Delete removes one report.
func (q *Queue) Delete(id string) error {
	if !reID.MatchString(id) {
		return ErrNotFound
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	err := os.Remove(filepath.Join(q.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// WipeAll removes every queued report (opt-out, account deletion).
func (q *Queue) WipeAll() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, r := range q.pruneLocked() {
		_ = os.Remove(filepath.Join(q.dir, r.ID+".json"))
	}
}

// pruneLocked drops expired and surplus reports and returns the rest,
// newest first.
func (q *Queue) pruneLocked() []apitypes.CrashReport {
	ents, _ := os.ReadDir(q.dir)
	var all []apitypes.CrashReport
	oldest := crash.Today(q.now().AddDate(0, 0, -crash.MaxAgeDays))
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") || !reID.MatchString(strings.TrimSuffix(e.Name(), ".json")) {
			continue
		}
		r, err := q.read(e.Name())
		if err != nil || r.Day < oldest {
			_ = os.Remove(filepath.Join(q.dir, e.Name()))
			continue
		}
		all = append(all, r)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Day != all[j].Day {
			return all[i].Day > all[j].Day
		}
		return modTime(q.dir, all[i].ID) > modTime(q.dir, all[j].ID)
	})
	for len(all) > crash.MaxQueued {
		_ = os.Remove(filepath.Join(q.dir, all[len(all)-1].ID+".json"))
		all = all[:len(all)-1]
	}
	return all
}

func modTime(dir, id string) int64 {
	st, err := os.Stat(filepath.Join(dir, id+".json"))
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}

func (q *Queue) read(name string) (apitypes.CrashReport, error) {
	var r apitypes.CrashReport
	b, err := os.ReadFile(filepath.Join(q.dir, name)) //nolint:gosec // name checked against reID
	if err != nil {
		return r, err
	}
	if len(b) > 4*crash.MaxText {
		return r, errors.New("crashq: oversized report")
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.ID+".json" != name {
		return r, errors.New("crashq: id mismatch")
	}
	return r, nil
}

// InstallSelf queues the traceback a previous veilond run left behind (if
// any), then makes the Go runtime write this process's fatal errors to
// the same file. The file holds raw text only between a crash and the
// next start, inside the 0700 state directory.
func (q *Queue) InstallSelf() (queued bool, err error) {
	p := filepath.Join(q.dir, selfTrace)
	if f, err := os.Open(p); err == nil { //nolint:gosec // fixed name in the state dir
		b, _ := io.ReadAll(io.LimitReader(f, 4*crash.MaxText))
		_ = f.Close()
		if tb, ok := crash.FromGoOutput(string(b)); ok {
			_, queued, _ = q.Add(crash.ComponentVeilond, crash.KindPanic, "", tb)
		}
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // fixed name in the state dir
	if err != nil {
		return queued, err
	}
	// The runtime duplicates the descriptor: f can be closed here.
	err = debug.SetCrashOutput(f, debug.CrashOptions{})
	_ = f.Close()
	return queued, err
}

func writeFile0600(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
