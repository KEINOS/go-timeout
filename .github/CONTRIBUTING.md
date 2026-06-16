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
| `timeout/timeout.go` | Core library: parsing, signal handling, runner |
| `timeout/timeout_unit_test.go` | Unit tests for parsing and run behavior |
| `timeout/timeout_example_test.go` | Go example tests for public usage |
| `cmd/timeout/main.go` | CLI entry point over `timeout.Run` |
| `cmd/timeout/main_unit_test.go` | Tests for `main`, `run`, and `osExit` override |
| `test/e2e/timeout_test.go` | YAML scenario-driven E2E harness |
| `testdata/e2e-scenarios/*.yml` | E2E behavior scenarios |
| `Makefile` | Build, lint, and test orchestration |
| `Dockerfile` | Multi-stage build and scratch image |
| `.github/workflows/` | CI workflows |
| `.golangci.yml` | Go lint configuration |

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

The initial compatibility target is GNU-compatible Unix behavior. Linux is the
primary reference for process groups, signals, and exit statuses. macOS support
is desirable. Windows support is not part of the initial target.
