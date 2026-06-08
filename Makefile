# IOC build/dev tasks.
#
# Universal: the core targets (build/test/vet/fmt/tidy/scenario/serve) are plain
# `go` invocations, so they work whether `make` runs under cmd/PowerShell or a
# Unix shell. Only clean/help/test-race differ per OS; on Windows we pin the
# shell to cmd.exe so those recipes are deterministic (no git-bash/coreutils
# dependency). `go build -o` creates bin/ itself — no mkdir needed.

GO        := go
BUILD_DIR := bin
EMBED     ?=
DIR       ?=
SCENARIO  ?= internal/eval/scenarios/hoe.json

ifeq ($(OS),Windows_NT)
  SHELL       := cmd.exe
  .SHELLFLAGS := /c
  EXE         := .exe
  # gcc for -race (cgo). Override on the command line (MINGW=...) or add your
  # toolchain to PATH. Default points at the WinLibs install on D:.
  MINGW       ?= D:\tools\mingw64\mingw64\bin
  RACEENV     := set "PATH=$(MINGW);%PATH%" && set "CGO_ENABLED=1" &&
  RM_BIN      := if exist $(BUILD_DIR) rmdir /s /q $(BUILD_DIR)
  RM_COV      := if exist coverage.out del /q coverage.out & if exist coverage.html del /q coverage.html
else
  EXE         :=
  RACEENV     := CGO_ENABLED=1
  RM_BIN      := rm -rf $(BUILD_DIR)
  RM_COV      := rm -f coverage.out coverage.html
endif

.PHONY: all dev build test test-race vet fmt tidy lint scenario serve clean help

all: tidy fmt vet test build            ## tidy -> fmt -> vet -> test -> build (default)

dev: fmt vet test build                 ## fast local feedback

build:                                  ## Build cmd/ioc and cmd/ioc-mcp into bin/
	$(GO) build -o $(BUILD_DIR)/ioc$(EXE) ./cmd/ioc
	$(GO) build -o $(BUILD_DIR)/ioc-mcp$(EXE) ./cmd/ioc-mcp

test:                                   ## Run all tests
	$(GO) test -count=1 ./...

test-race:                              ## Run tests with -race (needs cgo+gcc; Windows: MINGW=path)
	$(RACEENV) $(GO) test -race -count=1 ./...

vet:                                    ## go vet
	$(GO) vet ./...

fmt:                                    ## go fmt
	$(GO) fmt ./...

tidy:                                   ## go mod tidy
	$(GO) mod tidy

lint:                                   ## golangci-lint
	golangci-lint run ./...

scenario:                               ## Dogfood scenario (EMBED=http://127.0.0.1:8088 for real bge)
	$(GO) run ./cmd/ioc run-scenario $(SCENARIO) $(if $(EMBED),-embed $(EMBED))

serve:                                  ## Run the runtime daemon (DIR=.ioc/data EMBED=http://127.0.0.1:8088)
	$(GO) run ./cmd/ioc serve $(if $(DIR),-dir $(DIR)) $(if $(EMBED),-embed $(EMBED))

clean:                                  ## Remove build artifacts
	-$(RM_BIN)
	-$(RM_COV)

help:                                   ## Show targets
	@echo IOC make targets:
	@echo   build       - build bin/ioc and bin/ioc-mcp
	@echo   test        - go test ./...
	@echo   test-race   - go test -race ./... [needs cgo+gcc; Windows MINGW=path]
	@echo   vet fmt tidy lint
	@echo   scenario    - run-scenario [EMBED=... for real bge]
	@echo   serve       - run the runtime daemon [DIR=... EMBED=...]
	@echo   clean       - remove bin/ and coverage files
