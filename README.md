# go-timeout

`timeout` is a Go port of GNU Coreutils `timeout`.

It starts a command, waits for a duration, and terminates the command if it is
still running.

## Usage

```shell
timeout [OPTION]... DURATION COMMAND [ARG]...
```

Examples:

```shell
timeout 5s sleep 10
timeout --signal=KILL 1s long-running-command
timeout --kill-after=2s 5s command-that-may-ignore-term
```

Supported options:

- `-f`, `--foreground`
- `-k DURATION`, `--kill-after=DURATION`
- `-p`, `--preserve-status`
- `-s SIGNAL`, `--signal=SIGNAL`
- `-v`, `--verbose`
- `--help`
- `--version`

## Install

Install from source:

```shell
go install github.com/KEINOS/go-timeout/cmd/timeout@latest
```

Or build the local checkout:

```shell
make build
```

The local binary is written to:

```text
dist/timeout
```

## Platform Support

- **Linux, macOS**: fully supported, including process-group signaling and all POSIX signals.
- **Windows**: best-effort. Because Windows has no POSIX process groups or signals, the command is force-terminated (`TerminateProcess`): any requested `--signal` is mapped to a forced kill, descendant processes the command spawns are not terminated, and job-control signal names (`SIGCONT`, `SIGSTOP`, ...) are rejected at parse time.

## Usage as a Library

The implementation lives in the `timeout` package and can be used from Go code.
See [CONTRIBUTING.md](./.github/CONTRIBUTING.md) for the repository layout and
development notes.

## Contributing

Issues and bug reports are welcome:

<https://github.com/KEINOS/go-timeout/issues>

Before sending changes, run:

```shell
make check-full
```

## License

GPL-3.0. See [LICENSE.txt](./LICENSE.txt).
