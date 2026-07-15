BINARY := supercli
WEB_BINARY := supercli-web.exe
PKG := ./...
GO ?= go

.PHONY: build build-web run test tidy clean clean-data fmt vet all help

help:
	@echo "SuperCli Makefile"
	@echo "  make build   - compile to ./$(BINARY)"
	@echo "  make build-web - compile Windows GUI web app to ./$(WEB_BINARY)"
	@echo "  make run     - run via 'go run ./cmd/supercli'"
	@echo "  make test    - run all tests (verbose)"
	@echo "  make vet     - go vet"
	@echo "  make fmt     - gofmt -w ."
	@echo "  make tidy    - go mod tidy"
	@echo "  make clean   - remove build outputs and test cache (preserves runtime data)"
	@echo "  make clean-data - remove .supercli and supercli-data, including the skills pack"
	@echo "  make all     - fmt + vet + test + build"

build:
	$(GO) build -o $(BINARY) ./cmd/supercli

build-web:
	$(GO) build -ldflags="-H=windowsgui" -o $(WEB_BINARY) ./cmd/supercli-web

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
	rm -f $(WEB_BINARY)
	rm -f *.test *.test.exe headerprobe.exe coverage.out stdout.log stderr.log
	rm -rf .tmp-home
	$(GO) clean -testcache

clean-data:
	rm -rf .supercli supercli-data

all: fmt vet test build
