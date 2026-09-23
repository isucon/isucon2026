// cmd/portal はポータルの HTTP サーバ（app タスク相当）。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/isucon/isucon2026/portal/internal/app"
	"github.com/isucon/isucon2026/portal/internal/db"
	"github.com/isucon/isucon2026/portal/internal/handler"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	a, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer a.DB.Close()

	if a.Cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: a.Cfg.SentryDSN, Environment: a.Cfg.SentryEnvironment}); err != nil {
			slog.Warn("sentry init failed", "error", err)
		}
	}

	if os.Getenv("PORTAL_AUTO_MIGRATE") == "1" {
		if err := db.Migrate(ctx, a.DB); err != nil {
			return err
		}
		slog.Info("schema applied")
	}

	// Prometheus /metrics は内部ポートで公開する（NF-6）
	if a.Cfg.MetricsListen != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			if err := http.ListenAndServe(a.Cfg.MetricsListen, mux); err != nil {
				slog.Error("metrics listener failed", "error", err)
			}
		}()
	}

	srv := &http.Server{
		Addr:    a.Cfg.Listen,
		Handler: handler.New(a.Cfg, a.DB, a.Store, a.Cache, a.Jobs, a.Push, a.AWS).Router(),
	}

	// graceful shutdown（NF-5）: SIGTERM で処理中リクエストを完了させてから終了
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", a.Cfg.Listen, "test_mode", a.Cfg.TestMode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig.String())
		shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
