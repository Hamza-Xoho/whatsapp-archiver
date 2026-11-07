SHELL := /bin/sh
ROOT := $(CURDIR)
export GOPATH := $(ROOT)/.cache/gopath
export GOMODCACHE := $(ROOT)/.cache/gomod
export GOCACHE := $(ROOT)/.cache/gocache
export GOBIN := $(ROOT)/.tools/bin
export GOTOOLCHAIN := local
# Keep compiler scratch files off the host's small /tmp tmpfs. The toolchain
# target prepares this ignored directory before any Go command runs.
export TMPDIR := $(ROOT)/.cache/tmp
export GOTMPDIR := $(ROOT)/.cache/tmp
$(shell mkdir -p "$(ROOT)/.cache/tmp")
# Make 3.81 ignores an exported PATH when it looks up simple recipe commands,
# so every direct invocation below uses these absolute paths instead.
export PATH := $(ROOT)/.tools/bin:$(ROOT)/.tools/go/bin:$(PATH)
GO := $(ROOT)/.tools/go/bin/go
LINT := $(ROOT)/.tools/bin/golangci-lint

# Pinned tool versions and SHA-256 checksums (go.dev/dl, golangci-lint release checksums.txt).
GO_VERSION := 1.26.9
GO_SHA256_darwin_arm64 := f9bb7c0a02506c5d9bf0d1eb1f7ee6c7684f844ae49558308a0427b830e022cc
GO_SHA256_darwin_amd64 := 00c29e3d4c8562f547410daa18d2959b641bd691ab00c4a525ea817a0253d4bd
GO_SHA256_linux_amd64 := 42d158b4d8f7b61ac0a830567c940a86098fb7aac52e467a5ebec03ef5cc2f8d
GO_SHA256_linux_arm64 := 4a97373d49fcacdcf3694fea368a500b00ee3e963974f3e7514132717632f052
LINT_VERSION := 2.14.0
LINT_SHA256_darwin_arm64 := 5ef5f36a7147e91dc58ef9ef4d11bb7bad5ead0c76eb6c01327a73c641d1dcc3
LINT_SHA256_darwin_amd64 := a5667c1c3536be1740133213e1e822bfb8f0d98ea12903174d6d5f635e4ed68d
LINT_SHA256_linux_amd64 := ab90aeb7b066f92a33415b638a50fe5344bbb75a0d32ad30cc248d88f81032ab
LINT_SHA256_linux_arm64 := ee7ec5f3453d15ddf106fae5a4d6c71737712348a979d1fe9cd52ec7ea299bae

HOST_OS := $(shell uname -s | tr 'A-Z' 'a-z')
HOST_ARCH := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
HOST := $(HOST_OS)_$(HOST_ARCH)
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

BUILD_TARGETS := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64

# Stamped into `archiver version` (cmd/archiver: var version). Falls back to "dev" outside a git checkout.
VERSION ?= $(shell git describe --always --dirty --tags 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: check check-fast test test-quiet lint build build-all fmt tools

check: test lint build-all

# Fast gate for the Stop hook: race tests on packages with Go changes vs HEAD, then lint. No build-all.
check-fast: $(GO)
	@pkgs=$$( { git diff --name-only HEAD; git ls-files --others --exclude-standard; } 2>/dev/null \
	  | grep '\.go$$' | while read -r f; do d=$$(dirname "$$f"); [ -d "$$d" ] && echo "$$d"; done | sort -u \
	  | while read -r d; do if [ "$$d" = . ]; then echo .; else echo "./$$d/..."; fi; done); \
	if [ -z "$$pkgs" ]; then echo "check-fast: no Go changes"; \
	else echo "check-fast: $$pkgs"; $(GO) test -race -short $$pkgs || exit 1; fi
	@$(MAKE) --no-print-directory lint

# Full race tests, but only failing output and the last lines show; exit status is go test's.
test-quiet: $(GO)
	@out=$$(mktemp); $(GO) test -race ./... >$$out 2>&1; rc=$$?; if [ $$rc -eq 0 ]; then $(GO) test -count=1 -run AtScale ./internal/store >>$$out 2>&1; rc=$$?; fi; tail -n 40 $$out; rm -f $$out; exit $$rc

# Race tests, then the 300k-message scale test without -race (the race build seeds 50k: status_scale_race_test.go).
test: $(GO)
	$(GO) test -race ./...
	$(GO) test -count=1 -run AtScale ./internal/store

lint: tools $(GO)
	$(LINT) run

build: $(GO)
	mkdir -p .cache/build
	$(GO) build -ldflags "$(LDFLAGS)" -o .cache/build/archiver ./cmd/archiver

build-all: $(GO)
	mkdir -p .cache/build
	for t in $(BUILD_TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  echo "build $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o .cache/build/archiver-$$os-$$arch$$ext ./cmd/archiver || exit 1; \
	done

fmt: $(GO)
	$(ROOT)/.tools/go/bin/gofmt -w cmd internal

# Download the pinned Go for the host into .tools/go after verifying its checksum.
$(GO):
	@test -n "$(GO_SHA256_$(HOST))" || { echo "no pinned Go for $(HOST)"; exit 1; }
	mkdir -p .tools .cache/tools
	curl -fsSL -o .cache/tools/go.tar.gz https://go.dev/dl/go$(GO_VERSION).$(HOST_OS)-$(HOST_ARCH).tar.gz
	echo "$(GO_SHA256_$(HOST))  .cache/tools/go.tar.gz" | $(SHA256) -c -
	rm -rf .tools/go
	tar -xzf .cache/tools/go.tar.gz -C .tools

# (Re)install the pinned golangci-lint unless the existing binary runs and has the right version.
tools:
	@$(LINT) --version 2>/dev/null | grep -q "version $(LINT_VERSION) " || $(MAKE) --no-print-directory install-lint

.PHONY: install-lint
install-lint:
	@test -n "$(LINT_SHA256_$(HOST))" || { echo "no pinned golangci-lint for $(HOST)"; exit 1; }
	mkdir -p .tools/bin .cache/tools
	curl -fsSL -o .cache/tools/lint.tar.gz https://github.com/golangci/golangci-lint/releases/download/v$(LINT_VERSION)/golangci-lint-$(LINT_VERSION)-$(HOST_OS)-$(HOST_ARCH).tar.gz
	echo "$(LINT_SHA256_$(HOST))  .cache/tools/lint.tar.gz" | $(SHA256) -c -
	tar -xzf .cache/tools/lint.tar.gz -C .cache/tools --strip-components=1 golangci-lint-$(LINT_VERSION)-$(HOST_OS)-$(HOST_ARCH)/golangci-lint
	mv .cache/tools/golangci-lint $(LINT)

# Standalone archive MCP companion: Linux amd64 only. The existing archiver's
# cross-platform build-all matrix is intentionally independent.
MCP_VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)$(shell test -z "$$(git status --porcelain 2>/dev/null)" || printf '%s' '-dirty')
.PHONY: mcp-test mcp-build mcp-release mcp-deps mcp-fmt mcp-tidy
check: mcp-build
mcp-test: $(GO)
	$(GO) test -race -short ./internal/archiveread ./internal/mcpbridge ./cmd/archiver-mcp
mcp-tidy: $(GO)
	$(GO) mod tidy
mcp-deps: $(GO)
	$(GO) mod download
mcp-fmt: $(GO)
	$(ROOT)/.tools/go/bin/gofmt -w internal/mcpbridge/*.go cmd/archiver-mcp/*.go
mcp-build: $(GO)
	mkdir -p .cache/build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-X main.version=$(MCP_VERSION)" -o .cache/build/archiver-mcp ./cmd/archiver-mcp
mcp-release: mcp-build
	rm -rf .cache/release/archiver-mcp-linux-amd64
	mkdir -p .cache/release/archiver-mcp-linux-amd64/scripts .cache/release/archiver-mcp-linux-amd64/docs .cache/release/archiver-mcp-linux-amd64/deploy/mcp
	cp .cache/build/archiver-mcp .cache/release/archiver-mcp-linux-amd64/archiver-mcp
	cp deploy/mcp/manage.py deploy/mcp/mcp_host.py deploy/mcp/config.yaml deploy/mcp/archiver-mcp-* deploy/mcp/archiver-mcp.tmpfiles .cache/release/archiver-mcp-linux-amd64/deploy/mcp/
	cp scripts/install-mcp.sh scripts/uninstall-mcp.sh .cache/release/archiver-mcp-linux-amd64/scripts/
	cp docs/mcp-linux.md docs/hermes-linux.md .cache/release/archiver-mcp-linux-amd64/docs/
	cp README.md .cache/release/archiver-mcp-linux-amd64/README.md
	printf '%s\n' '$(MCP_VERSION)' > .cache/release/archiver-mcp-linux-amd64/VERSION
	cd .cache/release/archiver-mcp-linux-amd64 && find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 $(SHA256) > SHA256SUMS
	cd .cache/release && tar -czf archiver-mcp-linux-amd64-$(MCP_VERSION).tar.gz archiver-mcp-linux-amd64
	cd .cache/release && $(SHA256) archiver-mcp-linux-amd64-$(MCP_VERSION).tar.gz > archiver-mcp-linux-amd64-$(MCP_VERSION).tar.gz.sha256

.PHONY: mcp-install-test
check: mcp-install-test
mcp-install-test:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests/mcp -p 'test_*.py'
	python3 -m py_compile deploy/mcp/manage.py deploy/mcp/mcp_host.py tests/mcp/test_install.py
	sh -n scripts/install-mcp.sh scripts/uninstall-mcp.sh deploy/mcp/archiver-mcp-launch deploy/mcp/archiver-mcp-serve
