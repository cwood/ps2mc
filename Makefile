BIN := bin/ps2mc
PKG := ./cmd/ps2mc

# Test fixtures. Override from the environment, e.g.
#   PS2MC_FIXTURES=/path/to/cards make test
# wildcard leaves these empty when the defaults are absent, so the tests skip
# instead of failing on an unreadable path.
PS2MC_FIXTURES ?= $(wildcard $(CURDIR)/testdata/fixtures)
ECC_VECTORS ?= $(wildcard $(CURDIR)/testdata/ecc_vectors.txt)
export PS2MC_FIXTURES
export ECC_VECTORS

GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null)

.DEFAULT_GOAL := all

.PHONY: all
all: fmt vet lint test build

.PHONY: build
build:
	go build -o $(BIN) $(PKG)

.PHONY: test
test:
	go test ./...

.PHONY: fmt
fmt:
	gofmt -l -w .

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
ifdef GOLANGCI_LINT
	golangci-lint run ./...
else
	@echo "golangci-lint not found; falling back to go vet + gofmt -l"
	@echo "install it with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting (run 'make fmt')"; exit 1; }
endif

.PHONY: clean
clean:
	rm -rf bin
	go clean -testcache

.PHONY: help
help:
	@echo "Targets:"
	@echo "  all    - fmt, vet, lint, test, build (default)"
	@echo "  build  - build the CLI to ./$(BIN)"
	@echo "  test   - run go test ./... with fixture env vars"
	@echo "  lint   - golangci-lint if installed, else go vet + gofmt -l"
	@echo "  fmt    - gofmt -l -w ."
	@echo "  vet    - go vet ./..."
	@echo "  clean  - remove ./bin and the test cache"
	@echo "  help   - this message"
	@echo ""
	@echo "Fixture variables (override from the environment):"
	@echo "  PS2MC_FIXTURES = $(PS2MC_FIXTURES)"
	@echo "  ECC_VECTORS    = $(ECC_VECTORS)"
	@echo "  (empty means the fixture is missing and its tests skip)"
