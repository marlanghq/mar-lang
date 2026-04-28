# Mar build / test commands.
#
# Usage:
#   make            # build the binary (./mar)
#   make test       # run the Go test suite
#   make check-examples
#                   # type-check every example under examples/
#   make install    # install ./mar into $GOBIN (defaults to ~/go/bin)
#   make clean      # remove the local binary
#
# Versioning info is embedded via -ldflags. To override the version string
# at build time, run e.g. `make build VERSION=0.2.0`.

VERSION ?= $(shell tr -d '\r\n' < VERSION 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

LDFLAGS := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)'

.PHONY: all build test check-examples install clean

all: build

build:
	@echo "Building mar $(VERSION) ($(COMMIT))"
	@go build -ldflags "$(LDFLAGS)" -o mar ./cmd/mar
	@echo "Built ./mar"

test:
	@go test ./...

check-examples: build
	@ok=0; fail=0; \
	for f in examples/*.mar; do \
		if ./mar check "$$f" > /dev/null 2>&1; then \
			echo "  OK   $$f"; ok=$$((ok+1)); \
		else \
			echo "  FAIL $$f"; fail=$$((fail+1)); \
		fi; \
	done; \
	for d in examples/*/; do \
		if ls "$$d"*.mar > /dev/null 2>&1; then \
			if ./mar check "$$d" > /dev/null 2>&1; then \
				echo "  OK   $$d"; ok=$$((ok+1)); \
			else \
				echo "  FAIL $$d"; fail=$$((fail+1)); \
			fi; \
		fi; \
	done; \
	echo ""; \
	echo "$$ok passed, $$fail failed"; \
	test "$$fail" = 0

install:
	@go install -ldflags "$(LDFLAGS)" ./cmd/mar
	@echo "Installed mar"

clean:
	@rm -f mar
	@echo "Cleaned"
