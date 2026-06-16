package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const commandName = "timeout"

func Test_errorOnMissingArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		osArgs []string
		want   error
	}{
		{
			name:   "returns error when args are empty",
			osArgs: []string{},
			want:   errMissingArgs,
		},
		{
			name:   "returns error when only command exists",
			osArgs: []string{commandName},
			want:   errMissingArgs,
		},
		{
			name:   "returns nil when user args exist",
			osArgs: []string{commandName, "--help"},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := errorOnMissingArgs(tt.osArgs)
			if tt.want == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.want)
		})
	}
}

//nolint:paralleltest // no parallel due to editing global variables
func Test_main(t *testing.T) {
	oldOsExit := osExit
	oldOsArgs := os.Args

	t.Cleanup(func() {
		osExit = oldOsExit
		os.Args = oldOsArgs
	})

	// Mock
	actualExitCode := 0
	osExit = func(code int) {
		actualExitCode = code

		panic("osExit called")
	}

	os.Args = []string{commandName}

	require.Panics(t, func() {
		main()
	})

	expectedExitCode := 1
	require.Equal(t, expectedExitCode, actualExitCode)
}
