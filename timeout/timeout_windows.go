//go:build windows

package timeout

import (
	"os/exec"
	"syscall"
)

// supportedSignals is the Windows subset of the signal table. Windows lacks
// POSIX job-control signals (CONT/STOP/TSTP/TTIN/TTOU/USR1/USR2), so they are
// intentionally absent. Of these, only termination can actually be delivered to
// another process (see deliverSignal); the rest are accepted for option parsing
// compatibility but are mapped to process termination.
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

// platformState carries the Windows signal-delivery seam. Windows has only the
// per-process signaler; there are no POSIX process groups, so the Unix
// process-group signaler is absent.
type platformState struct {
	signalProcess processSignalFunc
}

// setupProcessGroup is a no-op on Windows: there are no POSIX process groups,
// so timeout signals only the direct command process. Descendants the command
// spawns are not tracked. This is a documented portability limitation.
func (state *runnerState) setupProcessGroup() (int, bool) {
	return 0, true
}

// deliverSignal terminates the command process on timeout.
//
// Windows cannot deliver arbitrary POSIX signals to another process;
// os.Process.Signal only honors Kill, which Go translates to TerminateProcess.
// Every requested terminating signal is therefore mapped to termination. This
// affects only the direct command process, not its descendants.
func (state *runnerState) deliverSignal(cmd *exec.Cmd, _ syscall.Signal) {
	state.sendProcessSignal(cmd, syscall.SIGKILL)
}
