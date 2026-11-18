//go:build darwin

package supervisor

import (
	"os/exec"
	"syscall"
)

// macOS has no Pdeathsig; the core is in its own group and veilond (a
// LaunchDaemon, ADR-0007) kills the group on shutdown.
// TODO(iter-4): kqueue NOTE_EXIT watchdog in the child wrapper for veilond crashes.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
