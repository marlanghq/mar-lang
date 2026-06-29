# Mar build / test commands.
#
# Usage:
#   make                 # build the binary (./mar) — includes embedded stubs
#   make stubs           # cross-compile mar-runtime for every supported target
#   make test            # run the Go test suite
#   make check-examples  # type-check every example under examples/
#   make vscode          # build the VSCode extension (.vsix)
#   make website         # compile the marketing site (Elm) to website/dist/
#   make website-serve   # build the site then serve it on :8080 (Python)
#   make website-dev     # elm-live hot reload at :8080
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

# Elm version pinned to the latest stable. The website's elm.json is
# locked to this version too; bumping requires updating both.
ELM_REQUIRED_VERSION := 0.19.1

.PHONY: all build stubs ios-template test check-examples vscode website website-serve website-dev clean

all: build

build: stubs ios-template
	@echo "Building mar $(VERSION) ($(COMMIT))"
	@go build -trimpath -ldflags "$(LDFLAGS)" -o mar ./cmd/mar
	@echo "Built ./mar"

# Regenerate the embedded iOS Xcode project from the template. xcodegen
# is required only for contributors who run `make build` / `make test`
# (where the template needs to stay in sync with Sources/ + project.yml).
# End users who `go install` mar receive the pre-generated .xcodeproj
# from the source tarball — they never touch xcodegen.
#
# xcodegen runs in ~1s and produces deterministic output, so `git status`
# stays clean unless something in the template actually changed.
ios-template:
	@command -v xcodegen >/dev/null 2>&1 || { \
	  echo "error: xcodegen required to regenerate the iOS template"; \
	  echo "       brew install xcodegen"; \
	  exit 1; \
	}
	@cd internal/iosbundle/template && xcodegen generate > /dev/null

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

test: ios-template
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

# Compile the marketing site (Elm) → website/dist/app.js, then run
# esbuild to minify. Two-step (Elm produces an unminified bundle,
# esbuild squeezes it down ~70%) so the deployed asset is small
# without sacrificing the readable Elm output during dev.
#
# Requires `elm` 0.19.1 + `node`/`npx` (esbuild is invoked via
# `npx --yes` so no global install needed). The deps are checked
# inline — a missing tool yields a single-line error pointing at
# the install path.
website:
	@command -v elm >/dev/null 2>&1 || { \
		echo "error: elm $(ELM_REQUIRED_VERSION) required (install: https://guide.elm-lang.org/install/elm.html)"; \
		exit 1; \
	}
	@command -v npx >/dev/null 2>&1 || { \
		echo "error: npx required (install Node.js)"; \
		exit 1; \
	}
	@elm_actual="$$(elm --version 2>/dev/null)"; \
	if [ "$$elm_actual" != "$(ELM_REQUIRED_VERSION)" ]; then \
		echo "error: elm $(ELM_REQUIRED_VERSION) required, found $$elm_actual"; \
		exit 1; \
	fi
	@echo "Building website (Elm $(ELM_REQUIRED_VERSION) + esbuild)"
	@cd website && mkdir -p dist && \
		elm make src/Main.elm --optimize --output=dist/app.unminified.js > /dev/null && \
		npx --yes esbuild dist/app.unminified.js --minify --outfile=dist/app.js > /dev/null && \
		rm -f dist/app.unminified.js
	@echo "Built website/dist/app.js"

# Serve the built site at http://localhost:8080 via Python's
# built-in HTTP server. Cheap, no deps beyond Python 3 (ships with
# macOS + every Linux distro). For hot reload during development,
# use `make website-dev` instead.
website-serve: website
	@command -v python3 >/dev/null 2>&1 || { \
		echo "error: python3 required to serve the site"; \
		exit 1; \
	}
	@echo "Serving website at http://localhost:8080 (Ctrl+C to stop)"
	@cd website && python3 -m http.server 8080

# Hot reload via elm-live. Rebuilds the Elm bundle + refreshes the
# browser on every save. Useful for iterating on the marketing copy
# or styling without the manual `make website` loop.
#
# elm-live is an npm package: `npm install -g elm-live`.
website-dev:
	@command -v elm >/dev/null 2>&1 || { \
		echo "error: elm $(ELM_REQUIRED_VERSION) required"; \
		exit 1; \
	}
	@command -v elm-live >/dev/null 2>&1 || { \
		echo "error: elm-live required (install: npm install -g elm-live)"; \
		exit 1; \
	}
	@echo "Starting elm-live at http://localhost:8080 (Ctrl+C to stop)"
	@cd website && elm-live src/Main.elm --dir=. --port=8080 --open -- --output=dist/app.js

clean:
	@rm -f mar
	@rm -rf vscode-mar/out vscode-mar/*.vsix
	@rm -rf website/dist
	@rm -rf website/elm-stuff
	@find $(STUB_DIR) -type f ! -name 'README' -delete 2>/dev/null || true
	@echo "Cleaned"
