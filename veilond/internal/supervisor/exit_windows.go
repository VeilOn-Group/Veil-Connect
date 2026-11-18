//go:build windows

package supervisor

import (
	"errors"
	"os/exec"
)

// Windows reports a crash as an NTSTATUS exit code.
var faultCodes = map[uint32]string{
	0xC0000005: "EXCEPTION_ACCESS_VIOLATION",
	0xC000001D: "EXCEPTION_ILLEGAL_INSTRUCTION",
	0xC0000094: "EXCEPTION_INT_DIVIDE_BY_ZERO",
	0xC00000FD: "EXCEPTION_STACK_OVERFLOW",
	0xC0000409: "STATUS_STACK_BUFFER_OVERRUN",
	0xC0000374: "STATUS_HEAP_CORRUPTION",
	0x80000003: "EXCEPTION_BREAKPOINT",
}

// faultName returns the exception that ended the process, or "".
func faultName(err error) string {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return ""
	}
	return faultCodes[uint32(ee.ExitCode())] //nolint:gosec // NTSTATUS is the exit code bit pattern
}
