package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	routerconfig "github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/haproxy"
	"github.com/isucon/isucon2026/router/internal/netcontrol"
	"github.com/isucon/isucon2026/router/internal/reconcile"
	statusstore "github.com/isucon/isucon2026/router/internal/status"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	run := runController
	if len(os.Args) == 2 && os.Args[1] == "haproxy-config" {
		run = writeHAProxyConfig
	} else if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: isureal-router [haproxy-config]")
		os.Exit(2)
	}
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Router stopped with error", "error", err)
		os.Exit(1)
	}
}

// writeHAProxyConfig writes HAProxy's files into ROUTER_HAPROXY_DIR from the
// Router environment. HAProxy's service runs it before HAProxy starts.
func writeHAProxyConfig() error {
	cfg, err := routerconfig.FromEnv()
	if err != nil {
		return err
	}
	files, err := haproxy.Render(cfg)
	if err != nil {
		return err
	}
	return haproxy.Write(cfg.HAProxyDir, files)
}

func runController() error {
	cfg, err := routerconfig.FromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	state := statusstore.New()
	portalClient, err := reconcile.NewPortalClient(
		cfg.PortalEndpoint, cfg.TeamNumber, &http.Client{}, cfg.ReconnectMinBackoff, cfg.ReconnectMaxBackoff,
	)
	if err != nil {
		return err
	}
	network := netcontrol.NewController(cfg, netcontrol.OSExecutor{}, netcontrol.OSInterfaceResolver{})
	proxy := haproxy.NewRuntime(cfg)
	controller := reconcile.NewController(cfg, portalClient, network, proxy, state)
	probe := func(ctx context.Context) error {
		active := controller.Active()
		if active == nil {
			return errors.New("no active snapshot")
		}
		return proxy.Serving(ctx, active.Config)
	}

	statusServer := &http.Server{
		Addr: cfg.StatusListenAddress, Handler: statusstore.Handler(state, probe), ReadHeaderTimeout: 3 * time.Second,
	}
	statusErrors := make(chan error, 1)
	go func() { statusErrors <- statusServer.ListenAndServe() }()
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = statusServer.Shutdown(shutdownCtx)
	}()
	slog.Info("Router starting", "team_number", cfg.TeamNumber, "portal_endpoint", cfg.PortalEndpoint)

	controllerResult := make(chan error, 1)
	go func() { controllerResult <- controller.Run(ctx) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-controllerResult:
		return err
	case err := <-statusErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
