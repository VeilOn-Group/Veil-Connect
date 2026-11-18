// Package config loads veilond configuration (YAML + env override of the
// path only). The file contains no secrets: root keys are public keys.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/veilon/veil-shared/pkg/sign"
)

// Config is veilond's configuration.
type Config struct {
	RuntimeDir string `yaml:"runtime_dir"` // tmpfs; holds the transient core config
	StateDir   string `yaml:"state_dir"`   // 0700; tokens, device key, cached profile
	LogDir     string `yaml:"log_dir"`

	SingBox struct {
		Path          string `yaml:"path"`
		SHA256        string `yaml:"sha256"`
		AllowUnpinned bool   `yaml:"allow_unpinned"` // dev builds only
		// ExtraFiles pins files the core loads at runtime, e.g. libcronet
		// for the in-tree naive outbound: path -> sha256 (hex).
		ExtraFiles map[string]string `yaml:"extra_files"`
	} `yaml:"singbox"`

	// Naive selects how the naive transport runs (ADR-0002, ADR-0052).
	Naive struct {
		Mode   string `yaml:"mode"` // in_tree (default) | external
		Path   string `yaml:"path"` // external: official naive binary
		SHA256 string `yaml:"sha256"`
	} `yaml:"naive"`

	// Xray runs the VLESS transports, XHTTP and Vision (ADR-0066): the
	// official release binary, pinned. Empty path: VLESS is not offered.
	Xray struct {
		Path   string `yaml:"path"`
		SHA256 string `yaml:"sha256"`
	} `yaml:"xray"`

	// KillSwitch: firewall state file (Windows keeps the previous policy).
	KillSwitch struct {
		Disabled bool `yaml:"disabled"` // hide/disable the feature (e.g. unsupported OS)
	} `yaml:"kill_switch"`

	API struct {
		BaseURLs       []string `yaml:"base_urls"`
		SPKIPins       []string `yaml:"spki_pins"`
		AllowUnpinned  bool     `yaml:"allow_unpinned"`   // dev builds only
		AllowPlainHTTP bool     `yaml:"allow_plain_http"` // dev builds only
		TimeoutSeconds int      `yaml:"timeout_seconds"`
	} `yaml:"api"`

	Trust struct {
		RootKeys []RootKey `yaml:"root_keys"`
	} `yaml:"trust"`

	// LocalAPI is the UI control socket (ADR-0006, ADR-0060). Whoever may
	// connect may control the VPN (connect, disconnect, settings, account),
	// never more: no endpoint takes a file path or command.
	LocalAPI struct {
		Socket string `yaml:"socket"`
		// SocketGID (unix): group owning the socket; its members may connect.
		SocketGID int `yaml:"socket_gid"`
		// AllowedUIDs (unix): extra users allowed (still need file access).
		AllowedUIDs []int `yaml:"allowed_uids"`
		// AllowedGroups: unix group names/ids, Windows group or account
		// names or SIDs (e.g. the installer-created "VeilOn Users").
		AllowedGroups []string `yaml:"allowed_groups"`
		TCPFallback   string   `yaml:"tcp_fallback"` // dev builds only: "127.0.0.1:0"
		TokenFile     string   `yaml:"token_file"`
	} `yaml:"local_api"`

	// Update: silent installation through the privileged helper
	// veilon-updater (ADR-0069). Empty inbox: updates are only downloaded
	// and the user starts the installer.
	Update struct {
		Inbox   string `yaml:"inbox"`    // shared with the helper (root:veilon 0770)
		WorkDir string `yaml:"work_dir"` // helper only (root 0700)
	} `yaml:"update"`

	// Billing: hosts a payment confirmation URL may point to (default
	// core.DefaultPaymentHosts). The URL is opened in the system browser.
	Billing struct {
		PaymentHosts []string `yaml:"payment_hosts"`
	} `yaml:"billing"`

	TunStack string `yaml:"tun_stack"` // system | gvisor | mixed

	// ProfileRefreshMinutes: how often the signed profile is refreshed to
	// pick up node changes made in the panel (ADR-0062). Default 60.
	ProfileRefreshMinutes int `yaml:"profile_refresh_minutes"`
}

// RootKey is a pinned offline root public key (verifies keysets).
type RootKey struct {
	ID  string `yaml:"id"`
	Key string `yaml:"key"` // base64 std or url
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.UnmarshalWithOptions(b, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.Validate(DevBuild); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate checks the config. dev relaxes pinning rules (dev builds only).
func (c *Config) Validate(dev bool) error {
	var errs []error
	req := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("config: %s is required", name))
		}
	}
	req("runtime_dir", c.RuntimeDir)
	req("state_dir", c.StateDir)
	req("log_dir", c.LogDir)
	req("singbox.path", c.SingBox.Path)
	req("local_api.socket", c.LocalAPI.Socket)
	if c.SingBox.SHA256 == "" && (!dev || !c.SingBox.AllowUnpinned) {
		errs = append(errs, errors.New("config: singbox.sha256 is required (unpinned core only in dev builds)"))
	}
	for p, h := range c.SingBox.ExtraFiles {
		if len(h) != 64 {
			errs = append(errs, fmt.Errorf("config: singbox.extra_files[%s]: sha256 must be 64 hex chars", p))
		}
	}
	switch c.Naive.Mode {
	case "", "in_tree":
	case "external":
		req("naive.path", c.Naive.Path)
		if c.Naive.SHA256 == "" && (!dev || !c.SingBox.AllowUnpinned) {
			errs = append(errs, errors.New("config: naive.sha256 is required in external mode"))
		}
	default:
		errs = append(errs, fmt.Errorf("config: naive.mode %q (in_tree|external)", c.Naive.Mode))
	}
	if c.Xray.Path != "" && c.Xray.SHA256 == "" && (!dev || !c.SingBox.AllowUnpinned) {
		errs = append(errs, errors.New("config: xray.sha256 is required (unpinned core only in dev builds)"))
	}
	if len(c.API.BaseURLs) == 0 {
		errs = append(errs, errors.New("config: api.base_urls is required"))
	}
	for _, u := range c.API.BaseURLs {
		if strings.HasPrefix(u, "http://") && (!dev || !c.API.AllowPlainHTTP) {
			errs = append(errs, fmt.Errorf("config: %s: plain HTTP only in dev builds", u))
		}
	}
	if len(c.API.SPKIPins) < 2 && (!dev || !c.API.AllowUnpinned) {
		errs = append(errs, errors.New("config: api.spki_pins needs at least 2 pins (current + backup)"))
	}
	if !dev && (c.SingBox.AllowUnpinned || c.API.AllowUnpinned || c.API.AllowPlainHTTP) {
		errs = append(errs, errors.New("config: allow_* flags are rejected in release builds"))
	}
	if len(c.Trust.RootKeys) == 0 {
		errs = append(errs, errors.New("config: trust.root_keys is required"))
	}
	if _, err := c.RootPublicKeys(); err != nil {
		errs = append(errs, err)
	}
	if c.LocalAPI.TCPFallback != "" && !dev {
		errs = append(errs, errors.New("config: local_api.tcp_fallback is dev-only (any local process can reach a loopback port)"))
	}
	for _, u := range c.LocalAPI.AllowedUIDs {
		if u < 0 {
			errs = append(errs, fmt.Errorf("config: local_api.allowed_uids: bad uid %d", u))
		}
	}
	if c.LocalAPI.SocketGID < 0 {
		errs = append(errs, errors.New("config: local_api.socket_gid must be >= 0"))
	}
	if c.LocalAPI.TCPFallback != "" && !strings.HasPrefix(c.LocalAPI.TCPFallback, "127.0.0.1:") {
		errs = append(errs, errors.New("config: local_api.tcp_fallback must be on 127.0.0.1"))
	}
	if c.LocalAPI.TCPFallback != "" && c.LocalAPI.TokenFile == "" {
		errs = append(errs, errors.New("config: local_api.token_file required with tcp_fallback"))
	}
	if c.ProfileRefreshMinutes == 0 {
		c.ProfileRefreshMinutes = 60
	}
	if c.ProfileRefreshMinutes < 1 || c.ProfileRefreshMinutes > 24*60 {
		errs = append(errs, errors.New("config: profile_refresh_minutes must be 1..1440"))
	}
	if c.API.TimeoutSeconds == 0 {
		c.API.TimeoutSeconds = 20
	}
	return errors.Join(errs...)
}

// RootPublicKeys decodes the pinned root keys.
func (c *Config) RootPublicKeys() ([]sign.PublicKey, error) {
	var out []sign.PublicKey
	for _, k := range c.Trust.RootKeys {
		raw, err := base64.StdEncoding.DecodeString(k.Key)
		if err != nil {
			raw, err = base64.RawURLEncoding.DecodeString(k.Key)
		}
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("config: root key %q invalid", k.ID)
		}
		out = append(out, sign.PublicKey{ID: k.ID, Purpose: sign.PurposeKeyset, Key: raw})
	}
	return out, nil
}
