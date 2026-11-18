//go:build !windows

package supervisor

import (
	"errors"
	"os/exec"
	"syscall"

	"github.com/veilon/veil-shared/pkg/crash"
)

// faultName returns the fault signal that killed the process, or "".
func faultName(err error) string {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return ""
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	name := signalName(ws.Signal())
	if !crash.FaultSignal(name) {
		return ""
	}
	return name
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGBUS:
		return "SIGBUS"
	case syscall.SIGILL:
		return "SIGILL"
	case syscall.SIGFPE:
		return "SIGFPE"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGTRAP:
		return "SIGTRAP"
	case syscall.SIGSYS:
		return "SIGSYS"
	}
	return ""
}
