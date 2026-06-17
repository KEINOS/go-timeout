# E2E Test Scenarios

This directory contains E2E test scenarios for the `go-timeout` project.

Each YAML file represents a specific test scenario, and the tests are designed to validate the functionality and behavior of the `timeout` binary in various conditions.

## YAML Formats

```yaml
name: <test scenario name>
timeout: <timeout duration, e.g., "30s">

cases:
  - name: <test case name>
    args: ["<command-line arguments for timeout>"]
    stdin: "<stdin text>"
    send_signal: TERM
    send_signal_after: 100ms
    want:
      exit_code: <expected exit code, e.g., 0>
      stdout:
        contains:
          - "<expected output part match>"
        equals: "<expected exact output>"
      stderr:
        contains:
          - "<expected error output part match>"
        equals: "<expected exact error output>"

  - name: help
    args: ["--help"]
    want:
      exit_code: 0
      stdout:
        contains:
          - "Hello"
      stderr:
        equals: ""
```

`send_signal` and `send_signal_after` are optional. When both are set, the E2E
harness sends the named signal to the top-level `timeout` process after the
given delay. Supported signal names are `HUP`, `INT`, `QUIT`, and `TERM`.
