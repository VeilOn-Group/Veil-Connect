// Package updater is the privileged half of silent updates.
//
// veilond (unprivileged) downloads and verifies an update, then drops three
// files into the update inbox, a directory the helper's service watches:
//
//	keyset.json   the root-signed keyset (sign.Envelope)
//	release.json  the release metadata signed offline (sign.Envelope)
//	artifact      the installer package
//	request       marker, written last
//
// The helper runs as root (systemd path unit, launchd WatchPaths, Windows
// service) and trusts NOTHING from the inbox: it verifies the keyset with
// the root keys from its own root-owned config, the release metadata with
// that keyset, the platform and package format against its own build, the
// version against its own (installed) version and its last seen keyset
// version (no downgrade), and the artifact's size and SHA-256 on a private
// copy it made itself (no time-of-check/time-of-use race). Only then does it
// run the platform installer with a fixed argument list on that copy.
//
// Whoever can write to the inbox can therefore make the helper install
// only a newer, offline-signed VeilOn release: the same thing the user
// could do by hand. That is why the helper adds no local privilege
// escalation, and why it has no other inputs.
package updater

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/veilon/veil-shared/pkg/release"
	"github.com/veilon/veil-shared/pkg/sign"
)

// Inbox file names.
const (
	FileKeyset   = "keyset.json"
	FileRelease  = "release.json"
	FileArtifact = "artifact"
	FileRequest  = "request"
)

// maxEnvelope bounds the JSON files read from the inbox.
const maxEnvelope = 256 << 10

// Errors.
var (
	ErrRejected = errors.New("updater: update rejected")
)

// Installer runs the platform installer on a verified file.
type Installer interface {
	Install(ctx context.Context, path string) error
}

// Config of the helper. Everything here comes from root-owned files or the
// helper's own build, never from the inbox.
type Config struct {
	Inbox          string // watched directory (group-writable by the veilond account)
	WorkDir        string // root-only directory for the private copy and state
	RootKeys       []sign.PublicKey
	Platform       string // release.Platforms value of this build
	CurrentVersion string // version of the installed package (= this helper's build)
	Installer      Installer
	Now            func() time.Time
	Logger         *slog.Logger
}

// Result describes one ApplyOnce run.
type Result struct {
	Requested bool
	Installed string // version installed, "" if none
}

func (c *Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Config) stateFile() string { return filepath.Join(c.WorkDir, "keyset_version") }

func (c *Config) minKeyset() int64 {
	b, err := os.ReadFile(c.stateFile())
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v
}

// ApplyOnce processes the inbox once. The inbox is emptied whatever the
// outcome, so a bad request cannot make the service loop.
func (c *Config) ApplyOnce(ctx context.Context) (Result, error) {
	var res Result
	if _, err := os.Lstat(filepath.Join(c.Inbox, FileRequest)); err != nil {
		return res, nil
	}
	res.Requested = true
	defer c.clean()
	if err := os.MkdirAll(c.WorkDir, 0o700); err != nil {
		return res, err
	}
	var ksEnv, relEnv sign.Envelope
	if err := c.readJSON(FileKeyset, &ksEnv); err != nil {
		return res, err
	}
	if err := c.readJSON(FileRelease, &relEnv); err != nil {
		return res, err
	}
	ks, err := sign.VerifyKeyset(sign.NewVerifier(c.RootKeys, c.now), &ksEnv, c.minKeyset())
	if err != nil {
		return res, fmt.Errorf("%w: keyset: %w", ErrRejected, err)
	}
	meta, err := release.Verify(ks, &relEnv, c.now)
	if err != nil {
		return res, fmt.Errorf("%w: release metadata: %w", ErrRejected, err)
	}
	if meta.Platform != c.Platform {
		return res, fmt.Errorf("%w: platform %s, this installation is %s", ErrRejected, meta.Platform, c.Platform)
	}
	if !release.Newer(c.CurrentVersion, meta.Version) {
		return res, fmt.Errorf("%w: version %s is not newer than %s", ErrRejected, meta.Version, c.CurrentVersion)
	}
	copyPath, err := c.privateCopy(meta)
	if err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(copyPath) }()
	if err := c.Installer.Install(ctx, copyPath); err != nil {
		return res, fmt.Errorf("updater: install: %w", err)
	}
	_ = os.WriteFile(c.stateFile(), []byte(strconv.FormatInt(ks.Version, 10)), 0o600)
	res.Installed = meta.Version
	if c.Logger != nil {
		c.Logger.Warn("update installed", "version", meta.Version)
	}
	return res, nil
}

// openRegular opens an inbox file without following symlinks and checks it
// is a regular file.
func (c *Config) openRegular(name string) (*os.File, os.FileInfo, error) {
	p := filepath.Join(c.Inbox, name)
	st, err := os.Lstat(p)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s missing", ErrRejected, name)
	}
	if !st.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: %s is not a regular file", ErrRejected, name)
	}
	f, err := openNoFollow(p)
	if err != nil {
		return nil, nil, err
	}
	st2, err := f.Stat()
	if err != nil || !os.SameFile(st, st2) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: %s changed while opening", ErrRejected, name)
	}
	return f, st2, nil
}

func (c *Config) readJSON(name string, v any) error {
	f, _, err := c.openRegular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxEnvelope+1))
	if err != nil {
		return err
	}
	if len(b) > maxEnvelope {
		return fmt.Errorf("%w: %s too large", ErrRejected, name)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrRejected, name, err)
	}
	return nil
}

// privateCopy copies the artifact into the root-only work dir while
// hashing it; the installer only ever sees this copy.
func (c *Config) privateCopy(meta *release.Metadata) (string, error) {
	src, st, err := c.openRegular(FileArtifact)
	if err != nil {
		return "", err
	}
	defer src.Close()
	if st.Size() != meta.Size {
		return "", fmt.Errorf("%w: artifact size %d, signed size %d", ErrRejected, st.Size(), meta.Size)
	}
	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	dst := filepath.Join(c.WorkDir, "update-"+hex.EncodeToString(rnd)+"."+release.FormatOf(meta.Platform))
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // root-only work dir, random name
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(src, meta.Size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && (n != meta.Size || hex.EncodeToString(h.Sum(nil)) != meta.SHA256) {
		err = fmt.Errorf("%w: artifact does not match the signed SHA-256", ErrRejected)
	}
	if err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return dst, nil
}

func (c *Config) clean() {
	for _, n := range []string{FileRequest, FileArtifact, FileRelease, FileKeyset} {
		_ = os.Remove(filepath.Join(c.Inbox, n))
	}
}

// Submit is veilond's side: it places a verified download into the inbox.
// Files are written under temporary names and renamed; the request marker
// comes last, so the helper never sees a half-written request.
func Submit(inbox string, keyset, rel []byte, artifact string) error {
	for _, n := range []string{FileRequest, FileArtifact, FileRelease, FileKeyset} {
		_ = os.Remove(filepath.Join(inbox, n))
	}
	put := func(name string, data []byte) error {
		tmp := filepath.Join(inbox, "."+name+".tmp")
		_ = os.Remove(tmp)
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(inbox, name))
	}
	if err := put(FileKeyset, keyset); err != nil {
		return err
	}
	if err := put(FileRelease, rel); err != nil {
		return err
	}
	src, err := os.Open(artifact) //nolint:gosec // veilond's own verified download
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := filepath.Join(inbox, "."+FileArtifact+".tmp")
	_ = os.Remove(tmp)
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // fixed name inside the configured inbox
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(inbox, FileArtifact)); err != nil {
		return err
	}
	return put(FileRequest, []byte("1\n"))
}
