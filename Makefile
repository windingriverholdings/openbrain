GO      ?= /snap/go/current/bin/go
GOTOOLCHAIN := local
export GOTOOLCHAIN
BINDIR  := bin
CMDS    := openbrain openbrain-mcp openbrain-web openbrain-telegram openbrain-slack openbrain-watchd
BINS    := $(addprefix $(BINDIR)/,$(CMDS))
BUILD_TAGS ?=

.PHONY: all build build-ocr test test-cover test-verbose lint vet clean install fixtures setup-db

## Default: show help
all: help

build: $(BINS)

$(BINDIR)/%: cmd/%/*.go internal/**/*.go go.mod go.sum
	@mkdir -p $(BINDIR)
	$(GO) build $(if $(BUILD_TAGS),-tags $(BUILD_TAGS)) -o $@ ./cmd/$*

## Build all binaries with OCR support (requires tesseract-ocr + libtesseract-dev)
build-ocr:
	$(MAKE) build BUILD_TAGS=ocr

## Run all unit tests
test:
	$(GO) test ./internal/... -count=1

## Run tests with verbose output
test-verbose:
	$(GO) test ./internal/... -v -count=1

## Run tests with coverage report
test-cover:
	$(GO) test ./internal/... -v -count=1 -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -func=coverage.out
	@echo ""
	@echo "HTML report: go tool cover -html=coverage.out"

## Run go vet on all packages
vet:
	$(GO) vet ./...

## Run linters (vet + staticcheck if installed)
lint: vet
	@which staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed (go install honnef.co/go/tools/cmd/staticcheck@latest)"

## Generate test fixtures from Python implementation
fixtures:
	pixi run python scripts/generate_fixtures.py

## Run database migrations
setup-db:
	bash scripts/setup-db.sh

## Install binaries to GOPATH/bin
install:
	$(GO) install ./cmd/openbrain
	$(GO) install ./cmd/openbrain-mcp
	$(GO) install ./cmd/openbrain-web

## Remove build artifacts
clean:
	rm -rf $(BINDIR) coverage.out

## Show binary sizes
sizes: build
	@ls -lh $(BINDIR)/ | grep -v total

## Quick check: vet + test
check: vet test

## Full CI pipeline: lint + test with coverage
ci: lint test-cover

## Help
help:
	@echo "OpenBrain Go — available targets:"
	@echo ""
	@echo "  make build         Build all 6 binaries"
	@echo "  make build-ocr     Build with OCR support (needs tesseract)"
	@echo "  make test          Run unit tests"
	@echo "  make test-verbose  Run tests with verbose output"
	@echo "  make test-cover    Run tests with coverage report"
	@echo "  make vet           Run go vet"
	@echo "  make lint          Run vet + staticcheck"
	@echo "  make check         Quick check (vet + test)"
	@echo "  make ci            Full CI (lint + coverage)"
	@echo "  make fixtures      Regenerate test fixtures from Python"
	@echo "  make setup-db      Run database migrations"
	@echo "  make install       Install binaries to GOPATH"
	@echo "  make clean         Remove build artifacts"
	@echo "  make sizes         Show binary sizes"
	@echo "  make help          Show this help"
