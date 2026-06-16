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

### Linux and macOS

Full support for all features, including process-group signaling and all POSIX
signals.

### Windows (best-effort)

Supported on Windows with the following limitations:

- **Process groups**: Windows lacks POSIX process groups. The timeout command
  only signals the direct process; descendants spawned by the command are not
  tracked or terminated.
- **Signals**: Only process termination is delivered. All requested signals
  (e.g., `SIGTERM`, `SIGHUP`, `SIGCONT`) are mapped to forced termination
  (`SIGKILL`). POSIX job-control signals (`SIGCONT`, `SIGSTOP`, etc.) are
  rejected at parse time.

These are documented platform boundaries, not bugs. Process-group-based termination
and signal delivery on Windows may improve in future releases.

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
