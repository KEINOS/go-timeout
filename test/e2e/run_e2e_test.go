//go:build e2e

// Package e2e_test tests the timeout binary from end to end.
//
// VS Code CodeLens tests require a prebuilt binary in the dist directory.
// Press Command+Shift+B to build it.
//
//nolint:tagliatelle // want snake_case YAML tags
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

// Suite describes a group of test cases from a YAML file.
type Suite struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
	Cases   []Case        `yaml:"cases"`
}

// Case describes one test case and its expected result.
type Case struct {
	Name            string            `yaml:"name"`
	Args            []string          `yaml:"args"`
	Stdin           string            `yaml:"stdin"`
	Env             map[string]string `yaml:"env"`
	SendSignal      string            `yaml:"send_signal"`
	SendSignalAfter time.Duration     `yaml:"send_signal_after"`
	Want            *Want             `yaml:"want"`
}

// Want describes the expected exit code and output.
type Want struct {
	ExitCode *int       `yaml:"exit_code"`
	Stdout   TextAssert `yaml:"stdout"`
	Stderr   TextAssert `yaml:"stderr"`
}

// TextAssert describes checks for one output stream.
type TextAssert struct {
	Equals   *string  `yaml:"equals"`
	Contains []string `yaml:"contains"`
	Matches  []string `yaml:"matches"`
}

func assertText(t *testing.T, streamName string, got string, want TextAssert) {
	t.Helper()

	if want.Equals != nil {
		assert.Equal(t, *want.Equals, got,
			"%s should exactly match", streamName)
	}

	for _, expected := range want.Contains {
		assert.Contains(t, got, expected,
			"%s should contain expected text", streamName)
	}

	for _, pattern := range want.Matches {
		compiledPattern, err := regexp.Compile(pattern)
		require.NoError(t, err,
			"%s has invalid regex pattern: %s", streamName, pattern)

		assert.Regexp(t, compiledPattern, got,
			"%s should match regex pattern", streamName)
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

	path = filepath.Clean(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read scenario file %q", path)

	suite, err := decodeTestScenario(data)
	require.NoError(t, err, "failed to decode scenario file %q", path)

	return suite
}

func parseCaseSignal(value string) (os.Signal, error) {
	normalized := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(value)), "SIG")

	// Add signals only when a test scenario needs them.
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

	// Reject control characters before using the path.
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

	// Parse again to validate Case values created without YAML.
	sig, err := parseCaseSignal(testCase.SendSignal)
	require.NoError(t, err, "invalid send_signal for test case %q", testCase.Name)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		timer := time.NewTimer(testCase.SendSignalAfter)
		defer timer.Stop()

		select {
		case <-timer.C:
			// Signal timeout, which forwards the signal to the command.
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
