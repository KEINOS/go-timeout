package timeout_test

import (
	"bytes"
	"fmt"

	"github.com/KEINOS/go-timeout/timeout"
)

func ExampleParse() {
	config, err := timeout.Parse([]string{"-k", "1s", "-s", "TERM", "2s", "printf", "ok"})

	fmt.Println(err)
	fmt.Println(config.KillAfter)
	fmt.Println(config.Duration)
	fmt.Println(config.Command)
	// Output:
	// <nil>
	// 1s
	// 2s
	// [printf ok]
}

func ExampleRun_help() {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	code := timeout.Run([]string{"--help"}, timeout.Streams{
		Stdout: stdout,
		Stderr: stderr,
	})

	fmt.Println(code)
	fmt.Println(stderr.String())
	fmt.Println(stdout.String()[:6])
	// Output:
	// 0
	//
	// Usage:
}
