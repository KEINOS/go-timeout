# End-to-End Test Package

This package tests the built `timeout` command as a real program (E2E tests).

It reads YAML scenarios from `testdata/e2e-scenarios/`. For each case, it runs the binary with the given arguments and checks the exit code, standard output, and standard error.

Run them with:

```shell
make test-e2e
```

## Note

The tests use the `e2e` build tag and require `TIMEOUT_BIN` and `TIMEOUT_E2E_SCENARIOS_DIR` environment variables to be set.

The Makefile builds the command and sets the required environment variables. For manual testing, you need to build the command and set the environment variables yourself.

You can set them like this:

```shell
export TIMEOUT_BIN="$(PWD)/dist/timeout"
export TIMEOUT_E2E_SCENARIOS_DIR="$(PWD)/testdata/e2e-scenarios"
go test -tags=e2e -race ./test/e2e/...
```
