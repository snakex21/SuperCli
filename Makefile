BINARY := supercli
PKG := ./...
GO ?= go

.PHONY: build run test tidy clean fmt vet all help

help:
	@echo "SuperCli Makefile"
	@echo "  make build   - compile to ./$(BINARY)"
	@echo "  make run     - run via 'go run ./cmd/supercli'"
	@echo "  make test    - run all tests (verbose)"
	@echo "  make vet     - go vet"
	@echo "  make fmt     - gofmt -w ."
	@echo "  make tidy    - go mod tidy"
	@echo "  make clean   - remove binary, .supercli/supercli-data data, test cache"
	@echo "  make all     - fmt + vet + test + build"

build:
	$(GO) build -o $(BINARY) ./cmd/supercli

run:
	$(GO) run ./cmd/supercli

test:
	$(GO) test -race -count=1 $(PKG)

vet:
	$(GO) vet $(PKG)

fmt:
	$(GO) fmt $(PKG)

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY)
	rm -rf .supercli supercli-data
	$(GO) clean -testcache

all: fmt vet test build
