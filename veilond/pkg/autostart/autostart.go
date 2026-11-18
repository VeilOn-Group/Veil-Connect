// Package autostart registers the desktop UI to start when the user logs
// in (ADR-0076). It is per user and needs no privileges: the UI process
// calls it for the logged-in user. veilond itself is a system service and
// starts at boot independently of this.
//
//   - Linux and other XDG desktops: $XDG_CONFIG_HOME/autostart/<id>.desktop
//     (TryExec lets the desktop skip the entry after an uninstall).
//   - macOS: ~/Library/LaunchAgents/<id>.plist (RunAtLoad).
//   - Windows: HKCU\Software\Microsoft\Windows\CurrentVersion\Run.
package autostart

import (
	"errors"
	"fmt"
	"strings"
)

// Entry describes what to start.
type Entry struct {
	ID   string   // reverse-DNS id, e.g. "net.veilon.app"
	Name string   // display name, e.g. "VeilOn"
	Exe  string   // absolute path of the UI executable
	Args []string // e.g. ["--hidden"]
}

// ErrUnsupported is returned on platforms without a login item mechanism.
var ErrUnsupported = errors.New("autostart: not supported on this platform")

func (e Entry) validate() error {
	if e.ID == "" || strings.ContainsAny(e.ID, "/\\ \t\n\"'") || e.Name == "" || strings.ContainsAny(e.Name, "\n\r") {
		return fmt.Errorf("autostart: bad id or name")
	}
	if !absPath(e.Exe) || strings.ContainsAny(e.Exe, "\n\r\x00") {
		return fmt.Errorf("autostart: executable path must be absolute")
	}
	for _, a := range e.Args {
		if strings.ContainsAny(a, "\n\r\x00\"") {
			return fmt.Errorf("autostart: bad argument")
		}
	}
	return nil
}

func absPath(p string) bool {
	return strings.HasPrefix(p, "/") || (len(p) > 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'))
}

// desktopFile renders an XDG autostart entry. Exec quoting follows the
// Desktop Entry Specification (double quotes; \ " ` $ escaped).
func desktopFile(e Entry) string {
	q := func(s string) string {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
		return `"` + r.Replace(s) + `"`
	}
	exec := q(e.Exe)
	for _, a := range e.Args {
		exec += " " + q(a)
	}
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=" + e.Name + "\n" +
		"Exec=" + exec + "\n" +
		"TryExec=" + e.Exe + "\n" +
		"X-GNOME-Autostart-enabled=true\n" +
		"NoDisplay=true\n" +
		"Terminal=false\n"
}

// launchAgent renders a launchd agent that starts the UI at login.
func launchAgent(e Entry) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("\t<key>Label</key>\n\t<string>" + esc.Replace(e.ID) + "</string>\n")
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range append([]string{e.Exe}, e.Args...) {
		b.WriteString("\t\t<string>" + esc.Replace(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>ProcessType</key>\n\t<string>Interactive</string>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// runValue renders the HKCU Run value: quoted program and arguments.
func runValue(e Entry) string {
	v := `"` + e.Exe + `"`
	for _, a := range e.Args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		v += " " + a
	}
	return v
}
