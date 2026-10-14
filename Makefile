SHELL := /bin/sh
ROOT := $(CURDIR)
export GOPATH := $(ROOT)/.cache/gopath
export GOMODCACHE := $(ROOT)/.cache/gomod
export GOCACHE := $(ROOT)/.cache/gocache
export GOBIN := $(ROOT)/.tools/bin
export GOTOOLCHAIN := local
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

.PHONY: check test lint build build-all fmt tools

check: test lint build-all

test: $(GO)
	$(GO) test -race ./...

lint: tools $(GO)
	$(LINT) run

build: $(GO)
	mkdir -p .cache/build
	$(GO) build -o .cache/build/archiver ./cmd/archiver

build-all: $(GO)
	mkdir -p .cache/build
	for t in $(BUILD_TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  echo "build $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -o .cache/build/archiver-$$os-$$arch$$ext ./cmd/archiver || exit 1; \
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
