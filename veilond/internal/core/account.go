package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/veilon/veil-shared/pkg/release"
	"github.com/veilon/veil-shared/pkg/sign"
	"github.com/veilon/veilon-app/veilond/internal/trustpath"
	"github.com/veilon/veilon-app/veilond/internal/updater"
)

// AccountAPI is the optional part of the server API used for data rights
// and self-update (implemented by apiclient.Client).
type AccountAPI interface {
	ExportAccount(ctx context.Context, access string) (json.RawMessage, error)
	DeleteAccount(ctx context.Context, access string) error
	LatestRelease(ctx context.Context, platform, channel string) (*sign.Envelope, error)
}

// ErrUnsupported is returned when the API client lacks a capability.
var ErrUnsupported = errors.New("core: not supported by this API client")

func (m *Manager) accountAPI() (AccountAPI, error) {
	a, ok := m.api.(AccountAPI)
	if !ok {
		return nil, ErrUnsupported
	}
	return a, nil
}

// ExportAccount returns the server-side data export (shown to the user, not
// stored by veilond).
func (m *Manager) ExportAccount(ctx context.Context) (json.RawMessage, error) {
	a, err := m.accountAPI()
	if err != nil {
		return nil, err
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	return a.ExportAccount(ctx, access)
}

// DeleteAccount disconnects, erases the account on the server and wipes the
// local session.
func (m *Manager) DeleteAccount(ctx context.Context) error {
	a, err := m.accountAPI()
	if err != nil {
		return err
	}
	access, err := m.accessToken(ctx)
	if err != nil {
		return err
	}
	if err := m.Disconnect(ctx); err != nil {
		return err
	}
	if err := a.DeleteAccount(ctx, access); err != nil {
		return err
	}
	m.mu.Lock()
	m.prof = nil
	m.mu.Unlock()
	return m.store.ClearSession()
}

// ---------- self-update (ADR-0056) ----------

// Platform is this build's release platform name.
func Platform() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin-universal"
	case "windows":
		return runtime.GOOS + "-" + runtime.GOARCH
	case "linux":
		if PackageFormat == "deb" || PackageFormat == "rpm" {
			return "linux-" + runtime.GOARCH + "-" + PackageFormat
		}
		return "linux-" + runtime.GOARCH
	}
	return ""
}

// UpdateInfo is shown in the UI.
type UpdateInfo struct {
	Current    string `json:"current"`
	Available  bool   `json:"available"`
	Version    string `json:"version,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Mandatory  bool   `json:"mandatory,omitempty"`
	Downloaded bool   `json:"downloaded"`
	Path       string `json:"path,omitempty"`
	// SilentInstall: the privileged helper can install it (ADR-0069).
	SilentInstall bool `json:"silent_install"`
	Installing    bool `json:"installing,omitempty"`
}

type updateState struct {
	mu   sync.Mutex
	meta *release.Metadata
	env  *sign.Envelope // signed metadata, handed to the helper as is
	path string
}

// PackageFormat is set at build time by the Linux packages ("deb", "rpm"):
// -ldflags "-X github.com/veilon/veilon-app/veilond/internal/core.PackageFormat=deb".
// It selects the release platform the silent updater can install (ADR-0069).
var PackageFormat = ""

// CheckUpdate fetches signed release metadata and verifies it against the
// cached, root-verified keyset. Unsigned or downgraded metadata is ignored.
func (m *Manager) CheckUpdate(ctx context.Context, channel string) (*UpdateInfo, error) {
	a, err := m.accountAPI()
	if err != nil {
		return nil, err
	}
	if channel == "" {
		channel = "stable"
	}
	info := &UpdateInfo{Current: m.opt.ClientVersion}
	env, err := a.LatestRelease(ctx, Platform(), channel)
	if err != nil || env == nil {
		return info, err
	}
	rec, err := m.store.LoadKeyset()
	if err != nil {
		return nil, ErrNoProfile
	}
	ks, err := sign.VerifyKeyset(sign.NewVerifier(m.opt.RootKeys, m.opt.Now), rec.Envelope, rec.Version)
	if err != nil {
		return nil, err
	}
	meta, err := release.Verify(ks, env, m.opt.Now)
	if err != nil {
		return nil, fmt.Errorf("core: release metadata rejected: %w", err)
	}
	if meta.Platform != Platform() || !release.Newer(m.opt.ClientVersion, meta.Version) {
		return info, nil
	}
	m.upd.mu.Lock()
	if m.upd.meta == nil || m.upd.meta.Version != meta.Version {
		m.upd.meta, m.upd.path = meta, ""
	}
	m.upd.env = env
	info.Downloaded, info.Path = m.upd.path != "", m.upd.path
	m.upd.mu.Unlock()
	info.Available, info.Version, info.Notes, info.Size, info.Mandatory = true, meta.Version, meta.Notes, meta.Size, meta.Mandatory
	info.SilentInstall = m.opt.UpdateInbox != ""
	return info, nil
}

// ErrUpdateHash is returned when a download does not match signed metadata.
var ErrUpdateHash = errors.New("core: update does not match signed hash; discarded")

// DownloadUpdate downloads the artifact of the last verified metadata and
// checks size and SHA-256 before keeping it. Installing is left to the OS
// installer the user starts (TODO(iter-5): silent per-platform install).
func (m *Manager) DownloadUpdate(ctx context.Context) (*UpdateInfo, error) {
	m.upd.mu.Lock()
	meta := m.upd.meta
	m.upd.mu.Unlock()
	if meta == nil {
		return nil, errors.New("core: no verified update; check first")
	}
	dir := filepath.Join(m.opt.RuntimeDir, "update")
	// Readable by the unprivileged UI (it opens the installer), writable
	// only by veilond: a pre-created or loose directory is refused, so the
	// download below can never be redirected by a planted symlink.
	if err := trustpath.EnsureDir(dir, trustpath.DirOptions{Perm: 0o755}); err != nil {
		return nil, err
	}
	name := filepath.Base(meta.URL)
	if name == "." || name == "/" || name == "" {
		name = "veilon-update"
	}
	final := filepath.Join(dir, name)
	tmp := final + ".part"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return nil, err
	}
	hc := m.opt.UpdateHTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("core: update download: HTTP %d", resp.StatusCode)
	}
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // public installer
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, meta.Size+1))
	_ = f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if n != meta.Size || hex.EncodeToString(h.Sum(nil)) != meta.SHA256 {
		_ = os.Remove(tmp)
		return nil, ErrUpdateHash
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	m.upd.mu.Lock()
	m.upd.path = final
	m.upd.mu.Unlock()
	return &UpdateInfo{Current: m.opt.ClientVersion, Available: true, Version: meta.Version, Notes: meta.Notes, Size: meta.Size, Downloaded: true, Path: final,
		SilentInstall: m.opt.UpdateInbox != ""}, nil
}

// ErrNoSilentInstall means this installation has no update helper.
var ErrNoSilentInstall = errors.New("core: silent install is not available on this installation")

// InstallUpdate hands the downloaded, verified update to the privileged
// helper veilon-updater through its inbox (ADR-0069). The helper verifies
// everything again with its own root keys and installs the package; the
// package scripts then restart veilond.
func (m *Manager) InstallUpdate(_ context.Context) (*UpdateInfo, error) {
	if m.opt.UpdateInbox == "" {
		return nil, ErrNoSilentInstall
	}
	m.upd.mu.Lock()
	meta, env, path := m.upd.meta, m.upd.env, m.upd.path
	m.upd.mu.Unlock()
	if meta == nil || env == nil || path == "" {
		return nil, errors.New("core: no downloaded update; download first")
	}
	rec, err := m.store.LoadKeyset()
	if err != nil {
		return nil, ErrNoProfile
	}
	ks, err := json.Marshal(rec.Envelope)
	if err != nil {
		return nil, err
	}
	rel, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	if err := updater.Submit(m.opt.UpdateInbox, ks, rel, path); err != nil {
		return nil, fmt.Errorf("core: update inbox: %w", err)
	}
	m.opt.Logger.Info("update handed to the installer helper", "version", meta.Version)
	return &UpdateInfo{Current: m.opt.ClientVersion, Available: true, Version: meta.Version, Size: meta.Size,
		Downloaded: true, Path: path, SilentInstall: true, Installing: true}, nil
}
