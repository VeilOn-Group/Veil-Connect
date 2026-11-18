package autostart

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var entry = Entry{ID: "net.veilon.app", Name: "VeilOn", Exe: "/opt/Veil On/bin/veil$on", Args: []string{"--hidden"}}

func TestXDGRoundTrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG path")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if on, err := Enabled(entry); err != nil || on {
		t.Fatal(on, err)
	}
	if err := Enable(entry); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "autostart", "net.veilon.app.desktop")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Exec=\"/opt/Veil On/bin/veil\\$on\" \"--hidden\"\n") || !strings.Contains(string(b), "TryExec=/opt/Veil On/bin/veil$on\n") {
		t.Fatalf("%s", b)
	}
	if on, err := Enabled(entry); err != nil || !on {
		t.Fatal("not enabled", err)
	}
	moved := entry
	moved.Exe = "/usr/bin/veilon"
	if on, _ := Enabled(moved); on {
		t.Fatal("an entry for another executable must not count as enabled")
	}
	if err := Disable(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
	if err := Disable(entry); err != nil {
		t.Fatal("disable must be idempotent", err)
	}
}

func TestValidate(t *testing.T) {
	for _, e := range []Entry{
		{ID: "x", Name: "X", Exe: "relative/bin"},
		{ID: "a/b", Name: "X", Exe: "/bin/x"},
		{ID: "x", Name: "X\nExec=/bin/sh", Exe: "/bin/x"},
		{ID: "x", Name: "X", Exe: "/bin/x\nExec=/bin/sh"},
		{ID: "x", Name: "X", Exe: "/bin/x", Args: []string{"a\"b"}},
	} {
		if e.validate() == nil {
			t.Errorf("accepted %+v", e)
		}
	}
	if (Entry{ID: "x", Name: "X", Exe: `C:\Program Files\VeilOn\veilon.exe`}).validate() != nil {
		t.Error("windows path refused")
	}
}

func TestRenderers(t *testing.T) {
	a := launchAgent(Entry{ID: "net.veilon.app", Name: "VeilOn", Exe: "/Applications/VeilOn.app/Contents/MacOS/veilon", Args: []string{"--hidden"}})
	for _, want := range []string{"<string>net.veilon.app</string>", "<string>/Applications/VeilOn.app/Contents/MacOS/veilon</string>", "<string>--hidden</string>", "<key>RunAtLoad</key>\n\t<true/>"} {
		if !strings.Contains(a, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if d := desktopFile(Entry{ID: "x", Name: "VeilOn", Exe: "/usr/bin/veilon"}); !strings.Contains(d, "Exec=\"/usr/bin/veilon\"\n") {
		t.Errorf("desktop entry %s", d)
	}
	if v := runValue(Entry{Exe: `C:\Program Files\VeilOn\veilon.exe`, Args: []string{"--hidden"}}); v != `"C:\Program Files\VeilOn\veilon.exe" --hidden` {
		t.Errorf("run value %s", v)
	}
}
