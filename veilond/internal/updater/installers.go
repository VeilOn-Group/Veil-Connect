package updater

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Command is an installer that runs a fixed program with fixed arguments
// and the verified file path appended (no shell, minimal environment).
type Command struct {
	Program string
	Args    []string
	Env     []string
	Timeout time.Duration
}

// Install implements Installer.
func (c Command) Install(ctx context.Context, path string) error {
	t := c.Timeout
	if t == 0 {
		t = 15 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	args := append(append([]string(nil), c.Args...), path)
	cmd := exec.CommandContext(cctx, c.Program, args...) //nolint:gosec // fixed program and arguments; path is the helper's own verified copy
	cmd.Env = c.Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 600 {
			out = out[len(out)-600:]
		}
		return fmt.Errorf("%s: %w: %s", c.Program, err, out)
	}
	return nil
}

// ForFormat returns the installer for a package format.
func ForFormat(format string) (Installer, error) {
	path := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	switch format {
	case "deb":
		// dpkg keeps conffiles (veilond.yaml) and runs the package scripts
		// (they restart veilond). DEBIAN_FRONTEND avoids prompts.
		return Command{Program: "/usr/bin/dpkg", Args: []string{"--force-confold", "-i"}, Env: append(path, "DEBIAN_FRONTEND=noninteractive")}, nil
	case "rpm":
		return Command{Program: "/usr/bin/rpm", Args: []string{"-U", "--quiet"}, Env: path}, nil
	case "msi":
		return Command{Program: `C:\Windows\System32\msiexec.exe`, Args: []string{"/qn", "/norestart", "/i"}}, nil
	case "pkg":
		// installer -pkg <path> -target /: the path must precede -target,
		// so wrap the arguments.
		return pkgInstaller{}, nil
	}
	return nil, fmt.Errorf("updater: no silent installer for %q", format)
}

type pkgInstaller struct{}

func (pkgInstaller) Install(ctx context.Context, path string) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(cctx, "/usr/sbin/installer", "-pkg", path, "-target", "/").CombinedOutput() //nolint:gosec // fixed program; verified copy
	if err != nil {
		return fmt.Errorf("installer: %w: %s", err, out)
	}
	return nil
}
