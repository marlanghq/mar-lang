GOCACHE ?= $(CURDIR)/.gocache
GO_MIN_VERSION := 1.26
ELM_REQUIRED_VERSION := 0.19.1
GO_VERSION := $(shell go version | awk '{print $$3}' 2>/dev/null | sed 's/^go//')

.PHONY: all check check-go check-elm check-elm-live check-python3 admin website website-serve website-dev compiler-assets mar todo dev-todo test clean distclean

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
	@$(MAKE) --no-print-directory mar
	$(call print_title,Mar compiler ready)
	$(call print_ok,./mar)
	@$(MAKE) --no-print-directory website
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

admin: check-elm
	$(call print_title,Admin UI)
	$(call print_info,Building admin/dist/app.js with Elm $(ELM_REQUIRED_VERSION))
	@cd admin && elm make src/Main.elm --output=dist/app.js
	$(call print_ok,Output: admin/dist/app.js)

website: check-elm
	$(call print_title,Website)
	$(call print_info,Building website/dist/app.js with Elm $(ELM_REQUIRED_VERSION))
	@cd website && elm make src/Main.elm --output=dist/app.js
	$(call print_ok,Output: website/dist/app.js)

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

compiler-assets: check-go admin
	$(call print_title,Compiler assets)
	$(call print_info,Refreshing embedded admin assets and runtime stubs)
	@GOCACHE="$(GOCACHE)" ./scripts/build-compiler-assets.sh
	$(call print_ok,Embedded admin assets: internal/cli/compiler_assets/admin)
	$(call print_ok,Runtime stubs: internal/cli/runtime_stubs)

mar: check-go compiler-assets
	$(call print_title,Mar compiler)
	$(call print_info,Building ./mar with Go $(GO_VERSION))
	@GOCACHE="$(GOCACHE)" go build -o mar ./cmd/mar
	$(call print_ok,Output: ./mar)

todo: mar
	$(call print_title,Example app)
	$(call print_info,Compiling examples/todo.mar into dist/)
	@./mar compile examples/todo.mar

dev-todo: mar
	$(call print_title,Example app)
	$(call print_info,Starting dev mode for examples/todo.mar)
	@./mar dev examples/todo.mar

test: check-go
	$(call print_title,Tests)
	$(call print_info,Running app bundle, CLI, and runtime-stub tests)
	@GOCACHE="$(GOCACHE)" go test ./internal/appbundle ./internal/cli ./cmd/mar-app

clean:
	$(call print_title,Clean)
	$(call print_info,Removing Go cache)
	@rm -rf "$(GOCACHE)"

distclean: clean
	$(call print_info,Removing dist/)
	@rm -rf "$(CURDIR)/dist"
