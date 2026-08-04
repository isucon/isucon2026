package rpc

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	portalv1connect "github.com/isucon/isucon2026/gen/go/portal/v1/portalv1connect"
	portalmockv1connect "github.com/isucon/isucon2026/gen/go/portal_mock/v1/portal_mockv1connect"
	"github.com/isucon/isucon2026/portal-mock/server/internal/benchmark"
	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/id"
	"github.com/isucon/isucon2026/portal-mock/server/internal/standin"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

type Application struct {
	Config  config.Config
	Store   *store.Store
	Handler http.Handler

	standIn *standin.StandIn
}

func NewApplication(cfg config.Config) (*Application, error) {
	return NewApplicationWithDependencies(cfg, clock.Real{}, id.UUID{})
}

func NewApplicationWithDependencies(cfg config.Config, clk clock.Clock, ids id.Generator) (*Application, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mode, profile := domainRuntime(cfg)
	processStart := clk.Now()
	stateStore, err := store.New(store.Options{
		Team:             domain.TeamKey{Number: cfg.TeamNumber},
		Hostname:         domain.HostnameConfig{RandomHash: cfg.TeamHash, DomainSuffix: cfg.DomainSuffix},
		RouterMode:       mode,
		StandInProfile:   profile,
		ProcessStartedAt: processStart,
		ApplyTimeout:     cfg.ApplyTimeout,
		Clock:            clk,
		ID:               ids,
	})
	if err != nil {
		return nil, err
	}
	benchmarkRunner, err := benchmark.NewRunner(stateStore, clk, cfg.BenchmarkPending, cfg.BenchmarkStep)
	if err != nil {
		stateStore.Close()
		return nil, err
	}
	routerHandler := NewRouterHandler(stateStore)
	mockHandler := NewMockHandler(stateStore, benchmarkRunner)
	mux := http.NewServeMux()
	routerPath, routerHTTPHandler := portalv1connect.NewPortalRouterServiceHandler(routerHandler)
	mockPath, mockHTTPHandler := portalmockv1connect.NewPortalMockServiceHandler(mockHandler)
	mux.Handle(routerPath, routerHTTPHandler)
	mux.Handle(mockPath, mockHTTPHandler)
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.Handle("/", staticFallback(cfg.WebDirectory))

	app := &Application{
		Config:  cfg,
		Store:   stateStore,
		Handler: withCORS(mux),
	}
	if mode == domain.RouterModeStandIn {
		app.standIn, err = standin.New(stateStore, clk, profile, cfg.StandInDelay, cfg.StandInRetryWait)
		if err != nil {
			stateStore.Close()
			return nil, err
		}
		app.standIn.Start(context.Background())
	}
	return app, nil
}

func (a *Application) Close() {
	if a.standIn != nil {
		a.standIn.Close()
	}
	a.Store.Close()
}

func (a *Application) HTTPServer() *http.Server {
	return &http.Server{
		Addr:              a.Config.ListenAddress,
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func Serve(ctx context.Context, app *Application, logger *slog.Logger) error {
	server := app.HTTPServer()
	errCh := make(chan error, 1)
	go func() {
		logger.Info("portal mock listening", "address", server.Addr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func domainRuntime(cfg config.Config) (domain.RouterMode, domain.StandInProfile) {
	if cfg.RouterMode == config.RouterModeExternalRouter {
		return domain.RouterModeExternalRouter, domain.StandInProfileNone
	}
	switch cfg.StandInProfile {
	case config.StandInFailOnceThenSuccess:
		return domain.RouterModeStandIn, domain.StandInProfileFailOnceThenSuccess
	case config.StandInNoReport:
		return domain.RouterModeStandIn, domain.StandInProfileNoReport
	default:
		return domain.RouterModeStandIn, domain.StandInProfileSuccess
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin != "" {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Vary", "Origin")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Grpc-Timeout, X-Grpc-Web")
			writer.Header().Set("Access-Control-Expose-Headers", "Grpc-Status, Grpc-Message")
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func staticFallback(directory string) http.Handler {
	if directory == "" {
		return http.NotFoundHandler()
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSystem := os.DirFS(root)
	fileServer := http.FileServer(http.FS(fileSystem))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(writer, request)
			return
		}
		name := strings.TrimPrefix(filepath.Clean(request.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		if info, statErr := fs.Stat(fileSystem, name); statErr != nil || info.IsDir() {
			name = "index.html"
		}
		if extension := filepath.Ext(name); extension != "" {
			if contentType := mime.TypeByExtension(extension); contentType != "" {
				writer.Header().Set("Content-Type", contentType)
			}
		}
		originalPath := request.URL.Path
		request.URL.Path = "/" + name
		fileServer.ServeHTTP(writer, request)
		request.URL.Path = originalPath
	})
}
