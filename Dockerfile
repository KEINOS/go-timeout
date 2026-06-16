FROM golang:alpine AS base

RUN \
    apk update --no-cache && \
    apk upgrade --no-cache && \
    apk add --no-cache alpine-sdk build-base

FROM base AS builder

WORKDIR /app

COPY . /app/

# Install checkmake, golangci-lint and yamlfmt
RUN \
    go install github.com/checkmake/checkmake/cmd/checkmake@latest && \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest && \
    go install github.com/google/yamlfmt/cmd/yamlfmt@latest

# Smoke test
RUN \
    make --version && \
    golangci-lint --version && \
    checkmake --version && \
    yamlfmt --version

# Lint Makefile
RUN make lint-makefile

# Lint YAML files
RUN yamlfmt -lint .

# Lint Go code and run unit tests
RUN make lint-go-check lint-e2e-check && \
    make test

# Build and E2E test
RUN \
    make build && \
    make test-e2e


FROM scratch

COPY --from=builder /app/dist/timeout /timeout
ENTRYPOINT ["/timeout"]
