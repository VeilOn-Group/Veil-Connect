// Package state persists veilond's local data in a 0700 directory with 0600
// files and atomic writes. Contents (data-inventory: client.keychain,
// client.profile_cache): device key seed, tokens, account number, signed
// profile/keyset, UI settings. No usage history of any kind.
//
// TODO(iter-4): move secrets (device key, tokens, account number) to the OS
// keychain (Windows DPAPI/Credential Manager, macOS Keychain, libsecret).
package state

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/veilon/veil-shared/pkg/sign"
)

// Store is the local state directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open creates the directory if needed and enforces 0700.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // directory: 0700 is the strictest usable mode
		return nil, err
	}
	return &Store{dir: dir}, nil
}

const (
	fDevice   = "device.key"
	fTokens   = "tokens.json"
	fAccount  = "account.json"
	fProfile  = "profile.json"
	fKeyset   = "keyset.json"
	fSettings = "settings.json"
	fResolved = "resolved.json"
)

// ErrNotFound is returned for missing items.
var ErrNotFound = errors.New("state: not found")

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func (s *Store) write(name string, data []byte) error {
	tmp, err := os.CreateTemp(s.dir, "."+name+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path(name))
}

func (s *Store) read(name string) ([]byte, error) {
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Store) writeJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(name, b)
}

func (s *Store) readJSON(name string, v any) error {
	b, err := s.read(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// DeviceKey returns the device signing key, creating it on first use.
func (s *Store) DeviceKey() (ed25519.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.read(fDevice)
	if err == nil {
		if len(b) != ed25519.SeedSize {
			return nil, fmt.Errorf("state: corrupt device key")
		}
		return ed25519.NewKeyFromSeed(b), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := s.write(fDevice, seed); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// Tokens are the API session tokens.
type Tokens struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	AccessExp int64  `json:"access_exp"` // unix seconds, client-side only
}

func (s *Store) SaveTokens(t Tokens) error { return s.writeJSON(fTokens, t) }
func (s *Store) LoadTokens() (Tokens, error) {
	var t Tokens
	return t, s.readJSON(fTokens, &t)
}

// SaveAccountNumber stores the anonymous account number (shown to the user).
func (s *Store) SaveAccountNumber(n string) error {
	return s.writeJSON(fAccount, map[string]string{"number": n})
}

// AccountNumber returns the stored number.
func (s *Store) AccountNumber() (string, error) {
	var m map[string]string
	if err := s.readJSON(fAccount, &m); err != nil {
		return "", err
	}
	return m["number"], nil
}

// SaveProfile stores the signed envelope (verified again on every load).
func (s *Store) SaveProfile(env *sign.Envelope) error { return s.writeJSON(fProfile, env) }
func (s *Store) LoadProfile() (*sign.Envelope, error) {
	var e sign.Envelope
	return &e, s.readJSON(fProfile, &e)
}

// KeysetRecord keeps the signed keyset and its version (downgrade protection).
type KeysetRecord struct {
	Envelope *sign.Envelope `json:"envelope"`
	Version  int64          `json:"version"`
}

func (s *Store) SaveKeyset(r KeysetRecord) error { return s.writeJSON(fKeyset, r) }
func (s *Store) LoadKeyset() (KeysetRecord, error) {
	var r KeysetRecord
	return r, s.readJSON(fKeyset, &r)
}

// Settings are user preferences (no history).
type Settings struct {
	ClearLogsOnExit   bool            `json:"clear_logs_on_exit"`
	AllowLAN          bool            `json:"allow_lan"`
	NodeID            string          `json:"node_id"`
	DisabledTransport map[string]bool `json:"disabled_transports"`
	ManualTCP         string          `json:"manual_tcp"`
	ManualUDP         string          `json:"manual_udp"`
	Language          string          `json:"language"`
	// CrashReports sends sanitized crash reports automatically (opt-in,
	// off by default, ADR-0077). Off: reports wait on the device for a
	// manual "Send" and expire after 30 days.
	CrashReports bool `json:"crash_reports"`
	// KillSwitch blocks all non-tunnel traffic at the OS firewall while
	// connected or reconnecting; released only by an explicit disconnect.
	KillSwitch bool `json:"kill_switch"`
	// AutoSwitch lets the hysteresis controller change transports (ADR-0004).
	// A manual transport selection turns it off until re-enabled.
	AutoSwitch bool `json:"auto_switch"`
	// SplitRules are the user's split-tunnelling rules (ADR-0054).
	SplitRules []SplitRule `json:"split_rules"`
	// BypassRU sends the .ru and .su zones around the VPN (automatic split
	// tunnelling, ADR-0064). nil (settings saved by older versions) means
	// the default: on.
	BypassRU *bool `json:"bypass_ru,omitempty"`
	// DNS is the DoH resolver used through the tunnel (ADR-0073). nil
	// (older settings, older UIs) means the default: Quad9.
	DNS *DNS `json:"dns,omitempty"`
	// AutoConnect connects when veilond starts (system boot), with the
	// last location (ADR-0076). nil (older settings, older UIs): off.
	AutoConnect *bool `json:"auto_connect,omitempty"`
}

// AutoConnectEnabled reports the effective auto-connect switch.
func (s Settings) AutoConnectEnabled() bool { return s.AutoConnect != nil && *s.AutoConnect }

// DNS is the user's resolver choice: provider quad9 | cloudflare | google |
// adguard | opendns | custom, and the URL for custom.
type DNS struct {
	Provider  string `json:"provider"`
	CustomURL string `json:"custom_url,omitempty"`
}

// DNSOrDefault returns the effective choice.
func (s Settings) DNSOrDefault() DNS {
	if s.DNS == nil || s.DNS.Provider == "" {
		return DNS{Provider: "quad9"}
	}
	return *s.DNS
}

// BypassRUEnabled reports the effective automatic split-tunnelling switch.
func (s Settings) BypassRUEnabled() bool { return s.BypassRU == nil || *s.BypassRU }

// SplitRule routes matching traffic directly, through the VPN, or blocks it.
type SplitRule struct {
	Kind   string `json:"kind"`   // domain | domain_suffix | ip | app
	Value  string `json:"value"`  // e.g. "bank.example.rs", "192.0.2.0/24", "telegram.exe"
	Action string `json:"action"` // direct | proxy | block
}

// DefaultSettings are privacy-first defaults.
func DefaultSettings() Settings {
	on := true
	return Settings{ClearLogsOnExit: true, AllowLAN: true, AutoSwitch: true, DisabledTransport: map[string]bool{}, Language: "ru", BypassRU: &on}
}

// LoadSettings returns stored settings or defaults.
func (s *Store) LoadSettings() Settings {
	st := DefaultSettings()
	if err := s.readJSON(fSettings, &st); err != nil {
		return DefaultSettings()
	}
	if st.DisabledTransport == nil {
		st.DisabledTransport = map[string]bool{}
	}
	return st
}

func (s *Store) SaveSettings(st Settings) error { return s.writeJSON(fSettings, st) }

// ClearSession removes everything tied to the account (logout). The device
// key is rotated too, so the next login looks like a new device.
func (s *Store) ClearSession() error {
	var errs []error
	for _, f := range []string{fTokens, fAccount, fProfile, fKeyset, fDevice, fResolved} {
		if err := os.Remove(s.path(f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SaveResolved stores the last known addresses of node and API host names
// (the same names the cached profile already contains). Used only when the
// kill switch blocks the system resolver after a daemon restart (ADR-0053).
func (s *Store) SaveResolved(m map[string][]string) error { return s.writeJSON(fResolved, m) }

// LoadResolved returns the stored host addresses (empty map if none).
func (s *Store) LoadResolved() map[string][]string {
	m := map[string][]string{}
	if err := s.readJSON(fResolved, &m); err != nil {
		return map[string][]string{}
	}
	return m
}
