//go:build e2e

//nolint:tagliatelle // want snake_case YAML tags
/*
Package e2e_test contains the E2E test harness for `timeout` binary.

Note: **For VSCode users**: To run each test via CodeLens, you need to pre-build
the `timeout` binary. Press `command+shift+B` to trigger building the  binary in
the `dist` directory.
*/
package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	envNameTimeoutBin      = "TIMEOUT_BIN"
	envNameE2EScenariosDir = "TIMEOUT_E2E_SCENARIOS_DIR"

	defaultScenarioTimeout = 30 * time.Second
)

var errMultipleYAMLDocuments = errors.New("scenario file must contain exactly one YAML document")

var (
	errCaseSignalAfterRequired = errors.New("send_signal_after is required when send_signal is set")
	errCaseSignalRequired      = errors.New("send_signal is required when send_signal_after is set")
	errUnsupportedCaseSignal   = errors.New("unsupported signal")
	errSuiteNameRequired       = errors.New("suite name is required")
	errSuiteCasesRequired      = errors.New("suite must contain at least one case")
	errCaseNameRequired        = errors.New("case name is required")
	errCaseWantRequired        = errors.New("case want is required")
	errCaseExitCodeRequired    = errors.New("case want.exit_code is required")
)

// ============================================================================
//  Test Section
// ============================================================================

func TestTimeout(t *testing.T) {
	t.Parallel()

	pathTimeoutBin := getPathTargetBin(t)
	pathTestScenariosDir := getPathDirTestScenarios(t)

	require.FileExists(t, pathTimeoutBin,
		"timeout binary does not exist at path: %s", pathTimeoutBin)
	require.DirExists(t, pathTestScenariosDir,
		"test scenarios directory does not exist at path: %s", pathTestScenariosDir)

	pathsTestScenarios := findPathsTestScenarios(t, pathTestScenariosDir)

	for _, pathTestScenario := range pathsTestScenarios {
		testScenario := loadTestScenario(t, pathTestScenario)
		t.Logf("Scenario: %#+v", testScenario)

		runTestScenario(t, pathTimeoutBin, testScenario)
	}
}

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

// ============================================================================
//  Setup Section
// ============================================================================

// ----------------------------------------------------------------------------
//  YAML structure for test scenarios (in order of use)
// ----------------------------------------------------------------------------

// Suite represents a test suite containing multiple test cases.
type Suite struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
	Cases   []Case        `yaml:"cases"`
}

// Case represents a single test case with its configuration and expected outcomes.
type Case struct {
	Name            string            `yaml:"name"`
	Args            []string          `yaml:"args"`
	Stdin           string            `yaml:"stdin"`
	Env             map[string]string `yaml:"env"`
	SendSignal      string            `yaml:"send_signal"`
	SendSignalAfter time.Duration     `yaml:"send_signal_after"`
	Want            *Want             `yaml:"want"`
}

// Want represents the expected outcomes of a test case, including exit code and
// assertions for stdout and stderr.
type Want struct {
	ExitCode *int       `yaml:"exit_code"`
	Stdout   TextAssert `yaml:"stdout"`
	Stderr   TextAssert `yaml:"stderr"`
}

// TextAssert represents the assertions for text output, allowing for equality,
// containment, and regex matching.
type TextAssert struct {
	Equals   *string  `yaml:"equals"`
	Contains []string `yaml:"contains"`
	Matches  []string `yaml:"matches"`
}

// ----------------------------------------------------------------------------
//  Helper functions for the test (alphabetical order)
// ----------------------------------------------------------------------------

func assertText(t *testing.T, streamName string, got string, want TextAssert) {
	t.Helper()

	if want.Equals != nil {
		assert.Equal(t, *want.Equals, got, "%s should exactly match", streamName)
	}

	for _, expected := range want.Contains {
		assert.Contains(t, got, expected, "%s should contain expected text", streamName)
	}

	for _, pattern := range want.Matches {
		compiledPattern, err := regexp.Compile(pattern)
		require.NoError(t, err, "%s has invalid regex pattern: %s", streamName, pattern)

		assert.Regexp(t, compiledPattern, got, "%s should match regex pattern", streamName)
	}
}

func decodeTestScenario(data []byte) (*Suite, error) {
	var suite Suite

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	err := decoder.Decode(&suite)
	if err != nil {
		return nil, fmt.Errorf("decode scenario YAML: %w", err)
	}

	var extra yaml.Node

	err = decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		err := validateTestScenario(&suite)
		if err != nil {
			return nil, err
		}

		return &suite, nil
	} else if err != nil {
		return nil, fmt.Errorf("decode extra YAML document: %w", err)
	}

	return nil, errMultipleYAMLDocuments
}

func findPathsTestScenarios(t *testing.T, pathTestScenariosDir string) []string {
	t.Helper()

	pathsYML, err := filepath.Glob(filepath.Join(pathTestScenariosDir, "*.yml"))
	require.NoError(t, err, "failed to glob .yml scenario files")

	pathsYAML, err := filepath.Glob(filepath.Join(pathTestScenariosDir, "*.yaml"))
	require.NoError(t, err, "failed to glob .yaml scenario files")

	pathsYML = append(pathsYML, pathsYAML...)
	slices.Sort(pathsYML)

	require.NotEmpty(t, pathsYML,
		"test scenarios directory should contain at least one .yml or .yaml file: %s", pathTestScenariosDir)

	return pathsYML
}

func getPathDirTestScenarios(t *testing.T) string {
	t.Helper()

	pathDirTestScenarios, ok := os.LookupEnv(envNameE2EScenariosDir)
	require.True(t, ok,
		"environment variable not set: %s", envNameE2EScenariosDir)

	return resolvedPath(t, pathDirTestScenarios)
}

func getPathTargetBin(t *testing.T) string {
	t.Helper()

	timeoutBinEnv, ok := os.LookupEnv(envNameTimeoutBin)
	require.True(t, ok,
		"environment variable %s is not set; please set it to the path of the timeout binary to test", envNameTimeoutBin)

	return resolvedPath(t, timeoutBinEnv)
}

func loadTestScenario(t *testing.T, path string) *Suite {
	t.Helper()

	// Load the YAML file for the test scenario
	path = filepath.Clean(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read scenario file %q", path)

	suite, err := decodeTestScenario(data)
	require.NoError(t, err, "failed to decode scenario file %q", path)

	return suite
}

func parseCaseSignal(value string) (os.Signal, error) {
	normalized := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "SIG")

	// Keep this list narrow until a scenario needs another signal.
	switch normalized {
	case "HUP":
		return syscall.SIGHUP, nil
	case "INT":
		return syscall.SIGINT, nil
	case "QUIT":
		return syscall.SIGQUIT, nil
	case "TERM":
		return syscall.SIGTERM, nil
	default:
		return nil, fmt.Errorf("%w %q", errUnsupportedCaseSignal, value)
	}
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()

	// Error if the path contains control characters
	for _, char := range path {
		if unicode.IsControl(char) {
			t.Fatalf("path contains control characters: %q", path)
		}
	}

	pathAbsClean, err := filepath.Abs(path)
	require.NoError(t, err,
		"failed to resolve path during test: %v", err)

	return pathAbsClean
}

func scheduleCaseSignal(t *testing.T, process *os.Process, testCase Case) context.CancelFunc {
	t.Helper()

	if testCase.SendSignal == "" && testCase.SendSignalAfter == 0 {
		return func() {}
	}

	// Re-parse here so direct Case construction stays guarded like YAML input.
	sig, err := parseCaseSignal(testCase.SendSignal)
	require.NoError(t, err, "invalid send_signal for test case %q", testCase.Name)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		timer := time.NewTimer(testCase.SendSignalAfter)
		defer timer.Stop()

		select {
		case <-timer.C:
			// Signal the monitor; timeout is responsible for forwarding to children.
			_ = process.Signal(sig)
		case <-ctx.Done():
		}
	}()

	return cancel
}

func runTestCase(t *testing.T, pathTimeoutBin string, timeout time.Duration, testCase Case) {
	t.Helper()

	require.NotEmpty(t, testCase.Name, "test case should have a name")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	//nolint:gosec // Subprocess execution is intentional due to the E2E nature of the test
	cmd := exec.CommandContext(ctx, pathTimeoutBin, testCase.Args...)

	cmd.Stdin = strings.NewReader(testCase.Stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()

	for name, value := range testCase.Env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}

	err := cmd.Start()
	require.NoError(t, err, "failed to start timeout command")

	cancelCaseSignal := scheduleCaseSignal(t, cmd.Process, testCase)
	err = cmd.Wait()

	cancelCaseSignal()

	require.NoError(t, ctx.Err(), "test case timed out after %s", timeout)

	exitCode := 0

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				exitCode = 128 + int(status.Signal())
			}
		} else {
			require.NoError(t, err, "failed to run command")
		}
	}

	require.NotNil(t, testCase.Want, "test case should define want")
	require.NotNil(t, testCase.Want.ExitCode, "test case should define want.exit_code")
	assert.Equal(t, *testCase.Want.ExitCode, exitCode, "exit code mismatch")
	assertText(t, "stdout", stdout.String(), testCase.Want.Stdout)
	assertText(t, "stderr", stderr.String(), testCase.Want.Stderr)
}

func runTestScenario(t *testing.T, pathTimeoutBin string, suite *Suite) {
	t.Helper()

	require.NotNil(t, suite, "test suite should not be nil")
	require.NotEmpty(t, suite.Name, "test suite should have a name")
	require.NotEmpty(t, suite.Cases, "test suite should contain at least one test case")

	t.Run(suite.Name, func(t *testing.T) {
		t.Parallel()

		timeout := suite.Timeout
		if timeout == 0 {
			timeout = defaultScenarioTimeout
		}

		for _, testCase := range suite.Cases {
			t.Run(testCase.Name, func(t *testing.T) {
				t.Parallel()

				runTestCase(t, pathTimeoutBin, timeout, testCase)
			})
		}
	})
}

func validateCaseSignalConfig(testCase Case) error {
	hasSignal := strings.TrimSpace(testCase.SendSignal) != ""
	hasDelay := testCase.SendSignalAfter > 0

	switch {
	case !hasSignal && !hasDelay:
		return nil
	case hasSignal && !hasDelay:
		return errCaseSignalAfterRequired
	case !hasSignal && hasDelay:
		return errCaseSignalRequired
	}

	_, err := parseCaseSignal(testCase.SendSignal)
	if err != nil {
		return err
	}

	return nil
}

func validateTestScenario(suite *Suite) error {
	if strings.TrimSpace(suite.Name) == "" {
		return errSuiteNameRequired
	}

	if len(suite.Cases) == 0 {
		return errSuiteCasesRequired
	}

	for _, testCase := range suite.Cases {
		if strings.TrimSpace(testCase.Name) == "" {
			return fmt.Errorf("case %q: %w", testCase.Name, errCaseNameRequired)
		}

		if testCase.Want == nil {
			return fmt.Errorf("case %q: %w", testCase.Name, errCaseWantRequired)
		}

		if testCase.Want.ExitCode == nil {
			return fmt.Errorf("case %q: %w", testCase.Name, errCaseExitCodeRequired)
		}

		err := validateCaseSignalConfig(testCase)
		if err != nil {
			return fmt.Errorf("case %q: %w", testCase.Name, err)
		}
	}

	return nil
}
