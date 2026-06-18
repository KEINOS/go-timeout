package timeout_test

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/KEINOS/go-timeout/timeout"
)

func ExampleParse() {
	cmdArgs := []string{
		"-k", "1s", "-s", "TERM", "2s",
		"printf", "ok",
	}

	config, err := timeout.Parse(cmdArgs)

	fmt.Println("error:", err)
	fmt.Println("kill after:", config.KillAfter)
	fmt.Println("duration:", config.Duration)
	fmt.Println("command:", config.Command)
	// Output:
	// error: <nil>
	// kill after: 1s
	// duration: 2s
	// command: [printf ok]
}

func ExampleRun() {
	cmdArgs := []string{
		"-k", "1s", "-s", "TERM", "2s",
		"go", "version",
	}

	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := timeout.Run(cmdArgs, timeout.Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: stdout,
		Stderr: stderr,
	})

	output := strings.Join(strings.Fields(stdout.String())[0:2], " ")

	fmt.Println("exit code:", exitCode)
	fmt.Println(strings.TrimSpace("stderr: " + stderr.String()))
	fmt.Println(strings.TrimSpace("stdout: " + output))
	// Output:
	// exit code: 0
	// stderr:
	// stdout: go version
}

func ExampleRun_help() {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	exitCode := timeout.Run([]string{"--help"}, timeout.Streams{
		Stdin:  bytes.NewReader(nil),
		Stdout: stdout,
		Stderr: stderr,
	})

	fmt.Println("exit code:", exitCode)
	fmt.Println(strings.TrimSpace("stderr: " + stderr.String()))
	fmt.Println(strings.TrimSpace("stdout: " + stdout.String()[:6]))
	// Output:
	// exit code: 0
	// stderr:
	// stdout: Usage:
}
