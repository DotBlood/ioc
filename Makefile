SHELL     := bash
GO        := go
BUILD_DIR := bin
GOOS      := $(shell $(GO) env GOOS)
EXE       := $(if $(filter windows,$(GOOS)),.exe)

EMBED     ?=
SCENARIO  ?= internal/eval/scenarios/hoe.json

.PHONY: all dev build test test-race vet fmt tidy lint scenario clean help

## ---- Combined ----

all: tidy fmt vet test build            ## tidy → fmt → vet → test → build (default)

dev: fmt vet test build                 ## fast local feedback

## ---- Build ----

build:                                  ## Build cmd/ioc and cmd/ioc-mcp into bin/
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/ioc$(EXE) ./cmd/ioc
	$(GO) build -o $(BUILD_DIR)/ioc-mcp$(EXE) ./cmd/ioc-mcp

## ---- Test ----

test:                                   ## Run all tests
	$(GO) test -count=1 ./...

test-race:                              ## Run tests with -race (needs CGO)
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...

## ---- Quality ----

vet:                                    ## go vet
	$(GO) vet ./...

fmt:                                    ## go fmt
	$(GO) fmt ./...

tidy:                                   ## go mod tidy
	$(GO) mod tidy

lint:                                   ## golangci-lint
	golangci-lint run ./...

## ---- Eval ----

scenario:                               ## Run the dogfood scenario (EMBED=http://127.0.0.1:8088 for real)
	$(GO) run ./cmd/ioc run-scenario $(SCENARIO) $(if $(EMBED),-embed $(EMBED))

## ---- Utility ----

clean:                                  ## Remove build artifacts
	rm -rf $(BUILD_DIR) coverage.out coverage.html

help:                                   ## Show targets
	@grep -Eh '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /:/' | \
	    awk -F: '{printf "  %-14s %s\n", $$1, $$2}'
