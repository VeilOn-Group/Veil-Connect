package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func valid() *Config {
	c := &Config{RuntimeDir: "/run/veilon", StateDir: "/var/lib/veilon", LogDir: "/var/lib/veilon/logs"}
	c.SingBox.Path = "/usr/lib/veilon/sing-box"
	c.SingBox.SHA256 = strings.Repeat("a", 64)
	c.API.BaseURLs = []string{"https://api.example.net"}
	c.API.SPKIPins = []string{"pin-a", "pin-b"}
	c.Trust.RootKeys = []RootKey{{ID: "root-1", Key: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	c.LocalAPI.Socket = "/run/veilon/veilond.sock"
	return c
}

func TestReleaseRejectsDevFlags(t *testing.T) {
	if err := valid().Validate(false); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for name, mut := range map[string]func(*Config){
		"unpinned core": func(c *Config) { c.SingBox.SHA256 = ""; c.SingBox.AllowUnpinned = true },
		"plain http":    func(c *Config) { c.API.BaseURLs = []string{"http://127.0.0.1:8080"}; c.API.AllowPlainHTTP = true },
		"one pin":       func(c *Config) { c.API.SPKIPins = []string{"only"} },
		"public tcp":    func(c *Config) { c.LocalAPI.TCPFallback = "0.0.0.0:7000"; c.LocalAPI.TokenFile = "/x" },
		"bad root":      func(c *Config) { c.Trust.RootKeys[0].Key = "short" },
		"loopback tcp":  func(c *Config) { c.LocalAPI.TCPFallback = "127.0.0.1:0"; c.LocalAPI.TokenFile = "/x" },
		"negative uid":  func(c *Config) { c.LocalAPI.AllowedUIDs = []int{-1} },
	} {
		c := valid()
		mut(c)
		if err := c.Validate(false); err == nil {
			t.Errorf("%s: release build must reject", name)
		}
	}
	c := valid()
	c.SingBox.SHA256, c.SingBox.AllowUnpinned = "", true
	if err := c.Validate(true); err != nil {
		t.Errorf("dev build should allow unpinned core: %v", err)
	}
	c = valid()
	c.LocalAPI.TCPFallback, c.LocalAPI.TokenFile = "127.0.0.1:0", "/x"
	if err := c.Validate(true); err != nil {
		t.Errorf("dev build should allow the loopback fallback: %v", err)
	}
}
