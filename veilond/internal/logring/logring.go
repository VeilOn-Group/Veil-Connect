// Package logring is the client's local log sink (ADR-0011): two files of at
// most MaxBytes each (current + previous), already-redacted input only,
// wipeable on demand and on exit. Logs never leave the device without consent.
package logring

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// DefaultMaxBytes is the per-file limit (2 MiB, total 4 MiB).
const DefaultMaxBytes = 2 << 20

// Ring is an io.Writer with size-bounded rotation.
type Ring struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
	f        *os.File
	size     int64
}

const (
	curName  = "veilond.log"
	prevName = "veilond.log.1"
)

// Open creates the directory (0700) and the current file (0600).
func Open(dir string, maxBytes int64) (*Ring, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	r := &Ring{dir: dir, maxBytes: maxBytes}
	if err := r.openCurrent(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Ring) openCurrent() error {
	f, err := os.OpenFile(filepath.Join(r.dir, curName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

// Write appends p, rotating when the limit would be exceeded.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, errors.New("logring: closed")
	}
	if r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	if int64(len(p)) > r.maxBytes { // never let a single write exceed the cap
		p = p[len(p)-int(r.maxBytes):]
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *Ring) rotate() error {
	_ = r.f.Close()
	cur := filepath.Join(r.dir, curName)
	if err := os.Rename(cur, filepath.Join(r.dir, prevName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return r.openCurrent()
}

// Tail returns up to n last lines (previous file first).
func (r *Ring) Tail(n int) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var lines []string
	for _, name := range []string{prevName, curName} {
		f, err := os.Open(filepath.Join(r.dir, name)) //nolint:gosec // fixed file names in the configured log dir
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			lines = append(lines, sc.Text())
			if len(lines) > n {
				lines = lines[1:]
			}
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return lines, nil
}

// Wipe truncates both files (best effort overwrite is not meaningful on SSDs;
// files contain only redacted data in the first place).
func (r *Ring) Wipe() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		_ = r.f.Close()
	}
	for _, name := range []string{curName, prevName} {
		if err := os.Remove(filepath.Join(r.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return r.openCurrent()
}

// Close closes the current file.
func (r *Ring) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

var _ io.WriteCloser = (*Ring)(nil)
