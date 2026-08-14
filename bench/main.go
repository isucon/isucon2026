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
	"time"

	"github.com/isucon/isucon2026/bench/internal/scenario"
)

const (
	defaultTarget   = "http://localhost:8080"
	defaultUsers    = 8
	defaultDuration = 30 * time.Second
)

func main() {
	target := flag.String("target", defaultTarget, "base URL of the target application")
	users := flag.Int("users", defaultUsers, "number of concurrent load users")
	duration := flag.Duration("duration", defaultDuration, "load phase duration")
	cycles := flag.Int("cycles", 0, "run exactly this many posting cycles instead of -duration (0 = duration-bound)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *users < 1 {
		logger.Error("users must be at least 1")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	runner, err := scenario.NewRunner(scenario.Config{
		Target:       *target,
		Users:        *users,
		LoadDuration: *duration,
		MaxCycles:    *cycles,
		Logger:       logger,
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
