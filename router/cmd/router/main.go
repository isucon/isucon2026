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

	routerconfig "github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/netcontrol"
	"github.com/isucon/isucon2026/router/internal/reconcile"
	statusstore "github.com/isucon/isucon2026/router/internal/status"
	routerxds "github.com/isucon/isucon2026/router/internal/xds"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Router stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := routerconfig.FromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	state := statusstore.New()
	xdsServer, err := routerxds.NewManagementServer(cfg.EnvoyNodeID, state.SetEnvoyConnected, state.SetEnvoySynced)
	if err != nil {
		return err
	}
	if err := xdsServer.Start(ctx, cfg.XDSListenAddress); err != nil {
		return err
	}

	statusServer := &http.Server{
		Addr: cfg.StatusListenAddress, Handler: statusstore.Handler(state), ReadHeaderTimeout: 3 * time.Second,
	}
	statusErrors := make(chan error, 1)
	go func() { statusErrors <- statusServer.ListenAndServe() }()
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = statusServer.Shutdown(shutdownCtx)
	}()

	portalClient, err := reconcile.NewPortalClient(
		cfg.PortalEndpoint, cfg.TeamNumber, &http.Client{}, cfg.ReconnectMinBackoff, cfg.ReconnectMaxBackoff,
	)
	if err != nil {
		return err
	}
	network := netcontrol.NewController(cfg, netcontrol.OSExecutor{}, netcontrol.OSInterfaceResolver{})
	controller := reconcile.NewController(cfg, portalClient, network, xdsServer, state)
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
