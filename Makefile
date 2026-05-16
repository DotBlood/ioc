SHELL    := bash
BINARY   := iocctl
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR := bin
DIST_DIR := dist
GO       := go
GOOS     := $(shell $(GO) env GOOS)
GOARCH   := $(shell $(GO) env GOARCH)
LDFLAGS  := -ldflags="-s -w -X main.version=$(VERSION)"

RACE_PKGS   := ./internal/... ./pkg/...
BENCH_PKGS  := ./internal/graph/ ./internal/embedding/ ./internal/retrieval/ ./internal/store/
SHA256      := $(shell command -v sha256sum 2>/dev/null || command -v shasum 2>/dev/null || echo "sha256sum")

PLATFORMS := \
    linux/amd64 \
    linux/arm64 \
    darwin/amd64 \
    darwin/arm64 \
    windows/amd64 \
    windows/arm64

.PHONY: all
.PHONY: dev ci
.PHONY: build build-all build-linux build-darwin build-windows
.PHONY: test test-race test-short test-coverage
.PHONY: bench bench-short
.PHONY: lint vet fmt tidy
.PHONY: release install clean help

## ---- Combined ----

all: tidy fmt vet test build           ## Full pipeline (default)

dev: fmt vet test build                 ## Fast local feedback

ci: vet test-race build-all             ## CI pipeline

## ---- Build ----

build:                                 ## Build for current platform
	mkdir -p $(BUILD_DIR) && \
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe) ./cmd/$(BINARY)

build-all:                               ## Cross-compile all platforms
	@mkdir -p $(DIST_DIR) && \
	count=0; \
	for plat in $(PLATFORMS); do \
	    os=$${plat%/*}; arch=$${plat#*/}; \
	    ext=$$([ "$$os" = windows ] && echo ".exe"); \
	    echo "  $$os/$$arch"; \
	    GOOS=$$os GOARCH=$$arch $(GO) build $(LDFLAGS) \
	        -o $(DIST_DIR)/$(BINARY)-$$os-$$arch$$ext ./cmd/$(BINARY); \
	    count=$$((count + 1)); \
	done; \
	echo "Done — $$count binaries in $(DIST_DIR)/"

build-linux:                           ## Cross-compile linux/amd64 + linux/arm64
	$(MAKE) build-all PLATFORMS="$(filter linux/%,$(PLATFORMS))"

build-darwin:                          ## Cross-compile darwin/amd64 + darwin/arm64
	$(MAKE) build-all PLATFORMS="$(filter darwin/%,$(PLATFORMS))"

build-windows:                         ## Cross-compile windows/amd64 + windows/arm64
	$(MAKE) build-all PLATFORMS="$(filter windows/%,$(PLATFORMS))"

## ---- Test ----

test:                                  ## Run all tests (no race, works everywhere)
	$(GO) test -count=1 ./...

test-race:                             ## Run all tests with -race (needs CGO/gcc)
	CGO_ENABLED=1 $(GO) test -race -count=1 $(RACE_PKGS)

test-short:                            ## Run tests in short mode (skip 100K benchmarks)
	$(GO) test -count=1 -short ./...

test-coverage:                         ## Generate coverage profile + HTML report
	$(GO) test -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

## ---- Benchmark ----

bench:                                 ## Full benchmark suite (GOMAXPROCS=1, 5 iter)
	GOMAXPROCS=1 $(GO) test -bench=. -benchmem -count=5 -timeout=300s $(BENCH_PKGS)

bench-short:                           ## Quick benchmark validation (1 iter)
	$(GO) test -bench=. -benchtime=1x -count=1 -timeout=60s $(BENCH_PKGS)

## ---- Quality ----

lint:                                  ## Run golangci-lint
	golangci-lint run ./...

vet:                                   ## Run go vet
	$(GO) vet ./...

fmt:                                   ## Run go fmt
	$(GO) fmt ./...

tidy:                                  ## Run go mod tidy
	$(GO) mod tidy

## ---- Release ----

release: build-all                     ## Cross-compile all platforms + SHA256 checksums
	cd $(DIST_DIR) && $(SHA256) $(BINARY)-* > SHA256SUMS
	@echo "Release artifacts ready in $(DIST_DIR)/"

## ---- Utility ----

install:                               ## Install binary via go install
	$(GO) install $(LDFLAGS) ./cmd/$(BINARY)

clean:                                 ## Remove build artifacts
	cd . && rm -rf $(BUILD_DIR) $(DIST_DIR) coverage.out coverage.html; :

help:                                  ## Show this help
	@echo "IOC Makefile — targets"
	@echo ""
	@grep -Eh '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sort | \
	    sed 's/:.*## /:/' | \
	    awk -F: '{printf "  %-20s %s\n", $$1, $$2}'
