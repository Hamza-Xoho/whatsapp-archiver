//go:build linux

// archiver-mcp is a separate, read-only archive companion. It cannot capture,
// pair, send, schedule processing, or modify the archive.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"whatsapp-archiver/internal/archiveread"
	"whatsapp-archiver/internal/mcpbridge"
)

var version = "dev-dirty"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, input io.ReadCloser, output, diagnostics io.Writer) int {
	fail := func(code string) int { _, _ = fmt.Fprintln(diagnostics, code); return 1 }
	if len(args) == 0 {
		return fail("INVALID_ARGUMENT")
	}
	if args[0] == "version" {
		if len(args) != 1 {
			return fail("INVALID_ARGUMENT")
		}
		_, err := fmt.Fprintf(output, "archiver-mcp %s schemas=legacy,v1\n", version)
		if err != nil {
			return fail("INTERNAL_ERROR")
		}
		return 0
	}
	command := args[0]
	if command != "serve" && command != "inspect" && command != "catalog" {
		return fail("INVALID_ARGUMENT")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "operator-owned configuration path")
	if err := flags.Parse(args[1:]); err != nil || *configPath == "" || flags.NArg() != 0 {
		return fail("INVALID_ARGUMENT")
	}
	config, err := archiveread.LoadConfig(*configPath)
	if err != nil {
		return fail(mcpbridge.SafeCode(err))
	}
	if command == "serve" {
		lock, err := acquireLock(config.RuntimeDir)
		if err != nil {
			if errors.Is(err, errLocked) {
				return fail("BUSY")
			}
			return fail("ARCHIVE_UNAVAILABLE")
		}
		defer func() { _ = lock.Close() }()
		server, err := mcpbridge.New(*configPath, version)
		if err != nil {
			return fail(mcpbridge.SafeCode(err))
		}
		err = serveStdio(ctx, server, input, output)
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			return 0
		}
		return fail("INVALID_ARGUMENT")
	}
	engine, err := archiveread.New(*configPath)
	if err != nil {
		return fail(mcpbridge.SafeCode(err))
	}
	var result any
	if command == "inspect" {
		result, err = engine.Inspect(ctx)
	} else {
		result, err = engine.Catalog(ctx)
	}
	if err != nil {
		return fail(mcpbridge.SafeCode(err))
	}
	if err = json.NewEncoder(output).Encode(result); err != nil {
		return fail("INTERNAL_ERROR")
	}
	return 0
}
