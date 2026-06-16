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

// ============================================================================
//  Test Section
// ============================================================================

func TestTimeout(t *testing.T) {
	t.Parallel()

	// TIMEOUT_BIN を読んで exec.CommandContext で実行
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
	Name  string            `yaml:"name"`
	Args  []string          `yaml:"args"`
	Stdin string            `yaml:"stdin"`
	Env   map[string]string `yaml:"env"`
	Want  Want              `yaml:"want"`
}

// Want represents the expected outcomes of a test case, including exit code and
// assertions for stdout and stderr.
type Want struct {
	ExitCode int        `yaml:"exit_code"`
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

	err := cmd.Run()

	require.NoError(t, ctx.Err(), "test case timed out after %s", timeout)

	exitCode := 0

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			require.NoError(t, err, "failed to run command")
		}
	}

	assert.Equal(t, testCase.Want.ExitCode, exitCode, "exit code mismatch")
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
