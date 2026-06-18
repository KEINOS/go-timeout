# E2E Test Scenarios

This directory contains E2E test scenarios for the built `timeout` binary.

- Each scenario is defined in a single YAML file
- The `e2e` test harness (`test/e2e/`) runs the `timeout` binary with the specified arguments in the scenario and checks the exit code and output streams against the expected results.
- Malformed YAML files are rejected by the test harness.

## Suite Fields

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Non-empty scenario suite name. |
| `timeout` | no | Per-case test deadline. Defaults to `30s` when omitted. |
| `cases` | yes | Non-empty list of test cases. |

## Case Fields

| Field | Required | Description |
| --- | --- | --- |
| `name` | yes | Non-empty test case name. |
| `args` | no | Arguments passed to the `timeout` binary. |
| `stdin` | no | Text sent to the command's standard input. |
| `env` | no | Extra environment variables for the test process. |
| `send_signal` | no | Signal sent to the top-level `timeout` process. |
| `send_signal_after` | no | Delay before sending `send_signal`. |
| `want` | yes | Expected exit code and stream assertions. |

`send_signal` and `send_signal_after` must be set together. Supported signal
names are `HUP`, `INT`, `QUIT`, and `TERM`.

## Expected Results

Each case must define `want.exit_code` explicitly, including successful cases
that expect `0`.

`stdout` and `stderr` can use these assertions:

| Field | Description |
| --- | --- |
| `equals` | Exact stream contents. |
| `contains` | List of substrings that must appear. |
| `matches` | List of regular expressions that must match. |

## Example

```yaml
name: help
timeout: 10s

cases:
  - name: show help
    args: ["--help"]
    stdin: ""
    want:
      exit_code: 0
      stdout:
        contains:
          - "Usage:"
        matches:
          - "timeout \\[OPTION\\]"
      stderr:
        equals: ""
```
