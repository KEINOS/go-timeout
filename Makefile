.PHONY: all build build-cmd build-img check check-full clean lint lint-check test
.PHONY: test-build-cmd test-e2e
.PHONY: check-checkmake-exist check-docker check-docker-cli-exist check-docker-is-up
.PHONY: check-golangci-lint-exist check-markdownlint-cli2-exist check-yamlfmt-exist
.PHONY: lint-e2e lint-e2e-check lint-go lint-go-check lint-makefile lint-md
.PHONY: lint-md-check lint-yaml lint-yaml-check

# =============================================================================
# Commands (alphabetical order)
# =============================================================================

all: build

build: build-cmd test-build-cmd

build-cmd:
	@printf "* Building the project..."
	@mkdir -p ./dist  || (echo "FAIL: creating dist directory" && exit 1)
	@CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ./dist/timeout ./cmd/timeout && echo "OK" || (echo "FAIL: building the project" && exit 1)

build-img: check-docker
	@printf "* Building Docker image..."
	@docker build -t timeout:test . && echo "OK" || (echo "FAIL: building Docker image" && exit 1)

check: test lint-check lint-md-check lint-yaml-check

check-full: check test-e2e

check-checkmake-exist:
	@command -v checkmake >/dev/null 2>&1 || (echo "FAIL: checkmake command is not installed" && exit 1)

check-docker: check-docker-cli-exist check-docker-is-up
	@echo "* Docker is ready to use"

check-docker-cli-exist:
	@command -v docker >/dev/null 2>&1 || (echo "FAIL: docker command is not installed" && exit 1)

check-docker-is-up:
	@docker info >/dev/null 2>&1 || (echo "FAIL: Docker server is not up/running" && exit 1)

check-golangci-lint-exist:
	@command -v golangci-lint >/dev/null 2>&1 || (echo "FAIL: golangci-lint command is not installed" && exit 1)

check-markdownlint-cli2-exist:
	@command -v markdownlint-cli2 >/dev/null 2>&1 || (echo "FAIL: markdownlint-cli2 command is not installed" && exit 1)

check-yamlfmt-exist:
	@command -v yamlfmt >/dev/null 2>&1 || (echo "FAIL: yamlfmt command is not installed" && exit 1)

clean:
	@printf "* Cleaning up build artifacts..."
	@rm -rf ./dist || (echo "FAIL: cleaning build artifacts" && exit 1)
	@echo "OK"

lint: lint-e2e lint-go lint-makefile lint-md lint-yaml

lint-check: lint-e2e-check lint-go-check lint-makefile

lint-e2e: check-golangci-lint-exist
	@echo "* Modernizing end-to-end tests go syntax..."
	@go fix -tags=e2e ./test/e2e/... && echo "0 issues."
	@echo "* Running end-to-end tests lint..."
	@golangci-lint run --fix --build-tags "e2e" ./test/e2e/...

lint-e2e-check: check-golangci-lint-exist
	@echo "* Checking end-to-end tests lint..."
	@golangci-lint run --build-tags "e2e" ./test/e2e/...

lint-go: check-golangci-lint-exist
	@echo "* Modernizing go syntax..."
	@go fix ./... && echo "0 issues."
	@echo "* Running go lint w/fix..."
	@golangci-lint run --fix

lint-go-check: check-golangci-lint-exist
	@echo "* Checking go lint..."
	@golangci-lint run

lint-makefile: check-checkmake-exist
	@echo "* Running Makefile lint..."
	@checkmake Makefile && echo "0 issues."

lint-md: check-markdownlint-cli2-exist
	@echo "* Running markdown lint w/fix..."
	@markdownlint-cli2 --fix "**/*.md" 1>/dev/null && echo "0 issues."

lint-md-check: check-markdownlint-cli2-exist
	@echo "* Checking markdown lint..."
	@markdownlint-cli2 "**/*.md" 1>/dev/null && echo "0 issues."

lint-yaml: check-yamlfmt-exist
	@echo "* Running YAML formatter w/fix..."
	@yamlfmt . && echo "0 issues."

lint-yaml-check: check-yamlfmt-exist
	@echo "* Checking YAML formatting..."
	@yamlfmt -lint .

test:
	@echo "* Running unit tests with coverage and race detection..."
	@go test -cover -race ./...

test-build-cmd:
	@printf "* Smoke testing the executable..."
	@./dist/timeout --help > /dev/null && echo "OK" || (echo "FAIL: smoke testing the executable" && exit 1)
	@echo "* Build completed ==> ./dist/timeout"

test-e2e: build-cmd
	@echo "* Running end-to-end tests..."
	@TIMEOUT_BIN="$(PWD)/dist/timeout"\
	 TIMEOUT_E2E_SCENARIOS_DIR="$(PWD)/testdata/e2e-scenarios" \
	 go test -tags=e2e -race ./test/e2e/...
