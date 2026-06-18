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

// signalSuppressionDuration is how long timeout ignores its echoed group signal.
const signalSuppressionDuration = 200 * time.Millisecond

// groupSignalFunc sends a signal to a Unix process group.
type groupSignalFunc func(int, syscall.Signal)

// platformState stores Unix-specific signal handling.
// jobControlCh keeps job-control protection active while the command runs.
type platformState struct {
	signalProcess processSignalFunc
	signalGroup   groupSignalFunc
	jobControlCh  chan os.Signal
}

// These functions can be replaced in tests.
var syscallGetpgid = syscall.Getpgid
var syscallSetpgid = syscall.Setpgid

// These functions can be replaced in tests to avoid changing signal handlers.
var jobControlNotify = func(channel chan<- os.Signal, signals ...os.Signal) {
	signal.Notify(channel, signals...)
}
var jobControlStop = func(channel chan<- os.Signal) {
	signal.Stop(channel)
}

// supportedSignals lists the GNU-compatible signals available on Unix.
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

// setupProcessGroup puts timeout in a process group in background mode.
// The child joins this group, so timeout can signal the whole command.
// If ok is false, runCommand returns exitCode.
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

// protectFromJobControlStop prevents a background command's TTY access from
// stopping timeout. It uses os/signal so the child does not inherit these
// signals as ignored. Foreground mode keeps the normal TTY behavior.
func (state *runnerState) protectFromJobControlStop() {
	if state.config.Foreground {
		return
	}

	channel := make(chan os.Signal, 1)
	jobControlNotify(channel, syscall.SIGTTIN, syscall.SIGTTOU)
	state.jobControlCh = channel
}

// releaseJobControlProtection removes the SIGTTIN and SIGTTOU handlers.
// It is safe to call more than once or when protection was not enabled.
func (state *runnerState) releaseJobControlProtection() {
	if state.jobControlCh == nil {
		return
	}

	jobControlStop(state.jobControlCh)
	state.jobControlCh = nil
}

// deliverSignal sends a signal using GNU timeout-compatible behavior.
func (state *runnerState) deliverSignal(cmd *exec.Cmd, sig syscall.Signal) {
	if state.config.Foreground {
		state.sendProcessSignal(cmd, sig)

		return
	}

	// Signal both the command and its group. The direct signal still reaches
	// the command if it has moved to another process group.
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
	// Ignore the error because the command may have exited or changed groups.
	_ = syscall.Kill(-pgid, sig)
}

func shouldResumeAfterSignal(sig syscall.Signal) bool {
	return sig != syscall.SIGKILL && sig != syscall.SIGCONT
}
