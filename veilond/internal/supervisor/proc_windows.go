//go:build windows

package supervisor

import (
	"os/exec"
	"syscall"
)

// TODO(iter-4): assign the child to a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE (needs golang.org/x/sys/windows) so the
// core dies with the veilond service; until then Stop() kills it explicitly.
func configureProcess(cmd *exec.Cmd) {
	const createNewProcessGroup = 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func terminateGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
