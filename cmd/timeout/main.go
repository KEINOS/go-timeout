//nolint:gochecknoglobals // allow global variables for monkey patching
/*
Copyright 2024 KEINOS Software Ltd.

*/
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/KEINOS/go-timeout/timeout"
)

// ============================================================================
//  Pre-defined variables and constants
// ============================================================================

// ----------------------------------------------------------------------------
//  Default values
// ----------------------------------------------------------------------------

const (
	// NoVersion holds the un-tagged version of the application.
	NoVersion = "(devel)"
)

// ----------------------------------------------------------------------------
//  Errors
// ----------------------------------------------------------------------------

var (
	errMissingArgs = errors.New("missing arguments")
)

// ----------------------------------------------------------------------------
//  Monkey patching variables
// ----------------------------------------------------------------------------

// osExit is a copy of os.Exit that can be overridden in tests to prevent the
// program from exiting.
var osExit = os.Exit

// ============================================================================
//  Main functions
// ============================================================================

func main() {
	err := run(os.Args)
	exitOnError(err)
}

//nolint:forbidigo // allow fmt.Println for demonstration purposes
func run(osArgs []string) error {
	err := errorOnMissingArgs(osArgs)
	if err != nil {
		fmt.Println("miss!")
		fmt.Printf("osArgs: %v\n", osArgs)
	}

	fmt.Println("Hello, Timeout!")

	return wrapError(timeout.Placeholder(err),
		"failed to run the application")
}

// ============================================================================
//  Helper functions
// ============================================================================

func wrapError(err error, msg string) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", msg, err)
}

func errorOnMissingArgs(osArgs []string) error {
	const commandOnly = 1

	if len(osArgs) <= commandOnly {
		return errMissingArgs
	}

	return nil
}

func exitOnError(err error) {
	if err != nil {
		osExit(1)
	}
}
