//go:build windows

package timeout

import (
	"os/exec"
	"syscall"
)

// supportedSignals lists the signal names accepted on Windows.
// Windows does not support POSIX job-control signals. Other accepted signals
// are mapped to process termination by deliverSignal.
var supportedSignals = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT,
	"ALRM": syscall.SIGALRM,
	"FPE":  syscall.SIGFPE,
	"HUP":  syscall.SIGHUP,
	"ILL":  syscall.SIGILL,
	"INT":  syscall.SIGINT,
	"KILL": syscall.SIGKILL,
	"PIPE": syscall.SIGPIPE,
	"QUIT": syscall.SIGQUIT,
	"SEGV": syscall.SIGSEGV,
	"TERM": syscall.SIGTERM,
	"TRAP": syscall.SIGTRAP,
}

// platformState stores Windows-specific signal handling.
type platformState struct {
	signalProcess processSignalFunc
}

// setupProcessGroup does nothing on Windows because POSIX process groups do
// not exist. Timeout can signal only the direct command, not its child processes.
func (state *runnerState) setupProcessGroup() (int, bool) {
	return 0, true
}

// protectFromJobControlStop does nothing on Windows because SIGTTIN and
// SIGTTOU do not exist.
func (state *runnerState) protectFromJobControlStop() {}

// releaseJobControlProtection does nothing on Windows.
func (state *runnerState) releaseJobControlProtection() {}

// deliverSignal terminates the command process on timeout.
//
// Windows cannot send POSIX signals to another process, so every requested
// signal becomes SIGKILL. This affects only the direct command.
func (state *runnerState) deliverSignal(cmd *exec.Cmd, _ syscall.Signal) {
	state.sendProcessSignal(cmd, syscall.SIGKILL)
}
