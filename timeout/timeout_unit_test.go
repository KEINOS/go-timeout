package timeout

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want Config
	}{
		{
			name: "parses command",
			args: []string{"1s", "printf", "ok"},
			want: Config{
				Duration: 1 * time.Second,
				Signal:   syscall.SIGTERM,
				Command:  []string{"printf", "ok"},
			},
		},
		{
			name: "parses options before duration",
			args: []string{"-fpv", "-k", "0.5s", "-s", "HUP", "2m", "sleep", "9"},
			want: Config{
				Foreground:     true,
				PreserveStatus: true,
				Verbose:        true,
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
				Duration: 0,
				Signal:   syscall.SIGTERM,
				Command:  []string{"printf", "--help"},
			},
		},
		{
			name: "parses long option values",
			args: []string{"--kill-after=1s", "--signal=SIGUSR1", "3", "sleep", "4"},
			want: Config{
				Duration:  3 * time.Second,
				KillAfter: 1 * time.Second,
				Signal:    syscall.SIGUSR1,
				Command:   []string{"sleep", "4"},
			},
		},
	}

	for _, tt := range tests {
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

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing duration", args: nil},
		{name: "missing command", args: []string{"1s"}},
		{name: "unknown option", args: []string{"--bogus"}},
		{name: "missing signal argument", args: []string{"--signal"}},
		{name: "missing kill after argument", args: []string{"-k"}},
		{name: "invalid duration", args: []string{"bad", "true"}},
		{name: "invalid kill after", args: []string{"--kill-after=bad", "1s", "true"}},
		{name: "invalid signal", args: []string{"--signal=NOPE", "1s", "true"}},
	}

	for _, tt := range tests {
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

	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{name: "seconds without suffix", in: "2", want: 2 * time.Second},
		{name: "seconds suffix", in: "2s", want: 2 * time.Second},
		{name: "minutes suffix", in: "1.5m", want: 90 * time.Second},
		{name: "hours suffix", in: "0.5h", want: 30 * time.Minute},
		{name: "days suffix", in: "1d", want: 24 * time.Hour},
		{name: "zero disables", in: "0", want: 0},
		{name: "huge positive clamps", in: "1e999d", want: maxDuration},
	}

	for _, tt := range tests {
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

	for _, input := range []string{"", "-1", "NaN", "Inf", "1ms", "1sec", "abc"} {
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

	tests := []struct {
		name string
		in   string
		want syscall.Signal
	}{
		{name: "name", in: "TERM", want: syscall.SIGTERM},
		{name: "sig prefix", in: "SIGTERM", want: syscall.SIGTERM},
		{name: "lowercase", in: "term", want: syscall.SIGTERM},
		{name: "number", in: "15", want: syscall.SIGTERM},
	}

	for _, tt := range tests {
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

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
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

	for _, tt := range tests {
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

	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{
			name: "uses module version",
			info: &debug.BuildInfo{
				Main: debug.Module{
					Version: "v1.2.3",
				},
			},
			ok:   true,
			want: "v1.2.3",
		},
		{
			name: "falls back when version is empty",
			info: &debug.BuildInfo{
				Main: debug.Module{
					Version: "",
				},
			},
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := versionFromBuildInfo(tt.info, tt.ok)

			require.Equal(t, tt.want, got)
		})
	}
}

func TestRunCommandForeground(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "success",
			args:       []string{"--foreground", "0", "printf", "ok"},
			wantCode:   ExitSuccess,
			wantStdout: "ok",
		},
		{
			name:       "stdin passthrough",
			args:       []string{"--foreground", "0", "cat"},
			stdin:      "input",
			wantCode:   ExitSuccess,
			wantStdout: "input",
		},
		{
			name:     "exit status",
			args:     []string{"--foreground", "0", "sh", "-c", "exit 42"},
			wantCode: 42,
		},
		{
			name:       "stderr passthrough",
			args:       []string{"--foreground", "0", "sh", "-c", "printf err >&2"},
			wantCode:   ExitSuccess,
			wantStderr: "err",
		},
		{
			name:       "not found",
			args:       []string{"--foreground", "0", "go-timeout-command-not-found"},
			wantCode:   ExitNotFound,
			wantStderr: "failed to run command",
		},
		{
			name:       "absolute path not found",
			args:       []string{"--foreground", "0", "/tmp/go-timeout-command-not-found"},
			wantCode:   ExitNotFound,
			wantStderr: "failed to run command",
		},
		{
			name:     "timeout",
			args:     []string{"--foreground", "0.01s", "sleep", "1"},
			wantCode: ExitTimedOut,
		},
		{
			name: "preserve status",
			args: []string{
				"--foreground", "--preserve-status", "0.01s",
				"sh", "-c", "trap 'exit 42' TERM; while true; do :; done",
			},
			wantCode: 42,
		},
		{
			name:       "verbose signal",
			args:       []string{"--foreground", "--verbose", "0.01s", "sleep", "1"},
			wantCode:   ExitTimedOut,
			wantStderr: "sending signal TERM",
		},
		{
			name: "kill after",
			args: []string{
				"--foreground", "--kill-after=0.01s", "0.01s",
				"sh", "-c", "trap '' TERM; while true; do :; done",
			},
			wantCode: signalExitCode(syscall.SIGKILL),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)

			got := Run(tt.args, Streams{
				Stdin:  strings.NewReader(tt.stdin),
				Stdout: stdout,
				Stderr: stderr,
			})

			require.Equal(t, tt.wantCode, got)
			require.Contains(t, stdout.String(), tt.wantStdout)
			require.Contains(t, stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunCommandCannotInvoke(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "not-executable")

	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600))

	stderr := new(bytes.Buffer)
	got := Run([]string{"--foreground", "0", path}, Streams{
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
		Stderr: stderr,
	})

	require.Equal(t, ExitCannotInvoke, got)
	require.Contains(t, stderr.String(), "failed to run command")
}
