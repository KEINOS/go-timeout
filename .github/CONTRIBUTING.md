# Contributing

Thank you for helping improve `go-timeout`.

## Overview

`go-timeout` is a Go re-implementation of the GNU Coreutils `timeout`
command. It starts a command, and if the command is still running after a given
duration, sends a signal (default `SIGTERM`) to terminate it, optionally
escalating to `SIGKILL` after a grace period.

The code is structured around a reusable library package (`timeout/`) and a
thin CLI wrapper (`cmd/timeout/`). Test coverage includes unit tests, example
tests, and a YAML-driven end-to-end harness.

## Repository Layout

| Path | Purpose |
| ---- | ------- |
| `timeout/timeout.go` | Portable parsing, public API, streams, timers, and signal helpers |
| `timeout/runner_state.go` | Portable command runner and per-run state |
| `timeout/timeout_unix.go` | Unix-only process-group and signal behavior (`//go:build unix`) |
| `timeout/timeout_windows.go` | Windows best-effort behavior (`//go:build windows`) |
| `timeout/timeout_unix_unit_test.go` | Unix unit tests for parsing and run behavior (`//go:build unix`) |
| `timeout/timeout_windows_unit_test.go` | Windows unit tests (`//go:build windows`) |
| `timeout/timeout_example_test.go` | Go example tests for public usage (portable) |
| `cmd/timeout/main.go` | CLI entry point over `timeout.Run` |
| `cmd/timeout/main_unit_test.go` | Tests for `main`, `run`, and `osExit` override |
| `test/e2e/run_e2e_test.go` | YAML scenario-driven E2E runner and helpers |
| `test/e2e/test_e2e_test.go` | Tests for the E2E scenario harness |
| `testdata/e2e-scenarios/*.yml` | E2E behavior scenarios |
| `Makefile` | Build, lint, and test orchestration |
| `Dockerfile` | Multi-stage build and scratch image |
| `.github/workflows/` | CI workflows |
| `.golangci.yml` | Go lint configuration |

## Development Requirements

Use the Go version declared in `go.mod`. The repository checks also require
these commands to be available on `PATH`:

- `golangci-lint`
- `checkmake`
- `markdownlint-cli2`
- `yamlfmt`

Docker is required only for `make build-img`.

## Development Checks

Use the quick check during normal development:

```shell
make check
```

Use the full check before phase review, pull requests, or commits:

```shell
make check-full
```

The full check runs unit tests, lint/static checks, builds the command, and runs
the E2E scenarios.

## Public API Tests

Public package usage should be documented with `Example*` tests where practical.
Keep examples focused on golden path behavior, and keep private edge cases in
unit tests.

## Platform Scope

The primary compatibility target is GNU-compatible Unix behavior. Linux is the
reference for process groups, signals, and exit statuses; macOS is also fully
supported.

Windows is supported on a best-effort basis. Platform-specific code is isolated
behind `//go:build unix` and `//go:build windows` files so the GNU-compatible
Unix behavior is never weakened. Windows has documented limitations (no POSIX
process groups or signals, so the command is force-terminated and job-control
signal names are rejected).
