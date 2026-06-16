// Package main provides the timeout command entry point.
package main

import (
	"os"

	"github.com/KEINOS/go-timeout/timeout"
)

var osExit = os.Exit //nolint:gochecknoglobals // overridden by tests.

func main() {
	osExit(run(os.Args[1:]))
}

func run(args []string) int {
	return timeout.Run(args, timeout.Streams{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
}
