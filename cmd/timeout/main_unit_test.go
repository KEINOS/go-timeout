package main

import (
	"os"
	"testing"

	"github.com/KEINOS/go-timeout/timeout"
	"github.com/stretchr/testify/require"
)

func Test_run(t *testing.T) {
	t.Parallel()

	require.Equal(t, timeout.ExitSuccess, run([]string{"--help"}))
}

//nolint:paralleltest // no parallel due to editing global variables
func Test_main(t *testing.T) {
	oldOsExit := osExit
	oldOsArgs := os.Args

	t.Cleanup(func() {
		osExit = oldOsExit
		os.Args = oldOsArgs
	})

	actualExitCode := 0
	osExit = func(code int) {
		actualExitCode = code

		panic("osExit called")
	}

	os.Args = []string{"timeout", "--help"}

	require.Panics(t, func() {
		main()
	})
	require.Equal(t, timeout.ExitSuccess, actualExitCode)
}
