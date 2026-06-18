package timeout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

type processSignalFunc func(*exec.Cmd, syscall.Signal)

func classifyStartError(err error) int {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return ExitNotFound
	}

	// GNU timeout uses exit code 126 when a command exists but cannot run.
	if errors.Is(err, os.ErrPermission) ||
		errors.Is(err, syscall.ENOEXEC) ||
		errors.Is(err, syscall.EISDIR) {
		return ExitCannotInvoke
	}

	return ExitInternalFailure
}

func defaultProcessSignal(cmd *exec.Cmd, sig syscall.Signal) {
	// Ignore the error because the command may have already exited.
	_ = cmd.Process.Signal(sig)
}

// runnerState stores the state for one run.
// platformState contains platform-specific signal handling.
type runnerState struct {
	platformState

	streams    Streams
	suppressBy time.Time
	config     Config
	pgid       int
	suppressed syscall.Signal
	killSent   bool
	signalSent bool
	timedOut   bool
}

func (state *runnerState) forwardSignal(cmd *exec.Cmd, received os.Signal, killTimer *time.Timer) {
	sig, ok := received.(syscall.Signal)
	if !ok {
		return
	}

	if state.shouldSuppress(sig) {
		return
	}

	state.sendSignal(cmd, sig)
	state.startKillTimer(killTimer)
}

// runCommand starts the command and waits for it to finish.
// setupProcessGroup handles any platform-specific setup.
func (state *runnerState) runCommand() int {
	if code, ok := state.setupProcessGroup(); !ok {
		return code
	}

	state.protectFromJobControlStop()
	defer state.releaseJobControlProtection()

	//nolint:gosec // Running the requested command is the purpose of this package.
	cmd := exec.CommandContext(context.Background(), state.config.Command[0], state.config.Command[1:]...)
	cmd.Stdin = state.streams.Stdin
	cmd.Stdout = state.streams.Stdout
	cmd.Stderr = state.streams.Stderr

	err := cmd.Start()
	if err != nil {
		return state.startErrorExitCode(err)
	}

	return state.waitForCommand(cmd)
}

func (state *runnerState) sendProcessSignal(cmd *exec.Cmd, sig syscall.Signal) {
	signalProcess := state.signalProcess
	if signalProcess == nil {
		signalProcess = defaultProcessSignal
	}

	signalProcess(cmd, sig)
}

func (state *runnerState) sendSignal(cmd *exec.Cmd, sig syscall.Signal) {
	state.signalSent = true

	if state.config.Verbose {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: sending signal %s to command %s\n",
			signalName(sig), state.config.Command[0])
	}

	state.deliverSignal(cmd, sig)
}

func (state *runnerState) shouldSuppress(sig syscall.Signal) bool {
	// A process-group signal can return to timeout itself. Ignore the same
	// signal briefly to prevent a loop, but still allow later external signals.
	if state.suppressed != sig {
		return false
	}

	if time.Now().After(state.suppressBy) {
		state.suppressed = 0

		return false
	}

	return true
}

func (state *runnerState) startErrorExitCode(err error) int {
	exitCode := classifyStartError(err)

	_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: failed to run command %q: %v\n", state.config.Command[0], err)

	return exitCode
}

func (state *runnerState) startKillTimer(timer *time.Timer) {
	if state.killSent || state.config.KillAfter == 0 {
		return
	}

	resetTimer(timer, state.config.KillAfter)
}

func (state *runnerState) timeoutExitCode(err error) int {
	exitCode := state.waitExitCode(err)
	if state.timedOut && !state.config.PreserveStatus && exitCode != signalExitCode(syscall.SIGKILL) {
		return ExitTimedOut
	}

	return exitCode
}

func (state *runnerState) waitExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		_, _ = fmt.Fprintf(state.streams.Stderr, "timeout: wait for command: %v\n", err)

		return ExitInternalFailure
	}

	status, ok := exitErrorWaitStatus(exitErr)
	if !ok {
		return exitErr.ExitCode()
	}

	if status.Signaled() {
		return signalExitCode(status.Signal())
	}

	return status.ExitStatus()
}

func (state *runnerState) waitForCommand(cmd *exec.Cmd) int {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	initialTimer := newTimer(state.config.Duration)
	killTimer := newStoppedTimer()

	defer stopTimer(initialTimer)
	defer stopTimer(killTimer)

	signalCh := make(chan os.Signal, 1)
	notifySignals(signalCh, state.config.Signal)

	defer signal.Stop(signalCh)

	for {
		select {
		case err := <-done:
			return state.timeoutExitCode(err)
		case <-initialTimer.C:
			state.timedOut = true
			state.sendSignal(cmd, state.config.Signal)
			state.startKillTimer(killTimer)
		case <-killTimer.C:
			state.killSent = true
			state.sendSignal(cmd, syscall.SIGKILL)
		case received := <-signalCh:
			state.forwardSignal(cmd, received, killTimer)
		}
	}
}
