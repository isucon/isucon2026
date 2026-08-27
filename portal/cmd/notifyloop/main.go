// cmd/notifyloop は滞留監視ループ（notifyloop タスク相当）。
// 60 秒間隔で /admin/long_running_check を叩く（FR-J: LongRunningCheck）。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	target := os.Getenv("PORTAL_NOTIFYLOOP_TARGET")
	if target == "" {
		target = "http://127.0.0.1:8081/admin/long_running_check"
	}
	interval := 60 * time.Second

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("notifyloop started", "target", target, "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	client := &http.Client{Timeout: 30 * time.Second}
	for {
		select {
		case <-ctx.Done():
			slog.Info("notifyloop shutting down")
			return
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				slog.Error("notifyloop request build failed", "error", err)
				continue
			}
			res, err := client.Do(req)
			if err != nil {
				slog.Error("notifyloop request failed", "error", err)
				continue
			}
			res.Body.Close()
			if res.StatusCode >= 400 {
				slog.Error("notifyloop request rejected", "status", res.StatusCode)
			}
		}
	}
}
