package timeout

import (
	"bytes"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

	_, err := ParseSignal("999")

	require.Error(t, err)
	require.ErrorIs(t, err, ErrUsage)
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

	signal.Ignore(syscall.SIGTERM)
	//nolint:gosec // The parent test passes temp file paths to this helper process.
	require.NoError(t, os.WriteFile(readyPath, nil, 0o600))

	for {
		//nolint:gosec // The parent test passes temp file paths to this helper process.
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
	got := Run([]string{"--foreground", "0", path}, Streams{
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
	got := Run([]string{"--foreground", "0", path}, Streams{
		Stdin:  nil,
		Stdout: nil,
		Stderr: stderr,
	})

	require.Equal(t, ExitCannotInvoke, got)
	require.Contains(t, stderr.String(), "failed to run command")
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
	args       []string
	wantCode   int
	wantStdout string
	wantStderr string
}

type versionFromBuildInfoTestCase struct {
	name string
	info *debug.BuildInfo
	ok   bool
	want string
}

type runCommandForegroundTestCase struct {
	name       string
	args       []string
	run        func(t *testing.T, streams Streams) int
	stdin      string
	wantCode   int
	wantStdout string
	wantStderr string
}

func runTermIgnoringCommand(t *testing.T, streams Streams) int {
	t.Helper()

	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	startPath := filepath.Join(dir, "start")

	args := []string{
		"--foreground", "--kill-after=0.1s", "0.5s",
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

func writerString(writer any) string {
	stringer, ok := writer.(interface{ String() string })
	if !ok {
		return ""
	}

	return stringer.String()
}

// ============================================================================
//  Data Providers Section
// ============================================================================

var parseTestCases = []parseTestCase{
	{
		name: "parses command",
		args: []string{"1s", "printf", "ok"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       1 * time.Second,
			KillAfter:      0,
			Signal:         syscall.SIGTERM,
			Command:        []string{"printf", "ok"},
		},
	},
	{
		name: "parses options before duration",
		args: []string{"-fpv", "-k", "0.5s", "-s", "HUP", "2m", "sleep", "9"},
		want: Config{
			Foreground:     true,
			PreserveStatus: true,
			Verbose:        true,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       2 * time.Minute,
			KillAfter:      500 * time.Millisecond,
			Signal:         syscall.SIGHUP,
			Command:        []string{"sleep", "9"},
		},
	},
	{
		name: "stops option parsing at duration",
		args: []string{"0", "printf", "--help"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       0,
			KillAfter:      0,
			Signal:         syscall.SIGTERM,
			Command:        []string{"printf", "--help"},
		},
	},
	{
		name: "parses long option values",
		args: []string{"--kill-after=1s", "--signal=SIGUSR1", "3", "sleep", "4"},
		want: Config{
			Foreground:     false,
			PreserveStatus: false,
			Verbose:        false,
			ShowHelp:       false,
			ShowVersion:    false,
			Duration:       3 * time.Second,
			KillAfter:      1 * time.Second,
			Signal:         syscall.SIGUSR1,
			Command:        []string{"sleep", "4"},
		},
	},
}

var parseUsageErrorTestCases = []parseUsageErrorTestCase{
	{name: "missing duration", args: nil},
	{name: "missing command", args: []string{"1s"}},
	{name: "unknown option", args: []string{"--bogus"}},
	{name: "missing signal argument", args: []string{"--signal"}},
	{name: "missing kill after argument", args: []string{"-k"}},
	{name: "invalid duration", args: []string{"bad", "true"}},
	{name: "invalid kill after", args: []string{"--kill-after=bad", "1s", "true"}},
	{name: "invalid signal", args: []string{"--signal=NOPE", "1s", "true"}},
}

var parseDurationTestCases = []parseDurationTestCase{
	{name: "seconds without suffix", in: "2", want: 2 * time.Second},
	{name: "seconds suffix", in: "2s", want: 2 * time.Second},
	{name: "minutes suffix", in: "1.5m", want: 90 * time.Second},
	{name: "hours suffix", in: "0.5h", want: 30 * time.Minute},
	{name: "days suffix", in: "1d", want: 24 * time.Hour},
	{name: "zero disables", in: "0", want: 0},
	{name: "huge positive clamps", in: "1e999d", want: maxDuration},
}

var invalidDurationInputs = []string{"", "-1", "NaN", "Inf", "1ms", "1sec", "abc"}

var parseSignalTestCases = []parseSignalTestCase{
	{name: "name", in: "TERM", want: syscall.SIGTERM},
	{name: "sig prefix", in: "SIGTERM", want: syscall.SIGTERM},
	{name: "lowercase", in: "term", want: syscall.SIGTERM},
	{name: "number", in: "15", want: syscall.SIGTERM},
}

var runStaticOutputTestCases = []runStaticOutputTestCase{
	{
		name:       "help",
		args:       []string{"--help"},
		wantCode:   ExitSuccess,
		wantStdout: "Usage:",
		wantStderr: "",
	},
	{
		name:       "version",
		args:       []string{"--version"},
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
		args:       []string{"--foreground", "0", "printf", "ok"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitSuccess,
		wantStdout: "ok",
		wantStderr: "",
	},
	{
		name:       "stdin passthrough",
		args:       []string{"--foreground", "0", "cat"},
		run:        nil,
		stdin:      "input",
		wantCode:   ExitSuccess,
		wantStdout: "input",
		wantStderr: "",
	},
	{
		name:       "exit status",
		args:       []string{"--foreground", "0", "sh", "-c", "exit 42"},
		run:        nil,
		stdin:      "",
		wantCode:   42,
		wantStdout: "",
		wantStderr: "",
	},
	{
		name:       "stderr passthrough",
		args:       []string{"--foreground", "0", "sh", "-c", "printf err >&2"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitSuccess,
		wantStdout: "",
		wantStderr: "err",
	},
	{
		name:       "not found",
		args:       []string{"--foreground", "0", "go-timeout-command-not-found"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitNotFound,
		wantStdout: "",
		wantStderr: "failed to run command",
	},
	{
		name:       "absolute path not found",
		args:       []string{"--foreground", "0", "/tmp/go-timeout-command-not-found"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitNotFound,
		wantStdout: "",
		wantStderr: "failed to run command",
	},
	{
		name:       "timeout",
		args:       []string{"--foreground", "0.01s", "sleep", "1"},
		run:        nil,
		stdin:      "",
		wantCode:   ExitTimedOut,
		wantStdout: "",
		wantStderr: "",
	},
	{
		name: "preserve status",
		args: []string{
			"--foreground", "--preserve-status", "0.01s",
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
		args:       []string{"--foreground", "--verbose", "0.01s", "sleep", "1"},
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
