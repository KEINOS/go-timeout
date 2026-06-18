# End-to-End Test Package

This package tests the built `timeout` command as a real program.

It reads YAML scenarios from `testdata/e2e-scenarios/`. For each case, it runs
the binary with the given arguments and checks the exit code, standard output,
and standard error.

The tests use the `e2e` build tag. Run them with:

```shell
make test-e2e
```

The Makefile builds the command and sets the required environment variables.
