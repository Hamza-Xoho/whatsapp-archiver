package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"whatsapp-archiver/internal/devseed"
)

func runDevSeed(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("devseed", flag.ContinueOnError)
	fs.SetOutput(errOut)
	data := fs.String("data", "./data", "data directory")
	chats := fs.Int("chats", 250, "number of synthetic chats")
	messages := fs.Int("messages", 300000, "number of synthetic messages")
	seed := fs.Int64("seed", 1, "deterministic random seed")
	live := fs.String("simulate-live", "", "append synthetic messages at a rate such as 5/s")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rate, err := parseLiveRate(*live)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := devseed.Seed(ctx, devseed.Options{
		DataDir: *data, Chats: *chats, Messages: *messages, Seed: *seed, SimulateLive: rate, Serve: rate > 0,
	})
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "seeded %d chats and %d messages in %s\n", report.Chats, report.Messages, report.Duration)
	return 0
}

func parseLiveRate(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	raw = strings.TrimSuffix(raw, "/s")
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil || rate <= 0 || rate > 10000 {
		return 0, fmt.Errorf("simulate-live must be a rate from 0 to 10000 messages/s, such as 5/s")
	}
	return rate, nil
}
