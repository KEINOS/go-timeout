//go:build windows

package timeout

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	commandCmd = "cmd"

	durationFastTimeout = "0.05s"

	// signalNameTERM avoids repeating the "TERM" literal across cases (goconst).
	signalNameTERM = "TERM"
)

var errTestStreamWrite = errors.New("test stream write")

// ============================================================================
//  Parser and pure-logic tests (portable behavior verified on Windows)
// ============================================================================

func TestParseWindows(t *testing.T) {
	t.Parallel()

	config, err := Parse([]string{"-k", "1s", "-s", signalNameTERM, "2s", commandCmd, "/c", "echo", "ok"})

	require.NoError(t, err)
	require.Equal(t, time.Second, config.KillAfter)
	require.Equal(t, 2*time.Second, config.Duration)
	require.Equal(t, syscall.SIGTERM, config.Signal)
	require.Equal(t, []string{commandCmd, "/c", "echo", "ok"}, config.Command)
}

func TestParseUsageErrorsWindows(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		nil,
		{"1s"},
		{"--bogus"},
		{"bad", "cmd"},
		{optionSignal + "=NOPE", "1s", "cmd"},
	} {
		_, err := Parse(args)

		require.ErrorIs(t, err, ErrUsage)
	}
}

func TestParseDurationWindows(t *testing.T) {
	t.Parallel()

	cases := map[string]time.Duration{
		"2":   2 * time.Second,
		"2s":  2 * time.Second,
		"1.5m": 90 * time.Second,
		"0.5h": 30 * time.Minute,
		"1d":  24 * time.Hour,
		"0":   0,
	}

	for in, want := range cases {
		got, err := ParseDuration(in)

		require.NoError(t, err)
		require.Equal(t, want, got)
	}

	for _, bad := range []string{"", "-1", "NaN", "1ms", "abc"} {
		_, err := ParseDuration(bad)

		require.ErrorIs(t, err, ErrUsage)
	}
}

func TestParseSignalWindows(t *testing.T) {
	t.Parallel()

	for _, name := range []string{signalNameTERM, "SIGTERM", "term", "15"} {
		got, err := ParseSignal(name)

		require.NoError(t, err)
		require.Equal(t, syscall.SIGTERM, got)
	}

	for _, bad := range []string{"", "0", "-1", "999"} {
		_, err := ParseSignal(bad)

		require.ErrorIs(t, err, ErrUsage)
	}
}

// Job-control signal names are not supported on Windows; parsing them must fail.
func TestParseSignalRejectsUnsupportedOnWindows(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"USR1", "USR2", "CONT", "STOP", "TSTP", "TTIN", "TTOU"} {
		_, err := ParseSignal(name)

		require.ErrorIs(t, err, ErrUsage, name)
	}
}

func TestSupportedSignalsWindowsSubset(t *testing.T) {
	t.Parallel()

	for _, name := range []string{signalNameTERM, "INT", "KILL", "HUP", "QUIT"} {
		_, ok := supportedSignals[name]

		require.True(t, ok, name)
	}

	for _, name := range []string{"USR1", "USR2", "CONT", "STOP", "TSTP", "TTIN", "TTOU"} {
		_, ok := supportedSignals[name]

		require.False(t, ok, name)
	}
}

func TestSignalNameFallsBackForUnknownSignalWindows(t *testing.T) {
	t.Parallel()

	require.Equal(t, "signal 99", signalName(syscall.Signal(99)))
}

func TestParseDurationNumberRejectsNaNWindows(t *testing.T) {
	t.Parallel()

	_, err := parseDurationNumber("NaN")

	require.ErrorIs(t, err, ErrUsage)
}

func TestStopTimerAllowsNilWindows(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() { stopTimer(nil) })
}

func TestNewStoppedTimerDoesNotFireWindows(t *testing.T) {
	t.Parallel()

	timer := newStoppedTimer()
	defer stopTimer(timer)

	select {
	case <-timer.C:
		require.Fail(t, "stopped timer should not fire")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestLockedWriterWrapsWriteErrorWindows(t *testing.T) {
	t.Parallel()

	writer := lockedWriter{mutex: new(sync.Mutex), writer: errorWriter{}}

	written, err := writer.Write([]byte("data"))

	require.Zero(t, written)
	require.ErrorIs(t, err, errTestStreamWrite)
}

func TestVersionFromBuildInfoWindows(t *testing.T) {
	t.Parallel()

	require.Equal(t, defaultVersion, versionFromBuildInfo(nil, false))
}

// ============================================================================
//  Windows platform-behavior tests
// ============================================================================

// setupProcessGroup is a no-op on Windows and must always succeed.
func TestSetupProcessGroupNoOpWindows(t *testing.T) {
	t.Parallel()

	state := new(runnerState)

	code, ok := state.setupProcessGroup()

	require.True(t, ok)
	require.Equal(t, 0, code)
	require.Zero(t, state.pgid)
}

// deliverSignal must terminate the command process regardless of the requested
// signal, because Windows cannot deliver arbitrary POSIX signals.
func TestDeliverSignalTerminatesWindows(t *testing.T) {
	t.Parallel()

	var got []syscall.Signal

	state := new(runnerState)
	state.signalProcess = func(_ *exec.Cmd, sig syscall.Signal) {
		got = append(got, sig)
	}

	state.deliverSignal(new(exec.Cmd), syscall.SIGTERM)
	state.deliverSignal(new(exec.Cmd), syscall.SIGHUP)

	require.Equal(t, []syscall.Signal{syscall.SIGKILL, syscall.SIGKILL}, got)
}

// ============================================================================
//  End-to-end behavior on Windows using the cmd interpreter
// ============================================================================

func TestRunStaticOutputWindows(t *testing.T) {
	t.Parallel()

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	code := Run([]string{optionHelp}, Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: stdout,
		Stderr: stderr,
	})

	require.Equal(t, ExitSuccess, code)
	require.Contains(t, stdout.String(), "Usage:")
}

func TestRunCommandExitStatusWindows(t *testing.T) {
	t.Parallel()

	stdout := new(bytes.Buffer)
	code := Run([]string{"0", commandCmd, "/c", "exit 42"}, Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: stdout,
		Stderr: io.Discard,
	})

	require.Equal(t, 42, code)
}

func TestRunCommandNotFoundWindows(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	code := Run([]string{"0", "go-timeout-command-not-found"}, Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: stderr,
	})

	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr.String(), "failed to run command")
}

// TestRunCommandTimeoutWindows verifies the command is terminated and the
// timed-out exit code is returned. It re-execs the test binary as a helper that
// sleeps, avoiding dependence on external sleep utilities.
func TestRunCommandTimeoutWindows(t *testing.T) {
	// No t.Parallel(): this test calls t.Setenv, which Go forbids in parallel
	// tests.
	if os.Getenv("GO_TIMEOUT_HELPER_PROCESS") == "sleep" {
		time.Sleep(time.Hour)

		return
	}

	args := []string{
		durationFastTimeout,
		os.Args[0], "-test.run=TestRunCommandTimeoutWindows",
	}

	streams := Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}

	t.Setenv("GO_TIMEOUT_HELPER_PROCESS", "sleep")

	code := Run(args, streams)

	require.Equal(t, ExitTimedOut, code)
}

// ============================================================================
//  Helpers
// ============================================================================

type errorWriter struct{}

func (errorWriter) Write(_ []byte) (int, error) {
	return 0, errTestStreamWrite
}
