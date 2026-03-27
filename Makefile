GOCACHE ?= $(CURDIR)/.gocache
GO_MIN_VERSION := 1.26.1
ELM_REQUIRED_VERSION := 0.19.1
GO_VERSION := $(shell go version | awk '{print $$3}' 2>/dev/null | sed 's/^go//')
MAR_VERSION := $(shell tr -d '\r\n' < VERSION 2>/dev/null)
MAR_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || printf "unknown")
MAR_BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || printf "unknown")
MAR_LDFLAGS := -s -w -X 'mar/internal/cli.cliCommit=$(MAR_COMMIT)' -X 'mar/internal/cli.cliBuildTime=$(MAR_BUILD_TIME)'
MACOS_INSTALL_PREFIX ?= /usr/local/bin
MACOS_PKG_IDENTIFIER ?= tech.segunda.mar
MACOS_DEVELOPER_ID_APP ?=
MACOS_DEVELOPER_ID_INSTALLER ?=
MACOS_NOTARY_PROFILE ?=

.PHONY: all check check-go check-elm check-elm-live check-python3 check-node check-npm check-npx check-zip check-codesign check-pkgbuild check-notarytool check-stapler check-macos-release-config admin website website-serve website-dev vscode-plugin compiler-assets mar mar-release mar-release-zip mar-release-macos _mar-release-macos-sign _mar-release-macos-pkg _mar-release-macos-notarize _mar-release-macos-validate test clean distclean

define print_title
	@sh -c 'if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "\n%s\n" "$(1)"; else printf "\n\033[1;36m%s\033[0m\n" "$(1)"; fi'
endef

define print_info
	@sh -c 'printf "  %s\n" "$(1)"'
endef

define print_ok
	@sh -c 'if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "  %s\n" "$(1)"; else printf "  \033[1;32m%s\033[0m\n" "$(1)"; fi'
endef

define print_hint
	@sh -c 'if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "$(1)"; else printf "\033[1;33m%s\033[0m\n" "$(1)"; fi'
endef

define print_error
	@sh -c 'if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "\n%s\n\n" "$(1)"; else printf "\n\033[1;31m%s\033[0m\n\n" "$(1)"; fi'
endef

all:
	@$(MAKE) --no-print-directory CHAINED=1 mar
	$(call print_title,Mar compiler ready)
	$(call print_ok,./mar)
	@$(MAKE) --no-print-directory CHAINED=1 website
	@$(MAKE) --no-print-directory CHAINED=1 vscode-plugin
	@printf "\n"

check: check-go check-elm

check-go:
	@command -v go >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Go $(GO_MIN_VERSION)+ is required for this step. Install Go and try again."; else printf "\033[1;31m%s\033[0m\n" "Go $(GO_MIN_VERSION)+ is required for this step. Install Go and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}
	@GO_VERSION="$$(go version | awk '{print $$3}' | sed 's/^go//')"; \
	if [ -z "$$GO_VERSION" ]; then \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Could not determine Go version."; else printf "\033[1;31m%s\033[0m\n" "Could not determine Go version."; fi; \
		printf "\n"; \
		exit 1; \
	fi; \
	if ! printf '%s\n%s\n' "$(GO_MIN_VERSION)" "$$GO_VERSION" | sort -V -C; then \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Go $(GO_MIN_VERSION)+ is required. Found $$GO_VERSION."; else printf "\033[1;31m%s\033[0m\n" "Go $(GO_MIN_VERSION)+ is required. Found $$GO_VERSION."; fi; \
		printf "\n"; \
		exit 1; \
	fi

check-elm:
	@command -v elm >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Elm $(ELM_REQUIRED_VERSION) is required for this step. Install Elm and try again."; else printf "\033[1;31m%s\033[0m\n" "Elm $(ELM_REQUIRED_VERSION) is required for this step. Install Elm and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}
	@ELM_VERSION="$$(elm --version 2>/dev/null)"; \
	if [ "$$ELM_VERSION" != "$(ELM_REQUIRED_VERSION)" ]; then \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Elm $(ELM_REQUIRED_VERSION) is required. Found $$ELM_VERSION."; else printf "\033[1;31m%s\033[0m\n" "Elm $(ELM_REQUIRED_VERSION) is required. Found $$ELM_VERSION."; fi; \
		printf "\n"; \
		exit 1; \
	fi

check-elm-live:
	@command -v elm-live >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "elm-live is required for website hot reload. Install it and try again."; else printf "\033[1;31m%s\033[0m\n" "elm-live is required for website hot reload. Install it and try again."; fi; \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Hint:"; else printf "\033[1;33m%s\033[0m\n" "Hint:"; fi; \
		printf "  %s\n" "Run: npm install -g elm-live"; \
		printf "\n"; \
		exit 1; \
	}

check-python3:
	@command -v python3 >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "python3 is required to serve the website locally. Install Python 3 and try again."; else printf "\033[1;31m%s\033[0m\n" "python3 is required to serve the website locally. Install Python 3 and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-node:
	@command -v node >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Node.js is required for JS minification and VS Code extension packaging. Install Node.js and try again."; else printf "\033[1;31m%s\033[0m\n" "Node.js is required for JS minification and VS Code extension packaging. Install Node.js and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-npm: check-node
	@command -v npm >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "npm is required to package the VS Code extension. Install Node.js and try again."; else printf "\033[1;31m%s\033[0m\n" "npm is required to package the VS Code extension. Install Node.js and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-npx: check-node
	@command -v npx >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "npx is required for JS minification and VS Code extension packaging. Install Node.js and try again."; else printf "\033[1;31m%s\033[0m\n" "npx is required for JS minification and VS Code extension packaging. Install Node.js and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-zip:
	@command -v zip >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "zip is required to package release archives. Install zip and try again."; else printf "\033[1;31m%s\033[0m\n" "zip is required to package release archives. Install zip and try again."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-codesign:
	@command -v codesign >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "codesign is required for macOS signing. Run this on macOS with Xcode Command Line Tools installed."; else printf "\033[1;31m%s\033[0m\n" "codesign is required for macOS signing. Run this on macOS with Xcode Command Line Tools installed."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-pkgbuild:
	@command -v pkgbuild >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "pkgbuild is required to create signed macOS installer packages."; else printf "\033[1;31m%s\033[0m\n" "pkgbuild is required to create signed macOS installer packages."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-notarytool:
	@xcrun notarytool --help >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "xcrun notarytool is required for macOS notarization."; else printf "\033[1;31m%s\033[0m\n" "xcrun notarytool is required for macOS notarization."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-stapler:
	@xcrun --find stapler >/dev/null 2>&1 || { \
		printf "\n"; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "xcrun stapler is required to staple notarization tickets."; else printf "\033[1;31m%s\033[0m\n" "xcrun stapler is required to staple notarization tickets."; fi; \
		printf "\n"; \
		exit 1; \
	}

check-macos-release-config:
	@sh -c '\
		missing=""; \
		if [ -z "$(MACOS_DEVELOPER_ID_APP)" ]; then missing="$$missing MACOS_DEVELOPER_ID_APP"; fi; \
		if [ -z "$(MACOS_DEVELOPER_ID_INSTALLER)" ]; then missing="$$missing MACOS_DEVELOPER_ID_INSTALLER"; fi; \
		if [ -z "$(MACOS_NOTARY_PROFILE)" ]; then missing="$$missing MACOS_NOTARY_PROFILE"; fi; \
		if [ -n "$$missing" ]; then \
			printf "\n"; \
			if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "mar-release-macos needs explicit macOS signing/notarization parameters."; else printf "\033[1;31m%s\033[0m\n" "mar-release-macos needs explicit macOS signing/notarization parameters."; fi; \
			printf "\n"; \
			printf "  %s%s\n" "Missing required parameters:" "$$missing"; \
			printf "\n"; \
			if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "Hint:"; else printf "\033[1;33m%s\033[0m\n" "Hint:"; fi; \
			printf "  %s\n" "Run:"; \
			printf "    %s\n" "make mar-release-macos \\"; \
			printf "    %s\n" "  MACOS_DEVELOPER_ID_APP=\"Developer ID Application: Your Name (TEAMID)\" \\"; \
			printf "    %s\n" "  MACOS_DEVELOPER_ID_INSTALLER=\"Developer ID Installer: Your Name (TEAMID)\" \\"; \
			printf "    %s\n" "  MACOS_NOTARY_PROFILE=\"your-notary-profile\""; \
			printf "\n"; \
			exit 1; \
		fi'

admin: check-elm check-npx
	$(call print_title,Admin UI)
	$(call print_info,Building admin/dist/app.js with Elm $(ELM_REQUIRED_VERSION))
	$(call print_info,Minifying admin/dist/app.js with esbuild)
	@cd admin && sh -c '\
		elm_output=$$(elm make src/Main.elm --optimize --output=dist/app.unminified.js 2>&1) || { printf "%s\n" "$$elm_output"; exit 1; }; \
		before_bytes=$$(wc -c < dist/app.unminified.js | tr -d " "); \
		esbuild_output=$$(npx --yes esbuild dist/app.unminified.js --minify --outfile=dist/app.js 2>&1) || { printf "%s\n" "$$esbuild_output"; exit 1; }; \
		after_bytes=$$(wc -c < dist/app.js | tr -d " "); \
		before_kb=$$(awk "BEGIN { printf \"%.1f\", $$before_bytes / 1024 }"); \
		after_kb=$$(awk "BEGIN { printf \"%.1f\", $$after_bytes / 1024 }"); \
		reduction=$$(awk "BEGIN { if ($$before_bytes > 0) printf \"%.0f\", (( $$before_bytes - $$after_bytes ) / $$before_bytes) * 100; else printf \"0\" }"); \
		rm -f dist/app.unminified.js; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: admin/dist/app.js ($$after_kb KB, down from $$before_kb KB, -$$reduction%%)"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m \033[38;5;245m(%s KB, down from %s KB, -%s%%)\033[0m\n" "admin/dist/app.js" "$$after_kb" "$$before_kb" "$$reduction"; \
		fi'
	@if [ -z "$(CHAINED)" ] && [ -n "$(filter admin,$(MAKECMDGOALS))" ]; then printf "\n"; fi

website: check-elm check-npx
	$(call print_title,Website)
	$(call print_info,Building website/dist/app.js with Elm $(ELM_REQUIRED_VERSION))
	$(call print_info,Minifying website/dist/app.js with esbuild)
	@cd website && sh -c '\
		elm_output=$$(elm make src/Main.elm --optimize --output=dist/app.unminified.js 2>&1) || { printf "%s\n" "$$elm_output"; exit 1; }; \
		before_bytes=$$(wc -c < dist/app.unminified.js | tr -d " "); \
		esbuild_output=$$(npx --yes esbuild dist/app.unminified.js --minify --outfile=dist/app.js 2>&1) || { printf "%s\n" "$$esbuild_output"; exit 1; }; \
		after_bytes=$$(wc -c < dist/app.js | tr -d " "); \
		before_kb=$$(awk "BEGIN { printf \"%.1f\", $$before_bytes / 1024 }"); \
		after_kb=$$(awk "BEGIN { printf \"%.1f\", $$after_bytes / 1024 }"); \
		reduction=$$(awk "BEGIN { if ($$before_bytes > 0) printf \"%.0f\", (( $$before_bytes - $$after_bytes ) / $$before_bytes) * 100; else printf \"0\" }"); \
		rm -f dist/app.unminified.js; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: website/dist/app.js ($$after_kb KB, down from $$before_kb KB, -$$reduction%%)"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m \033[38;5;245m(%s KB, down from %s KB, -%s%%)\033[0m\n" "website/dist/app.js" "$$after_kb" "$$before_kb" "$$reduction"; \
		fi'
	@if [ -z "$(CHAINED)" ]; then printf "\n"; fi

website-serve: website check-python3
	$(call print_title,Website)
	$(call print_info,Serving website at http://localhost:8080)
	$(call print_info,Opening the browser when the server is ready)
	$(call print_info,Press Ctrl+C to stop)
	@cd website && sh -c '\
		python3 -m http.server 8080 & \
		pid=$$!; \
		trap "kill $$pid" INT TERM EXIT; \
		for _ in 1 2 3 4 5 6 7 8 9 10; do \
			python3 -c "import socket; s=socket.socket(); s.settimeout(0.2); ok=(s.connect_ex((\"127.0.0.1\", 8080)) == 0); s.close(); raise SystemExit(0 if ok else 1)" && break; \
			sleep 0.2; \
		done; \
		python3 -m webbrowser http://localhost:8080 >/dev/null 2>&1 || true; \
		wait $$pid'

website-dev: check-elm check-elm-live
	$(call print_title,Website)
	$(call print_info,Starting hot reload at http://localhost:8080)
	$(call print_info,Opening the browser when the dev server is ready)
	$(call print_info,Press Ctrl+C to stop)
	@cd website && elm-live src/Main.elm --dir=. --port=8080 --open -- --output=dist/app.js

vscode-plugin: check-npx check-npm
	$(call print_title,VS Code extension)
	@sh -c 'if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "  %s\n" "Installing vscode-mar dependencies with npm ci"; else printf "  Installing vscode-mar dependencies with \033[1;32mnpm ci\033[0m\n"; fi'
	$(call print_info,Packaging vscode-mar into a .vsix)
	@cd vscode-mar && sh -c '\
		publisher=$$(node -p "require(\"./package.json\").publisher"); \
		name=$$(node -p "require(\"./package.json\").name"); \
		version=$$(node -p "require(\"./package.json\").version"); \
		out="../dist/vscode/$$publisher.$$name-$$version.vsix"; \
		mkdir -p ../dist/vscode; \
		install_output=$$(npm ci --no-audit --no-fund 2>&1) || { printf "%s\n" "$$install_output"; exit 1; }; \
		output=$$(npx @vscode/vsce package --out "$$out" 2>&1) || { printf "%s\n" "$$output"; exit 1; }; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: $${out#../}"; \
			printf "  %s\n" "Install in VS Code with: code --install-extension $${out#../} --force"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m\n" "$${out#../}"; \
			printf "  Install in VS Code with: \033[1;32mcode --install-extension %s --force\033[0m\n" "$${out#../}"; \
		fi'
	@if [ -z "$(CHAINED)" ]; then printf "\n"; fi

compiler-assets: check-go admin
	$(call print_title,Compiler assets)
	$(call print_info,Refreshing embedded admin assets and runtime stubs)
	@GOCACHE="$(GOCACHE)" ./scripts/build-compiler-assets.sh
	@sh -c '\
		bytes=$$(wc -c < internal/cli/compiler_assets/admin/dist/app.js | tr -d " "); \
		size_kb=$$(awk "BEGIN { printf \"%.1f\", $$bytes / 1024 }"); \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Embedded admin bundle: internal/cli/compiler_assets/admin/dist/app.js ($$size_kb KB, copied from minified admin/dist/app.js)"; \
		else \
			printf "  Embedded admin bundle: \033[1;32m%s\033[0m \033[38;5;245m(%s KB, copied from minified admin/dist/app.js)\033[0m\n" "internal/cli/compiler_assets/admin/dist/app.js" "$$size_kb"; \
		fi'
	@if [ -z "$(CHAINED)" ] && [ -n "$(filter compiler-assets,$(MAKECMDGOALS))" ]; then printf "\n"; fi

mar: check-go compiler-assets
	$(call print_title,Mar compiler)
	$(call print_info,Building ./mar with Go $(GO_VERSION))
	@GOCACHE="$(GOCACHE)" go build -trimpath -ldflags="$(MAR_LDFLAGS)" -o mar ./cmd/mar
	@sh -c '\
		size=$$(du -sh mar | awk "{print \$$1}"); \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: ./mar ($$size)"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m \033[38;5;245m(%s)\033[0m\n" "./mar" "$$size"; \
		fi'
	@if [ -z "$(CHAINED)" ] && [ -n "$(filter mar,$(MAKECMDGOALS))" ]; then printf "\n"; fi

mar-release: check-go compiler-assets
	$(call print_title,Mar release)
	$(call print_info,Building release binaries for all supported platforms)
	@mkdir -p dist/releases/mar/darwin-arm64
	@mkdir -p dist/releases/mar/darwin-amd64
	@mkdir -p dist/releases/mar/linux-amd64
	@mkdir -p dist/releases/mar/linux-arm64
	@mkdir -p dist/releases/mar/windows-amd64
	$(call print_info,Building darwin-arm64)
	@env GOCACHE="$(GOCACHE)" CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
		go build -trimpath -ldflags="$(MAR_LDFLAGS)" -o dist/releases/mar/darwin-arm64/mar ./cmd/mar
	$(call print_info,Building darwin-amd64)
	@env GOCACHE="$(GOCACHE)" CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 \
		go build -trimpath -ldflags="$(MAR_LDFLAGS)" -o dist/releases/mar/darwin-amd64/mar ./cmd/mar
	$(call print_info,Building linux-amd64)
	@env GOCACHE="$(GOCACHE)" CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags="$(MAR_LDFLAGS)" -o dist/releases/mar/linux-amd64/mar ./cmd/mar
	$(call print_info,Building linux-arm64)
	@env GOCACHE="$(GOCACHE)" CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags="$(MAR_LDFLAGS)" -o dist/releases/mar/linux-arm64/mar ./cmd/mar
	$(call print_info,Building windows-amd64)
	@env GOCACHE="$(GOCACHE)" CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags="$(MAR_LDFLAGS)" -o dist/releases/mar/windows-amd64/mar.exe ./cmd/mar
	@sh -c '\
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: dist/releases/mar/darwin-arm64/mar"; \
			printf "  %s\n" "Output: dist/releases/mar/darwin-amd64/mar"; \
			printf "  %s\n" "Output: dist/releases/mar/linux-amd64/mar"; \
			printf "  %s\n" "Output: dist/releases/mar/linux-arm64/mar"; \
			printf "  %s\n" "Output: dist/releases/mar/windows-amd64/mar.exe"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/darwin-arm64/mar"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/darwin-amd64/mar"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/linux-amd64/mar"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/linux-arm64/mar"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/windows-amd64/mar.exe"; \
		fi'
	@if [ -z "$(CHAINED)" ] && [ -n "$(filter mar-release,$(MAKECMDGOALS))" ]; then printf "\n"; fi

mar-release-zip: check-zip mar-release
	$(call print_title,Mar release archives)
	$(call print_info,Packaging release binaries into .zip archives)
	@sh -c '\
		version="$(MAR_VERSION)"; \
		if [ -z "$$version" ]; then \
			printf "\n"; \
			if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "VERSION is missing or empty."; else printf "\033[1;31m%s\033[0m\n" "VERSION is missing or empty."; fi; \
			printf "\n"; \
			exit 1; \
		fi; \
		printf "  %s %s\n" "Release version:" "$$version"; \
		mkdir -p dist/releases/mar; \
		rm -f "dist/releases/mar/mar-$$version-darwin-arm64.zip"; \
		rm -f "dist/releases/mar/mar-$$version-darwin-amd64.zip"; \
		rm -f "dist/releases/mar/mar-$$version-linux-amd64.zip"; \
		rm -f "dist/releases/mar/mar-$$version-linux-arm64.zip"; \
		rm -f "dist/releases/mar/mar-$$version-windows-amd64.zip"; \
		printf "  %s\n" "Packaging darwin-arm64"; \
		cd dist/releases/mar/darwin-arm64 && zip -q "../mar-$$version-darwin-arm64.zip" mar; \
		printf "  %s\n" "Packaging darwin-amd64"; \
		cd ../darwin-amd64 && zip -q "../mar-$$version-darwin-amd64.zip" mar; \
		printf "  %s\n" "Packaging linux-amd64"; \
		cd ../linux-amd64 && zip -q "../mar-$$version-linux-amd64.zip" mar; \
		printf "  %s\n" "Packaging linux-arm64"; \
		cd ../linux-arm64 && zip -q "../mar-$$version-linux-arm64.zip" mar; \
		printf "  %s\n" "Packaging windows-amd64"; \
		cd ../windows-amd64 && zip -q "../mar-$$version-windows-amd64.zip" mar.exe; \
		cd ..; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-darwin-arm64.zip"; \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-darwin-amd64.zip"; \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-linux-amd64.zip"; \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-linux-arm64.zip"; \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-windows-amd64.zip"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-darwin-arm64.zip"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-darwin-amd64.zip"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-linux-amd64.zip"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-linux-arm64.zip"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-windows-amd64.zip"; \
		fi'
	@printf "\n"

_mar-release-macos-sign: check-codesign mar-release
	$(call print_title,Mar macOS signing)
	$(call print_info,Signing darwin binaries with Developer ID Application)
	@sh -c '\
		for arch in darwin-arm64 darwin-amd64; do \
			binary="dist/releases/mar/$$arch/mar"; \
			printf "  %s %s\n" "Signing" "$$binary"; \
			codesign --force --timestamp --options runtime --sign "$(MACOS_DEVELOPER_ID_APP)" "$$binary"; \
		done; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Signed: dist/releases/mar/darwin-arm64/mar"; \
			printf "  %s\n" "Signed: dist/releases/mar/darwin-amd64/mar"; \
		else \
			printf "  Signed: \033[1;32m%s\033[0m\n" "dist/releases/mar/darwin-arm64/mar"; \
			printf "  Signed: \033[1;32m%s\033[0m\n" "dist/releases/mar/darwin-amd64/mar"; \
		fi'

_mar-release-macos-pkg: check-pkgbuild _mar-release-macos-sign
	$(call print_title,Mar macOS packages)
	$(call print_info,Building signed .pkg installers for darwin binaries)
	@sh -c '\
		version="$(MAR_VERSION)"; \
		if [ -z "$$version" ]; then \
			printf "\n"; \
			if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "VERSION is missing or empty."; else printf "\033[1;31m%s\033[0m\n" "VERSION is missing or empty."; fi; \
			printf "\n"; \
			exit 1; \
		fi; \
		for arch in darwin-arm64 darwin-amd64; do \
			root="dist/releases/mar/$$arch/pkg-root"; \
			out="dist/releases/mar/mar-$$version-$$arch.pkg"; \
			rm -rf "$$root"; \
			rm -f "$$out"; \
			mkdir -p "$$root$(MACOS_INSTALL_PREFIX)"; \
			cp "dist/releases/mar/$$arch/mar" "$$root$(MACOS_INSTALL_PREFIX)/mar"; \
			printf "  %s %s\n" "Packaging" "$$out"; \
			pkgbuild \
				--root "$$root" \
				--identifier "$(MACOS_PKG_IDENTIFIER).$$arch" \
				--version "$$version" \
				--install-location / \
				--sign "$(MACOS_DEVELOPER_ID_INSTALLER)" \
				"$$out"; \
		done; \
		if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-darwin-arm64.pkg"; \
			printf "  %s\n" "Output: dist/releases/mar/mar-$$version-darwin-amd64.pkg"; \
		else \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-darwin-arm64.pkg"; \
			printf "  Output: \033[1;32m%s\033[0m\n" "dist/releases/mar/mar-$$version-darwin-amd64.pkg"; \
		fi'

_mar-release-macos-notarize: check-notarytool check-stapler _mar-release-macos-pkg
	$(call print_title,Mar macOS notarization)
	$(call print_info,Submitting .pkg installers with notarytool and stapling tickets)
	@sh -c '\
		version="$(MAR_VERSION)"; \
		if [ -z "$$version" ]; then \
			printf "\n"; \
			if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "VERSION is missing or empty."; else printf "\033[1;31m%s\033[0m\n" "VERSION is missing or empty."; fi; \
			printf "\n"; \
			exit 1; \
		fi; \
		for arch in darwin-arm64 darwin-amd64; do \
			pkg="dist/releases/mar/mar-$$version-$$arch.pkg"; \
			printf "  %s %s\n" "Notarizing" "$$pkg"; \
			xcrun notarytool submit "$$pkg" --keychain-profile "$(MACOS_NOTARY_PROFILE)" --wait; \
			printf "  %s %s\n" "Stapling" "$$pkg"; \
			xcrun stapler staple "$$pkg"; \
		done'

_mar-release-macos-validate: check-codesign check-stapler
	$(call print_title,Mar macOS validation)
	$(call print_info,Validating signatures and installer assessment)
	@sh -c '\
		version="$(MAR_VERSION)"; \
		if [ -z "$$version" ]; then \
			printf "\n"; \
			if [ -n "$$NO_COLOR" ] || ! [ -t 1 ]; then printf "%s\n" "VERSION is missing or empty."; else printf "\033[1;31m%s\033[0m\n" "VERSION is missing or empty."; fi; \
			printf "\n"; \
			exit 1; \
		fi; \
		for arch in darwin-arm64 darwin-amd64; do \
			binary="dist/releases/mar/$$arch/mar"; \
			pkg="dist/releases/mar/mar-$$version-$$arch.pkg"; \
			printf "  %s %s\n" "codesign --verify" "$$binary"; \
			codesign --verify --verbose=2 "$$binary"; \
			if [ -f "$$pkg" ]; then \
				printf "  %s %s\n" "spctl --assess" "$$pkg"; \
				spctl -a -vv -t install "$$pkg"; \
			fi; \
		done'

mar-release-macos: check-macos-release-config _mar-release-macos-notarize _mar-release-macos-validate
	$(call print_title,Mar macOS release ready)
	$(call print_ok,Notarized macOS installers are ready in dist/releases/mar)
	@printf "\n"

test: check-go
	$(call print_title,Tests)
	$(call print_info,Running app bundle, CLI, and runtime-stub tests)
	@GOCACHE="$(GOCACHE)" go test ./internal/appbundle ./internal/cli ./cmd/mar-app

clean:
	$(call print_title,Clean)
	$(call print_info,Removing Go cache)
	@rm -rf "$(GOCACHE)"
	@printf "\n"

distclean: clean
	$(call print_info,Removing dist/)
	@rm -rf "$(CURDIR)/dist"
	@printf "\n"
