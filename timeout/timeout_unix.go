//go:build unix

package timeout

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// signalSuppressionDuration bounds the window during which the monitor ignores
// an echo of its own process-group signal. See shouldSuppress.
const signalSuppressionDuration = 200 * time.Millisecond

// groupSignalFunc is the signature for process-group signal delivery. It is a
// test seam and exists only on Unix-like systems; Windows lacks POSIX process
// groups.
type groupSignalFunc func(int, syscall.Signal)

// platformState carries the Unix signal-delivery seams: the per-process
// signaler shared by all platforms plus the Unix-only process-group signaler.
// jobControlCh keeps the SIGTTIN/SIGTTOU notification alive for the run so the
// monitor is not stopped by a background child's TTY access.
type platformState struct {
	signalProcess processSignalFunc
	signalGroup   groupSignalFunc
	jobControlCh  chan os.Signal
}

// Test seams for external package calls that are hard to exercise directly.
var syscallGetpgid = syscall.Getpgid
var syscallSetpgid = syscall.Setpgid

// jobControlNotify and jobControlStop wrap the os/signal calls that protect the
// monitor from SIGTTIN/SIGTTOU. They are test seams so the activation and
// cleanup can be exercised without mutating process-wide signal state.
var jobControlNotify = func(channel chan<- os.Signal, signals ...os.Signal) {
	signal.Notify(channel, signals...)
}
var jobControlStop = func(channel chan<- os.Signal) {
	signal.Stop(channel)
}

// supportedSignals is the full GNU-compatible signal table on Unix.
var supportedSignals = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT,
	"ALRM": syscall.SIGALRM,
	"CONT": syscall.SIGCONT,
	"HUP":  syscall.SIGHUP,
	"ILL":  syscall.SIGILL,
	"INT":  syscall.SIGINT,
	"KILL": syscall.SIGKILL,
	"PIPE": syscall.SIGPIPE,
	"QUIT": syscall.SIGQUIT,
	"STOP": syscall.SIGSTOP,
	"TERM": syscall.SIGTERM,
	"TSTP": syscall.SIGTSTP,
	"TTIN": syscall.SIGTTIN,
	"TTOU": syscall.SIGTTOU,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
}

// setupProcessGroup puts timeout and the child in one process group in
// background mode so the whole command can be signaled. It returns
// (exitCode, ok); when ok is false, runCommand returns exitCode.
func (state *runnerState) setupProcessGroup() (int, bool) {
	if state.config.Foreground {
		return 0, true
	}

	err := syscallSetpgid(0, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: set process group: %v\n", err)

		return ExitInternalFailure, false
	}

	pgid, err := syscallGetpgid(0)
	if err != nil {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: get process group: %v\n", err)

		return ExitInternalFailure, false
	}

	state.pgid = pgid

	return 0, true
}

// protectFromJobControlStop registers SIGTTIN and SIGTTOU on a monitor-held
// channel in non-foreground mode so the monitor is not stopped when a background
// child touches the controlling TTY. Registering via os/signal (rather than
// signal.Ignore) matters because Go resets its handled signals to their default
// dispositions in the child before exec, so the child does not inherit an
// ignored disposition. Foreground mode keeps the default TTY behavior.
func (state *runnerState) protectFromJobControlStop() {
	if state.config.Foreground {
		return
	}

	channel := make(chan os.Signal, 1)
	jobControlNotify(channel, syscall.SIGTTIN, syscall.SIGTTOU)
	state.jobControlCh = channel
}

// releaseJobControlProtection stops the SIGTTIN/SIGTTOU notification installed by
// protectFromJobControlStop. It is safe to call when protection was never
// activated (foreground mode) and is idempotent.
func (state *runnerState) releaseJobControlProtection() {
	if state.jobControlCh == nil {
		return
	}

	jobControlStop(state.jobControlCh)
	state.jobControlCh = nil
}

// deliverSignal applies the GNU-compatible signal delivery policy.
func (state *runnerState) deliverSignal(cmd *exec.Cmd, sig syscall.Signal) {
	if state.config.Foreground {
		state.sendProcessSignal(cmd, sig)

		return
	}

	// GNU timeout sends the signal both to the monitored process and to the
	// shared process group. The direct signal covers edge cases where the command
	// has changed process groups; SIGCONT below follows the same policy.
	state.sendProcessSignal(cmd, sig)
	state.sendGroupSignal(sig)
	state.suppressed = sig
	state.suppressBy = time.Now().Add(signalSuppressionDuration)

	if shouldResumeAfterSignal(sig) {
		state.sendProcessSignal(cmd, syscall.SIGCONT)
		state.sendGroupSignal(syscall.SIGCONT)
	}
}

func (state *runnerState) sendGroupSignal(sig syscall.Signal) {
	signalGroup := state.signalGroup
	if signalGroup == nil {
		signalGroup = defaultGroupSignal
	}

	signalGroup(state.pgid, sig)
}

func defaultGroupSignal(pgid int, sig syscall.Signal) {
	// GNU timeout treats signal delivery as best effort; the command may have
	// already exited or changed process groups by the time the signal is sent.
	_ = syscall.Kill(-pgid, sig)
}

func shouldResumeAfterSignal(sig syscall.Signal) bool {
	return sig != syscall.SIGKILL && sig != syscall.SIGCONT
}
