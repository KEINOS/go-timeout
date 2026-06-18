# go-timeout

`timeout` is a Go port of GNU Coreutils `timeout`.

It starts a command, waits for a duration, and terminates the command if it is
still running.

## Usage

```shellsession
% timeout --help
Usage:
  timeout [OPTION]... DURATION COMMAND [ARG]...

timeout starts COMMAND, and kill it if still running after DURATION.

Options:
  -f, --foreground           keep COMMAND in the foreground
  -h, --help                 display this help and exit
  -k, --kill-after=DURATION  send KILL if COMMAND is still running later
  -p, --preserve-status      preserve COMMAND status after timeout
  -s, --signal=SIGNAL        specify signal to send on timeout
  -v, --verbose              diagnose sent signals
      --version              output version information and exit

Note:
  By default, timeout sends the TERM signal upon timeout. Use --signal
  to specify a different signal, or use --kill-after to send KILL if
  the command does not terminate after the initial signal.

Examples:
  # Sends SIGTERM after 5 seconds
  timeout 5s sleep 10

  # Sends SIGKILL after 5 seconds
  timeout --signal=KILL 5s long-running-command

  # Sends SIGTERM after 5 seconds, then SIGKILL after 2 more seconds
  timeout --kill-after=2s 5s command-that-may-ignore-term
```

## Install

```shell
# install the latest version
go install github.com/KEINOS/go-timeout/cmd/timeout@latest

# install a specific version
go install github.com/KEINOS/go-timeout/cmd/timeout@v1.0.0
```

Or clone the repository and build:

```shell
# it will build to ./dist/timeout
make build
```

## Usage as a Library

You can also use `timeout` as a library in your Go code. See [CONTRIBUTING.md](./.github/CONTRIBUTING.md) for the repository layout and development notes.

## Contributing

Issues and bug reports are welcome:

<https://github.com/KEINOS/go-timeout/issues>

Before sending changes, run:

```shell
make check-full
```

## License

GPL-3.0. See [LICENSE.txt](./LICENSE.txt).
