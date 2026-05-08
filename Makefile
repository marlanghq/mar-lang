# Mar build / test commands.
#
# Usage:
#   make                 # build the binary (./mar) — includes embedded stubs
#   make stubs           # cross-compile mar-runtime for every supported target
#   make test            # run the Go test suite
#   make check-examples  # type-check every example under examples/
#   make vscode          # build the VSCode extension (.vsix)
#   make clean           # remove the local binary + built stubs
#
# The ./mar binary lives in this directory. Add it to your PATH once:
#   export PATH="$(pwd):$PATH"   # or point to the absolute path in your shell rc
#
# Versioning info is embedded via -ldflags. To override the version string
# at build time, run e.g. `make build VERSION=0.2.0`.

VERSION ?= $(shell tr -d '\r\n' < VERSION 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

LDFLAGS := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)'

# Cross-compile targets for the production runtime stub. These get
# embedded into ./mar via go:embed so `mar build --target <X>` works
# without a local Go toolchain for X.
STUB_TARGETS := darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64
STUB_DIR     := internal/appbundle/stubs/binaries

.PHONY: all build stubs test check-examples vscode clean

all: build

build: stubs
	@echo "Building mar $(VERSION) ($(COMMIT))"
	@go build -ldflags "$(LDFLAGS)" -o mar ./cmd/mar
	@echo "Built ./mar"

# Cross-compile mar-runtime for every supported target into STUB_DIR.
# These binaries are then embedded into ./mar by `make build`.
# Using -trimpath + -ldflags='-s -w' to keep stub size small (~6-8 MB
# each); five stubs adds ~30-40 MB to the final ./mar binary, which is
# the trade-off for zero-toolchain cross-compile.
stubs:
	@echo "Cross-compiling mar-runtime stubs"
	@mkdir -p $(STUB_DIR)
	@for t in $(STUB_TARGETS); do \
		os=$$(echo $$t | cut -d- -f1); \
		arch=$$(echo $$t | cut -d- -f2); \
		out="$(STUB_DIR)/$$t"; \
		if [ "$$os" = "windows" ]; then out="$$out.exe"; fi; \
		echo "  $$t -> $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -trimpath -ldflags "$(LDFLAGS)" \
			-o "$$out" ./cmd/mar-runtime || exit 1; \
	done
	@echo "Built $$(ls $(STUB_DIR) | grep -v '^README$$' | wc -l | tr -d ' ') stubs"

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
		if [ -f "$$d.mar-design-only" ]; then \
			echo "  SKIP $$d (design-only; uses unimplemented API)"; \
			continue; \
		fi; \
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

# Builds the VSCode extension into a .vsix package and prints the
# command to install it locally.
vscode:
	@cd vscode-mar && \
		(test -d node_modules || npm install --silent) && \
		npm run compile --silent && \
		npx --yes @vscode/vsce package --out vscode-mar.vsix --no-dependencies > /dev/null
	@echo ""
	@echo "Built vscode-mar/vscode-mar.vsix"
	@echo ""
	@echo "Install with:"
	@echo "  code --install-extension vscode-mar/vscode-mar.vsix --force"

clean:
	@rm -f mar
	@rm -rf vscode-mar/out vscode-mar/*.vsix
	@find $(STUB_DIR) -type f ! -name 'README' -delete 2>/dev/null || true
	@echo "Cleaned"
