// bench は ISUReal の基本ベンチマーク（initialize → pretest → load）を実行し、
// 結果 JSON を標準出力へ出す。終了コードは pass=0、fail=1。
// 負荷走行では 3 リージョンのユーザーを同時に動かし、各ユーザーは自分の
// リージョンへルーターが振り分ける国を送る。
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
	"github.com/isucon/isucon2026/dataset"
)

const (
	defaultTarget = "http://app.127.0.0.1.nip.io:18090"
	defaultUsers  = 8
)

func main() {
	target := flag.String("target", defaultTarget, "base URL of the target application")
	host := flag.String("host", "", "HTTP Host header override (empty uses the target host)")
	users := flag.Int("users", defaultUsers, "number of load users per region")
	duration := flag.Duration("duration", 60*time.Second, "length of the load phase (shorter values are for development)")
	scale := flag.String("scale", "small", "scale of the initial data in the target DB: small or large (must match datagen -scale)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *users < 1 {
		logger.Error("users must be at least 1")
		os.Exit(2)
	}

	datasetConfig, ok := map[string]func() dataset.Config{"small": dataset.Small, "large": dataset.Large}[*scale]
	if !ok {
		logger.Error("unknown scale", "scale", *scale)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	runner, err := scenario.NewRunner(scenario.Config{
		Target:   *target,
		Host:     *host,
		Users:    *users,
		Duration: *duration,
		Dataset:  dataset.Generate(datasetConfig()),
		Logger:   logger,
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
