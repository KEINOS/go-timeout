//go:build unix

package timeout

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ============================================================================
//  Constants Section
// ============================================================================

const (
	commandPrintf = "printf"
	commandSleep  = "sleep"
	commandTrue   = "true"

	durationFastTimeout = "0.01s"
	pgidTest            = 424242

	// waitForCommandTimeout fails the self-signal test fast on a slow CI host
	// instead of hanging until the global go test timeout. It is generous
	// relative to the millisecond-scale signal delivery the test exercises.
	waitForCommandTimeout = 5 * time.Second
)

// ============================================================================
//  Variables Section
// ============================================================================

var errTestStartFailure = errors.New("test start failure")
var errTestStreamWrite = errors.New("test stream write")
var errTestSyscallFailure = errors.New("test syscall failure")

// ============================================================================
//  Test Section
// ============================================================================

func TestParse(t *testing.T) {
	t.Parallel()

	for _, tt := range parseTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.args)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseUsageErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range parseUsageErrorTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse(tt.args)

			require.Error(t, err)
			require.ErrorIs(t, err, ErrUsage)
		})
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	for _, tt := range parseDurationTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDuration(tt.in)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseDurationRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range invalidDurationInputs {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := ParseDuration(input)

			require.Error(t, err)
			require.ErrorIs(t, err, ErrUsage)
		})
	}
}

func TestParseSignal(t *testing.T) {
	t.Parallel()

	for _, tt := range parseSignalTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSignal(tt.in)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseSignalRejectsUnsupportedNumber(t *testing.T) {
	t.Parallel()

	for _, input := range invalidSignalInputs {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := ParseSignal(input)

			require.Error(t, err)
			require.ErrorIs(t, err, ErrUsage)
		})
	}
}

func TestAppendSignalIfMissing(t *testing.T) {
	t.Parallel()

	signals := []os.Signal{syscall.SIGTERM}

	got := appendSignalIfMissing(signals, syscall.SIGTERM)

	require.Len(t, got, 1)
	require.Equal(t, signals, got)

	got = appendSignalIfMissing(got, syscall.SIGHUP)

	require.Equal(t, []os.Signal{syscall.SIGTERM, syscall.SIGHUP}, got)
}

func TestSendSignalForeground(t *testing.T) {
	t.Parallel()

	recorder := newSignalRecorder()
	state := new(runnerState)
	state.config.Foreground = true
	state.signalProcess = recorder.process
	state.signalGroup = recorder.group

	state.sendSignal(new(exec.Cmd), syscall.SIGTERM)

	require.True(t, state.signalSent)
	require.Equal(t, []syscall.Signal{syscall.SIGTERM}, recorder.processSignals)
	require.Empty(t, recorder.groupSignals)
	require.Zero(t, state.suppressed)
}

func TestSendSignalBackground(t *testing.T) {
	t.Parallel()

	recorder := newSignalRecorder()
	state := new(runnerState)
	state.pgid = os.Getpid()
	state.signalProcess = recorder.process
	state.signalGroup = recorder.group

	state.sendSignal(new(exec.Cmd), syscall.SIGTERM)

	require.True(t, state.signalSent)
	require.Equal(t, []syscall.Signal{syscall.SIGTERM, syscall.SIGCONT}, recorder.processSignals)
	require.Equal(t, []syscall.Signal{syscall.SIGTERM, syscall.SIGCONT}, recorder.groupSignals)
	require.Equal(t, syscall.SIGTERM, state.suppressed)
	require.NotZero(t, state.suppressBy)
}

func TestSendSignalBackgroundDoesNotResumeForKillOrCont(t *testing.T) {
	t.Parallel()

	for _, sig := range []syscall.Signal{syscall.SIGKILL, syscall.SIGCONT} {
		t.Run(signalName(sig), func(t *testing.T) {
			t.Parallel()

			recorder := newSignalRecorder()
			state := new(runnerState)
			state.pgid = os.Getpid()
			state.signalProcess = recorder.process
			state.signalGroup = recorder.group

			state.sendSignal(new(exec.Cmd), sig)

			require.Equal(t, []syscall.Signal{sig}, recorder.processSignals)
			require.Equal(t, []syscall.Signal{sig}, recorder.groupSignals)
		})
	}
}

func TestShouldSuppress(t *testing.T) {
	t.Parallel()

	t.Run("different signal", func(t *testing.T) {
		t.Parallel()

		state := new(runnerState)
		state.suppressed = syscall.SIGTERM
		state.suppressBy = time.Now().Add(time.Minute)

		require.False(t, state.shouldSuppress(syscall.SIGHUP))
	})

	t.Run("expired window", func(t *testing.T) {
		t.Parallel()

		state := new(runnerState)
		state.suppressed = syscall.SIGTERM
		state.suppressBy = time.Now().Add(-time.Minute)

		require.False(t, state.shouldSuppress(syscall.SIGTERM))
		require.Zero(t, state.suppressed)
	})

	t.Run("active window", func(t *testing.T) {
		t.Parallel()

		state := new(runnerState)
		state.suppressed = syscall.SIGTERM
		state.suppressBy = time.Now().Add(time.Minute)

		require.True(t, state.shouldSuppress(syscall.SIGTERM))
	})
}

func TestForwardSignal(t *testing.T) {
	t.Parallel()

	t.Run("ignores non syscall signal", func(t *testing.T) {
		t.Parallel()

		state := new(runnerState)
		state.forwardSignal(new(exec.Cmd), fakeSignal("custom"), newStoppedTimer())

		require.False(t, state.signalSent)
	})

	t.Run("ignores suppressed signal", func(t *testing.T) {
		t.Parallel()

		state := new(runnerState)
		state.suppressed = syscall.SIGTERM
		state.suppressBy = time.Now().Add(time.Minute)
		state.forwardSignal(new(exec.Cmd), syscall.SIGTERM, newStoppedTimer())

		require.False(t, state.signalSent)
	})

	t.Run("forwards and starts kill timer", func(t *testing.T) {
		t.Parallel()

		recorder := newSignalRecorder()
		timer := newStoppedTimer()
		state := new(runnerState)
		state.config.KillAfter = time.Hour
		state.signalProcess = recorder.process
		state.signalGroup = recorder.group

		state.forwardSignal(new(exec.Cmd), syscall.SIGTERM, timer)
		defer stopTimer(timer)

		require.True(t, state.signalSent)
		require.Equal(t, []syscall.Signal{syscall.SIGTERM, syscall.SIGCONT}, recorder.processSignals)
		require.Equal(t, []syscall.Signal{syscall.SIGTERM, syscall.SIGCONT}, recorder.groupSignals)
	})
}

func TestClassifyStartError(t *testing.T) {
	t.Parallel()

	for _, tt := range classifyStartErrorTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifyStartError(tt.err)

			require.Equal(t, tt.want, got)
		})
	}
}

//nolint:paralleltest // no parallel due to editing process-group syscall globals
func TestRunCommandProcessGroupSetup(t *testing.T) {
	stderr := new(bytes.Buffer)
	state := newProcessGroupTestState(stderr)

	withProcessGroupFuncs(t, nil, nil)

	got := state.runCommand()

	require.Equal(t, ExitSuccess, got)
	require.Equal(t, pgidTest, state.pgid)
	require.Empty(t, stderr.String())
}

//nolint:paralleltest // no parallel due to editing process-group syscall globals
func TestRunCommandAllowsExistingProcessGroup(t *testing.T) {
	stderr := new(bytes.Buffer)
	state := newProcessGroupTestState(stderr)

	withProcessGroupFuncs(t, func(int, int) error {
		return syscall.EPERM
	}, nil)

	got := state.runCommand()

	require.Equal(t, ExitSuccess, got)
	require.Equal(t, pgidTest, state.pgid)
	require.Empty(t, stderr.String())
}

//nolint:paralleltest // no parallel due to editing process-group syscall globals
func TestRunCommandReportsProcessGroupErrors(t *testing.T) {
	for _, tt := range runCommandProcessGroupErrorTestCases {
		//nolint:paralleltest // no parallel due to editing process-group syscall globals
		t.Run(tt.name, func(t *testing.T) {
			stderr := new(bytes.Buffer)
			state := newProcessGroupTestState(stderr)

			withProcessGroupFuncs(t, tt.setpgid, tt.getpgid)

			got := state.runCommand()

			require.Equal(t, ExitInternalFailure, got)
			require.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunStaticOutput(t *testing.T) {
	t.Parallel()

	for _, tt := range runStaticOutputTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			got := Run(tt.args, Streams{
				Stdin:  bytes.NewReader(nil),
				Stdout: stdout,
				Stderr: stderr,
			})

			require.Equal(t, tt.wantCode, got)
			require.Contains(t, stdout.String(), tt.wantStdout)
			require.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestVersionFromBuildInfo(t *testing.T) {
	t.Parallel()

	for _, tt := range versionFromBuildInfoTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := versionFromBuildInfo(tt.info, tt.ok)

			require.Equal(t, tt.want, got)
		})
	}
}

func TestRunCommandForeground(t *testing.T) {
	t.Parallel()

	for _, tt := range runCommandForegroundTestCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)
			streams := Streams{
				Stdin:  strings.NewReader(tt.stdin),
				Stdout: stdout,
				Stderr: stderr,
			}

			var got int
			if tt.run == nil {
				got = Run(tt.args, streams)
			} else {
				got = tt.run(t, streams)
			}

			require.Equal(t, tt.wantCode, got)
			require.Contains(t, stdout.String(), tt.wantStdout)
			require.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestTermIgnoringHelperProcess(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_TIMEOUT_HELPER_PROCESS") != "term-ignore" {
		return
	}

	readyPath := os.Getenv("GO_TIMEOUT_HELPER_READY")
	startPath := os.Getenv("GO_TIMEOUT_HELPER_START")

	require.NotEmpty(t, readyPath)
	require.NotEmpty(t, startPath)

	readyPath = filepath.Clean(readyPath)
	startPath = filepath.Clean(startPath)

	signal.Ignore(syscall.SIGTERM)
	require.NoError(t, os.WriteFile(readyPath, nil, 0o600))

	for {
		_, err := os.Stat(startPath)
		if err == nil {
			break
		}

		require.ErrorIs(t, err, os.ErrNotExist)
		time.Sleep(10 * time.Millisecond)
	}

	for {
		time.Sleep(time.Hour)
	}
}

func TestRunCommandCannotInvoke(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "not-executable")

	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600))

	stderr := new(bytes.Buffer)
	got := Run([]string{optionForeground, "0", path}, Streams{
		Stdin:  nil,
		Stdout: nil,
		Stderr: stderr,
	})

	require.Equal(t, ExitCannotInvoke, got)
	require.Contains(t, stderr.String(), "failed to run command")
}

func TestRunCommandRejectsInvalidExecutableFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "invalid-executable")

	require.NoError(t, os.WriteFile(path, []byte("not a native executable\n"), 0o600))
	//nolint:gosec // executable bit is required to exercise ENOEXEC.
	require.NoError(t, os.Chmod(path, 0o700))

	stderr := new(bytes.Buffer)
	got := Run([]string{optionForeground, "0", path}, Streams{
		Stdin:  nil,
		Stdout: nil,
		Stderr: stderr,
	})

	require.Equal(t, ExitCannotInvoke, got)
	require.Contains(t, stderr.String(), "failed to run command")
}

func TestWaitExitCodeReportsUnexpectedWaitError(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	state := new(runnerState)
	state.streams = fillDefaultStreams(Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: stderr,
	})

	got := state.waitExitCode(errTestStartFailure)

	require.Equal(t, ExitInternalFailure, got)
	require.Contains(t, stderr.String(), "wait for command")
}

func TestFillDefaultStreamsUsesDefaultStderr(t *testing.T) {
	t.Parallel()

	streams := fillDefaultStreams(Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: nil,
	})

	require.NotNil(t, streams.Stderr)
}

func TestLockedWriterWrapsWriteError(t *testing.T) {
	t.Parallel()

	writer := lockedWriter{
		mutex:  new(sync.Mutex),
		writer: errorWriter{},
	}

	written, err := writer.Write([]byte("data"))

	require.Zero(t, written)
	require.ErrorIs(t, err, errTestStreamWrite)
}

func TestParseDurationNumberRejectsNaN(t *testing.T) {
	t.Parallel()

	_, err := parseDurationNumber("NaN")

	require.Error(t, err)
	require.ErrorIs(t, err, ErrUsage)
}

func TestIsPositiveFloatOverflowRejectsOtherErrors(t *testing.T) {
	t.Parallel()

	require.False(t, isPositiveFloatOverflow(errTestStartFailure, 0))
}

func TestDefaultGroupSignalIsBestEffort(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		defaultGroupSignal(pgidTest, 0)
	})
}

func TestSendGroupSignalUsesDefaultSender(t *testing.T) {
	t.Parallel()

	state := new(runnerState)
	state.pgid = pgidTest

	require.NotPanics(t, func() {
		state.sendGroupSignal(0)
	})
}

func TestSignalNameFallsBackForUnknownSignal(t *testing.T) {
	t.Parallel()

	require.Equal(t, "signal 99", signalName(syscall.Signal(99)))
}

func TestStopTimerAllowsNil(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		stopTimer(nil)
	})
}

//nolint:paralleltest // no parallel: installs process-wide signal handlers and self-signals
func TestWaitForCommandForwardsReceivedSignal(t *testing.T) {
	// Pre-register our own handler for SIGUSR1 before anything else so the
	// default (fatal) disposition is suppressed for the whole test, even in
	// the window before waitForCommand installs its own notifier.
	selfCh := make(chan os.Signal, 1)
	signal.Notify(selfCh, syscall.SIGUSR1)

	defer signal.Stop(selfCh)

	cmd := exec.CommandContext(t.Context(), commandSleep, "30")
	require.NoError(t, cmd.Start())

	forwarded := make(chan syscall.Signal, 1)

	state := new(runnerState)
	state.config = Config{
		Foreground:     true,
		PreserveStatus: false,
		Verbose:        false,
		ShowHelp:       false,
		ShowVersion:    false,
		Duration:       0,
		KillAfter:      0,
		Signal:         syscall.SIGUSR1,
		Command:        []string{commandSleep},
	}
	state.streams = fillDefaultStreams(Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	// Record the forwarded signal, then deliver it to the real child so the
	// child exits and waitForCommand's done branch ends the loop.
	state.signalProcess = func(child *exec.Cmd, sig syscall.Signal) {
		select {
		case forwarded <- sig:
		default:
		}

		_ = child.Process.Signal(sig)
	}

	// Repeatedly signal our own process until waitForCommand has installed its
	// notifier and forwarded the signal into the select loop.
	stopSending := make(chan struct{})

	go signalSelfUntil(stopSending)

	code := runWaitForCommandGuarded(t, state, cmd)

	close(stopSending)

	require.True(t, state.signalSent, "signal should have been forwarded to the child")
	require.Equal(t, syscall.SIGUSR1, <-forwarded)
	require.Equal(t, signalExitCode(syscall.SIGUSR1), code)
}

//nolint:paralleltest // no parallel: overrides the exitErrorWaitStatus package seam
func TestWaitExitCodeFallsBackWhenSysNotWaitStatus(t *testing.T) {
	original := exitErrorWaitStatus
	exitErrorWaitStatus = func(*exec.ExitError) (syscall.WaitStatus, bool) {
		return 0, false
	}

	defer func() { exitErrorWaitStatus = original }()

	cmd := exec.CommandContext(t.Context(), "sh", "-c", "exit 7")
	err := cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)

	state := new(runnerState)
	state.streams = fillDefaultStreams(Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})

	require.Equal(t, 7, state.waitExitCode(err))
}

func TestNewStoppedTimerDoesNotFire(t *testing.T) {
	t.Parallel()

	timer := newStoppedTimer()
	defer stopTimer(timer)

	select {
	case <-timer.C:
		require.Fail(t, "stopped timer should not fire")
	case <-time.After(20 * time.Millisecond):
	}
}

// ============================================================================
//  Helpers Section
// ============================================================================

type parseTestCase struct {
	name string
	args []string
	want Config
}

type parseUsageErrorTestCase struct {
	name string
	args []string
}

type parseDurationTestCase struct {
	name string
	in   string
	want time.Duration
}

type parseSignalTestCase struct {
	name string
	in   string
	want syscall.Signal
}

type runStaticOutputTestCase struct {
	name       string
	wantStdout string
	wantStderr string
	args       []string
	wantCode   int
}

type versionFromBuildInfoTestCase struct {
	info *debug.BuildInfo
	name string
	want string
	ok   bool
}

type runCommandForegroundTestCase struct {
	run        func(t *testing.T, streams Streams) int
	name       string
	stdin      string
	wantStdout string
	wantStderr string
	args       []string
	wantCode   int
}

type classifyStartErrorTestCase struct {
	err  error
	name string
	want int
}

type runCommandProcessGroupErrorTestCase struct {
	setpgid    func(int, int) error
	getpgid    func(int) (int, error)
	name       string
	wantStderr string
}

type fakeSignal string

type errorWriter struct{}

type signalRecorder struct {
	processSignals []syscall.Signal
	groupSignals   []syscall.Signal
}

func (signal fakeSignal) Signal() {}

func (signal fakeSignal) String() string {
	return string(signal)
}

func (writer errorWriter) Write(_ []byte) (int, error) {
	return 0, errTestStreamWrite
}

func newSignalRecorder() *signalRecorder {
	return new(signalRecorder)
}

func (recorder *signalRecorder) process(_ *exec.Cmd, sig syscall.Signal) {
	recorder.processSignals = append(recorder.processSignals, sig)
}

func (recorder *signalRecorder) group(_ int, sig syscall.Signal) {
	recorder.groupSignals = append(recorder.groupSignals, sig)
}

func runTermIgnoringCommand(t *testing.T, streams Streams) int {
	t.Helper()

	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	startPath := filepath.Join(dir, "start")

	args := []string{
		optionForeground, optionKillAfter + "=0.1s", "0.5s",
		"env", "GO_TIMEOUT_HELPER_PROCESS=term-ignore",
		"GO_TIMEOUT_HELPER_READY=" + readyPath,
		"GO_TIMEOUT_HELPER_START=" + startPath,
		os.Args[0], "-test.run=TestTermIgnoringHelperProcess",
	}

	done := make(chan int, 1)
	go func() {
		done <- Run(args, streams)
	}()

	deadline := time.After(2 * time.Second)

	for {
		select {
		case code := <-done:
			require.Failf(
				t,
				"helper exited before it was ready",
				"exit code: %d, stderr: %q",
				code,
				writerString(streams.Stderr),
			)
		case <-deadline:
			require.Fail(t, "helper did not become ready")
		default:
			_, err := os.Stat(readyPath)
			if err == nil {
				require.NoError(t, os.WriteFile(startPath, nil, 0o600))

				return <-done
			}

			require.ErrorIs(t, err, os.ErrNotExist)
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// runWaitForCommandGuarded runs the blocking waitForCommand loop under a
// deadline so a missed signal delivery on a slow CI host fails fast instead of
// hanging until the global test timeout. On timeout it kills the child so the
// done branch unblocks and neither the goroutine nor the child leaks.
func runWaitForCommandGuarded(t *testing.T, state *runnerState, cmd *exec.Cmd) int {
	t.Helper()

	result := make(chan int, 1)

	go func() {
		result <- state.waitForCommand(cmd)
	}()

	select {
	case code := <-result:
		return code
	case <-time.After(waitForCommandTimeout):
		_ = cmd.Process.Kill()

		<-result

		require.FailNow(t, "waitForCommand did not observe the forwarded signal in time")

		return 0
	}
}

func signalSelfUntil(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}

		_ = syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)

		time.Sleep(5 * time.Millisecond)
	}
}

func writerString(writer any) string {
	stringer, ok := writer.(interface{ String() string })
	if !ok {
		return ""
	}

	return stringer.String()
}

func newProcessGroupTestState(stderr io.Writer) *runnerState {
	state := new(runnerState)
	state.config = Config{
		Foreground:     false,
		PreserveStatus: false,
		Verbose:        false,
		ShowHelp:       false,
		ShowVersion:    false,
		Duration:       0,
		KillAfter:      0,
		Signal:         syscall.SIGTERM,
		Command:        []string{commandTrue},
	}
	state.streams = fillDefaultStreams(Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: stderr,
	})

	return state
}

func withProcessGroupFuncs(
	t *testing.T,
	setpgid func(int, int) error,
	getpgid func(int) (int, error),
) {
	t.Helper()

	oldSetpgid := syscallSetpgid
	oldGetpgid := syscallGetpgid

	if setpgid == nil {
		setpgid = func(int, int) error {
			return nil
		}
	}

	if getpgid == nil {
		getpgid = func(int) (int, error) {
			return pgidTest, nil
		}
	}

	syscallSetpgid = setpgid
	syscallGetpgid = getpgid

	t.Cleanup(func() {
		syscallSetpgid = oldSetpgid
		syscallGetpgid = oldGetpgid
	})
}

// ============================================================================
//  Data Providers Section
// ============================================================================

var parseTestCases = []parseTestCase{
	{
		name: "parses command",
		args: []string{"1s", commandPrintf, "ok"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       1 * time.Second,
			KillAfter:      0,
			Signal:         syscall.SIGTERM,
			Command:        []string{commandPrintf, "ok"},
		},
	},
	{
		name: "parses options before duration",
		args: []string{"-fpv", "-k", "0.5s", "-s", "HUP", "2m", commandSleep, "9"},
		want: Config{
			Foreground:     true,
			PreserveStatus: true,
			Verbose:        true,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       2 * time.Minute,
			KillAfter:      500 * time.Millisecond,
			Signal:         syscall.SIGHUP,
			Command:        []string{commandSleep, "9"},
		},
	},
	{
		name: "stops option parsing at duration",
		args: []string{"0", commandPrintf, optionHelp},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       0,
			KillAfter:      0,
			Signal:         syscall.SIGTERM,
			Command:        []string{commandPrintf, optionHelp},
		},
	},
	{
		name: "parses long option values",
		args: []string{optionKillAfter + "=1s", optionSignal + "=SIGUSR1", "3", commandSleep, "4"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       3 * time.Second,
			KillAfter:      1 * time.Second,
			Signal:         syscall.SIGUSR1,
			Command:        []string{commandSleep, "4"},
		},
	},
	{
		name: "parses long option arguments",
		args: []string{optionKillAfter, "1s", optionSignal, "USR2", "3", commandSleep, "4"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       3 * time.Second,
			KillAfter:      1 * time.Second,
			Signal:         syscall.SIGUSR2,
			Command:        []string{commandSleep, "4"},
		},
	},
	{
		name: "parses short option inline values",
		args: []string{"-k1s", "-sUSR1", "3", commandSleep, "4"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       3 * time.Second,
			KillAfter:      1 * time.Second,
			Signal:         syscall.SIGUSR1,
			Command:        []string{commandSleep, "4"},
		},
	},
	{
		name: "stops option parsing with double dash",
		args: []string{"--", "3", commandSleep, "4"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       3 * time.Second,
			KillAfter:      0,
			Signal:         syscall.SIGTERM,
			Command:        []string{commandSleep, "4"},
		},
	},
}

var parseUsageErrorTestCases = []parseUsageErrorTestCase{
	{name: "missing duration", args: nil},
	{name: "missing command", args: []string{"1s"}},
	{name: "unknown option", args: []string{"--bogus"}},
	{name: "missing signal argument", args: []string{optionSignal}},
	{name: "missing kill after argument", args: []string{"-k"}},
	{name: "missing long kill after argument", args: []string{optionKillAfter}},
	{name: "invalid duration", args: []string{"bad", commandTrue}},
	{name: "invalid kill after", args: []string{optionKillAfter + "=bad", "1s", commandTrue}},
	{name: "invalid signal", args: []string{optionSignal + "=NOPE", "1s", commandTrue}},
	{name: "invalid short option", args: []string{"-x"}},
}

var parseDurationTestCases = []parseDurationTestCase{
	{name: "seconds without suffix", in: "2", want: 2 * time.Second},
	{name: "seconds suffix", in: "2s", want: 2 * time.Second},
	{name: "minutes suffix", in: "1.5m", want: 90 * time.Second},
	{name: "hours suffix", in: "0.5h", want: 30 * time.Minute},
	{name: "days suffix", in: "1d", want: 24 * time.Hour},
	{name: "zero disables", in: "0", want: 0},
	{name: "finite huge positive clamps", in: "10000000000s", want: maxDuration},
	{name: "huge positive clamps", in: "1e999d", want: maxDuration},
}

var invalidDurationInputs = []string{"", "-1", "NaN", "Inf", "1ms", "1sec", "abc"}

var parseSignalTestCases = []parseSignalTestCase{
	{name: "name", in: "TERM", want: syscall.SIGTERM},
	{name: "sig prefix", in: "SIGTERM", want: syscall.SIGTERM},
	{name: "lowercase", in: "term", want: syscall.SIGTERM},
	{name: "number", in: "15", want: syscall.SIGTERM},
}

var invalidSignalInputs = []string{"", "0", "-1", "999"}

var runStaticOutputTestCases = []runStaticOutputTestCase{
	{
		name:       "help",
		args:       []string{optionHelp},
		wantCode:   ExitSuccess,
		wantStdout: "Usage:",
		wantStderr: "",
	},
	{
		name:       "version",
		args:       []string{optionVersion},
		wantCode:   ExitSuccess,
		wantStdout: "timeout",
		wantStderr: "",
	},
	{
		name:       "usage error",
		args:       nil,
		wantCode:   ExitInternalFailure,
		wantStdout: "",
		wantStderr: "missing operand",
	},
}

var versionFromBuildInfoTestCases = []versionFromBuildInfoTestCase{
	{
		name: "uses module version",
		info: newBuildInfo("v1.2.3"),
		ok:   true,
		want: "v1.2.3",
	},
	{
		name: "falls back when version is empty",
		info: newBuildInfo(""),
		ok:   true,
		want: defaultVersion,
	},
	{
		name: "falls back when unavailable",
		info: nil,
		ok:   false,
		want: defaultVersion,
	},
}

var runCommandForegroundTestCases = []runCommandForegroundTestCase{
	{
		name:       "success",
		args:       []string{optionForeground, "0", commandPrintf, "ok"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitSuccess,
		wantStdout: "ok",
		wantStderr: "",
	},
	{
		name:       "stdin passthrough",
		args:       []string{optionForeground, "0", "cat"},
		run:        nil,
		stdin:      "input",
		wantCode:   ExitSuccess,
		wantStdout: "input",
		wantStderr: "",
	},
	{
		name:       "exit status",
		args:       []string{optionForeground, "0", "sh", "-c", "exit 42"},
		run:        nil,
		stdin:      "",
		wantCode:   42,
		wantStdout: "",
		wantStderr: "",
	},
	{
		name:       "stderr passthrough",
		args:       []string{optionForeground, "0", "sh", "-c", "printf err >&2"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitSuccess,
		wantStdout: "",
		wantStderr: "err",
	},
	{
		name:       "not found",
		args:       []string{optionForeground, "0", "go-timeout-command-not-found"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitNotFound,
		wantStdout: "",
		wantStderr: "failed to run command",
	},
	{
		name:       "absolute path not found",
		args:       []string{optionForeground, "0", "/tmp/go-timeout-command-not-found"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitNotFound,
		wantStdout: "",
		wantStderr: "failed to run command",
	},
	{
		name:       "timeout",
		args:       []string{optionForeground, durationFastTimeout, commandSleep, "1"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitTimedOut,
		wantStdout: "",
		wantStderr: "",
	},
	{
		name: "preserve status",
		args: []string{
			optionForeground, optionPreserveStatus, durationFastTimeout,
			"sh", "-c", "trap 'exit 42' TERM; while true; do :; done",
		},
		run:        nil,
		stdin:      "",
		wantCode:   42,
		wantStdout: "",
		wantStderr: "",
	},
	{
		name:       "verbose signal",
		args:       []string{optionForeground, optionVerbose, durationFastTimeout, commandSleep, "1"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitTimedOut,
		wantStdout: "",
		wantStderr: "sending signal TERM",
	},
	{
		name:       "kill after",
		args:       nil,
		run:        runTermIgnoringCommand,
		stdin:      "",
		wantCode:   signalExitCode(syscall.SIGKILL),
		wantStdout: "",
		wantStderr: "",
	},
}

var classifyStartErrorTestCases = []classifyStartErrorTestCase{
	{
		name: "not found",
		err:  exec.ErrNotFound,
		want: ExitNotFound,
	},
	{
		name: "path does not exist",
		err:  os.ErrNotExist,
		want: ExitNotFound,
	},
	{
		name: "permission denied",
		err:  os.ErrPermission,
		want: ExitCannotInvoke,
	},
	{
		name: "invalid executable format",
		err:  syscall.ENOEXEC,
		want: ExitCannotInvoke,
	},
	{
		name: "is directory",
		err:  syscall.EISDIR,
		want: ExitCannotInvoke,
	},
	{
		name: "other",
		err:  errTestStartFailure,
		want: ExitInternalFailure,
	},
}

var runCommandProcessGroupErrorTestCases = []runCommandProcessGroupErrorTestCase{
	{
		name: "setpgid failure",
		setpgid: func(int, int) error {
			return errTestSyscallFailure
		},
		getpgid:    nil,
		wantStderr: "set process group",
	},
	{
		name: "getpgid failure",
		setpgid: func(int, int) error {
			return nil
		},
		getpgid: func(int) (int, error) {
			return 0, errTestSyscallFailure
		},
		wantStderr: "get process group",
	},
}

func newBuildInfo(version string) *debug.BuildInfo {
	info := new(debug.BuildInfo)
	info.GoVersion = ""
	info.Path = ""
	info.Main = debug.Module{
		Path:    "",
		Version: version,
		Sum:     "",
		Replace: nil,
	}
	info.Deps = nil
	info.Settings = nil

	return info
}
