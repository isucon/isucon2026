// Command bench runs a basic ISUReal benchmark: initialize → pretest → load,
// then prints the result JSON to stdout. Exit code 0 = pass, 1 = fail.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/isucon/isucon2026/bench/internal/scenario"
)

const (
	defaultTarget = "http://app.127.0.0.1.nip.io:18090"
	defaultUsers  = 8
)

func main() {
	target := flag.String("target", defaultTarget, "base URL of the target application")
	host := flag.String("host", "", "HTTP Host header override (empty uses the target host)")
	region := flag.String("region", "asia", "user region: asia, europe, america")
	users := flag.Int("users", defaultUsers, "number of concurrent load users")
	cycles := flag.Int("cycles", 0, "run exactly this many posting cycles instead of the fixed 60s load (0 = 60s-bound)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *users < 1 {
		logger.Error("users must be at least 1")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	runner, err := scenario.NewRunner(scenario.Config{
		Target:    *target,
		Host:      *host,
		Region:    *region,
		Users:     *users,
		MaxCycles: *cycles,
		Logger:    logger,
	})
	if err != nil {
		logger.Error("benchmarker setup failed", "error", err.Error())
		os.Exit(2)
	}

	result := runner.Run(ctx)

	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		logger.Error("encode result failed", "error", err.Error())
		os.Exit(2)
	}
	fmt.Println(string(encoded))
	if !result.Pass {
		os.Exit(1)
	}
}
