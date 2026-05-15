.PHONY: build test lint clean

BINARY := iocctl
GO := go
GOFLAGS := -ldflags="-s -w"

build:
	$(GO) build $(GOFLAGS) -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	$(GO) test -race -count=1 ./...

lint:
	golangci-lint run ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin/

all: tidy fmt vet test build
