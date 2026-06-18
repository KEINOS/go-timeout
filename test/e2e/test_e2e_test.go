//go:build e2e

package e2e_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_decodeTestScenarioRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()

	data := []byte(`
name: help
cases:
  - name: show help
    want:
      exit_code: 0
---
name: another
cases:
  - name: show another
    want:
      exit_code: 0
`)

	_, err := decodeTestScenario(data)

	require.ErrorContains(t, err, "exactly one YAML document")
}

func Test_decodeTestScenarioSupportsSuiteTimeout(t *testing.T) {
	t.Parallel()

	data := []byte(`
name: timeout field
timeout: 10s
cases:
  - name: show help
    args: ["--help"]
    want:
      exit_code: 0
`)

	suite, err := decodeTestScenario(data)

	require.NoError(t, err)
	require.Equal(t, 10*time.Second, suite.Timeout)
}

func Test_decodeTestScenarioRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	data := []byte(`
name: help
unknown_key: unexpected
cases:
  - name: show help
    want:
      exit_code: 0
`)

	_, err := decodeTestScenario(data)

	require.ErrorContains(t, err, "field unknown_key not found")
}

func Test_decodeTestScenarioRejectsInvalidSignalConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "signal without delay",
			data: []byte(`
name: signal config
cases:
  - name: send signal
    args: ["0", "true"]
    send_signal: TERM
    want:
      exit_code: 0
`),
			want: "send_signal_after",
		},
		{
			name: "delay without signal",
			data: []byte(`
name: signal config
cases:
  - name: send signal
    args: ["0", "true"]
    send_signal_after: 10ms
    want:
      exit_code: 0
`),
			want: "send_signal",
		},
		{
			name: "unsupported signal",
			data: []byte(`
name: signal config
cases:
  - name: send signal
    args: ["0", "true"]
    send_signal: NOPE
    send_signal_after: 10ms
    want:
      exit_code: 0
`),
			want: "unsupported signal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeTestScenario(tt.data)

			require.ErrorContains(t, err, tt.want)
		})
	}
}

func Test_decodeTestScenarioSupportsSignalConfig(t *testing.T) {
	t.Parallel()

	data := []byte(`
name: signal config
cases:
  - name: send signal
    args: ["0", "true"]
    send_signal: TERM
    send_signal_after: 10ms
    want:
      exit_code: 0
`)

	suite, err := decodeTestScenario(data)

	require.NoError(t, err)
	require.Equal(t, "TERM", suite.Cases[0].SendSignal)
	require.Equal(t, 10*time.Millisecond, suite.Cases[0].SendSignalAfter)
}

func Test_decodeTestScenarioRejectsInvalidSuiteSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "empty suite name",
			data: []byte(`
name: ""
cases:
  - name: show help
    args: ["--help"]
    want:
      exit_code: 0
`),
			want: errSuiteNameRequired.Error(),
		},
		{
			name: "empty cases",
			data: []byte(`
name: help
cases: []
`),
			want: errSuiteCasesRequired.Error(),
		},
		{
			name: "empty case name",
			data: []byte(`
name: help
cases:
  - name: ""
    args: ["--help"]
    want:
      exit_code: 0
`),
			want: errCaseNameRequired.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeTestScenario(tt.data)

			require.ErrorContains(t, err, tt.want)
		})
	}
}

func Test_decodeTestScenarioRejectsMissingWantOrExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "missing want",
			data: []byte(`
name: help
cases:
  - name: show help
    args: ["--help"]
`),
			want: errCaseWantRequired.Error(),
		},
		{
			name: "missing exit_code",
			data: []byte(`
name: help
cases:
  - name: show help
    args: ["--help"]
    want:
      stdout:
        contains: ["Usage:"]
`),
			want: errCaseExitCodeRequired.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeTestScenario(tt.data)

			require.ErrorContains(t, err, tt.want)
		})
	}
}

func Test_decodeTestScenarioAcceptsExplicitExitCode(t *testing.T) {
	t.Parallel()

	data := []byte(`
name: help
cases:
  - name: show help
    args: ["--help"]
    want:
      exit_code: 0
      stdout:
        contains: ["Usage:"]
`)

	suite, err := decodeTestScenario(data)

	require.NoError(t, err)
	require.Len(t, suite.Cases, 1)
	require.NotNil(t, suite.Cases[0].Want.ExitCode)
	require.Equal(t, 0, *suite.Cases[0].Want.ExitCode)
}
