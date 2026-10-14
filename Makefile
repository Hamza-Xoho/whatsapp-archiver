SHELL := /bin/sh
ROOT := $(CURDIR)
export GOPATH := $(ROOT)/.cache/gopath
export GOMODCACHE := $(ROOT)/.cache/gomod
export GOCACHE := $(ROOT)/.cache/gocache
export GOBIN := $(ROOT)/.tools/bin
export PATH := $(ROOT)/.tools/bin:$(ROOT)/.tools/go/bin:$(PATH)
export GOTOOLCHAIN := auto

.PHONY: check test lint build build-all fmt tools

check: test lint build-all

test:
	go test -race ./...

lint: tools
	golangci-lint run

build:
	mkdir -p .cache/build
	go build -o .cache/build/archiver ./cmd/archiver

build-all:
	mkdir -p .cache/build
	GOOS=linux GOARCH=amd64 go build -o .cache/build/archiver-linux ./cmd/archiver 2>/dev/null || GOOS=linux GOARCH=amd64 go build ./...
	GOOS=darwin GOARCH=amd64 go build -o .cache/build/archiver-darwin ./cmd/archiver 2>/dev/null || GOOS=darwin GOARCH=amd64 go build ./...
	GOOS=windows GOARCH=amd64 go build -o .cache/build/archiver-windows.exe ./cmd/archiver 2>/dev/null || GOOS=windows GOARCH=amd64 go build ./...

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f 2>/dev/null)

tools: .tools/bin/golangci-lint

.tools/bin/golangci-lint:
	mkdir -p .tools/bin .cache/tools
	curl -fsSL https://golangci-lint.run/install.sh | sh -s -- -b .tools/bin v2.14.0
