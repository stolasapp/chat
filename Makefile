MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules
MAKEFLAGS += --no-print-directory
.SUFFIXES:
SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DELETE_ON_ERROR:
.DEFAULT_GOAL := all

BIN ?= bin
GO ?= go

TAILWIND_VERSION ?= 4.1.8
TAILWIND ?= $(BIN)/tailwindcss-$(TAILWIND_VERSION)

GO_TEST_FLAGS ?= -vet=off -race
ARGS ?=

.PHONY: all
all: generate build

.PHONY: generate
generate: css
	$(GO) tool templ generate

.PHONY: css
css: $(TAILWIND) sources
	$(TAILWIND) -i assets/css/input.css -o internal/static/output.css --minify

.PHONY: sources
sources:
	@TEMPLUI_PATH=$$($(GO) list -m -f '{{.Dir}}' github.com/templui/templui 2>/dev/null || echo "") && \
	if [ -n "$$TEMPLUI_PATH" ]; then \
	  printf '@source "./**/*.templ";\n@source "%s/components/**/*.templ";\n' "$$TEMPLUI_PATH"; \
	else \
	  printf '@source "./**/*.templ";\n'; \
	fi > assets/css/sources.generated.css

.PHONY: build
build: generate
	$(GO) build -o $(BIN)/chat ./cmd/chat

.PHONY: dev
dev:
	$(GO) tool air

.PHONY: check
check: test lint

.PHONY: test
test:
	$(GO) test $(GO_TEST_FLAGS) $(ARGS) ./...

.PHONY: lint
lint:
	$(GO) tool golangci-lint run $(ARGS)
	$(GO) tool checklocks ./...

.PHONY: format
format:
	$(GO) tool golangci-lint fmt

.PHONY: clean
clean:
	rm -rf $(BIN) assets/css/sources.generated.css

$(TAILWIND): | $(BIN)
	@OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	ARCH=$$(uname -m); \
	case "$$OS" in \
	  darwin) OS=macos ;; \
	esac; \
	case "$$ARCH" in \
	  x86_64) ARCH=x64 ;; \
	  aarch64) ARCH=arm64 ;; \
	esac; \
	curl -sLo $@ "https://github.com/tailwindlabs/tailwindcss/releases/download/v$(TAILWIND_VERSION)/tailwindcss-$$OS-$$ARCH" && \
	chmod +x $@

$(BIN):
	mkdir -p $@
