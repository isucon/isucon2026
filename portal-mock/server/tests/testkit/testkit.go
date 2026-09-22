package testkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalv1connect "github.com/isucon/isucon2026/gen/go/portal/v1/portalv1connect"
	portalmockv1connect "github.com/isucon/isucon2026/gen/go/portal_mock/v1/portal_mockv1connect"
	"github.com/isucon/isucon2026/portal-mock/server/internal/clock"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/id"
	"github.com/isucon/isucon2026/portal-mock/server/internal/rpc"
)

type Server struct {
	App       *rpc.Application
	HTTP      *httptest.Server
	Clock     *clock.Fake
	Router    portalv1connect.PortalRouterServiceClient
	Mock      portalmockv1connect.PortalMockServiceClient
	closeOnce bool
}

func NewServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.TeamHash = "testhash"
	cfg.DomainSuffix = "example.test"
	cfg.ApplyTimeout = time.Second
	cfg.StandInDelay = 100 * time.Millisecond
	cfg.StandInRetryWait = 100 * time.Millisecond
	cfg.BenchmarkPending = 100 * time.Millisecond
	cfg.BenchmarkStep = 100 * time.Millisecond
	if mutate != nil {
		mutate(&cfg)
	}
	fakeClock := clock.NewFake(time.Unix(1_700_000_000, 0))
	app, err := rpc.NewApplicationWithDependencies(cfg, fakeClock, id.NewFake("test"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler)
	result := &Server{
		App: app, HTTP: server, Clock: fakeClock,
		Router: portalv1connect.NewPortalRouterServiceClient(http.DefaultClient, server.URL),
		Mock:   portalmockv1connect.NewPortalMockServiceClient(http.DefaultClient, server.URL),
	}
	t.Cleanup(result.Close)
	return result
}

func (s *Server) Close() {
	if s.closeOnce {
		return
	}
	s.closeOnce = true
	s.HTTP.Close()
	s.App.Close()
}

func Eventually(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied")
}

func ReceiveWithin[T any](t *testing.T, ctx context.Context, receive func() bool, message func() T) T {
	t.Helper()
	result := make(chan struct {
		value T
		ok    bool
	}, 1)
	go func() {
		ok := receive()
		result <- struct {
			value T
			ok    bool
		}{value: message(), ok: ok}
	}()
	select {
	case got := <-result:
		if !got.ok {
			t.Fatal("stream ended before receiving a message")
		}
		return got.value
	case <-ctx.Done():
		t.Fatal("timed out waiting for stream message")
		var zero T
		return zero
	}
}

func ConfigProto(config domain.Config) *portalv1.Config {
	return &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			Server1: config.Server1Region,
			Server2: config.Server2Region,
			Server3: config.Server3Region,
		},
		DefaultCountry: config.DefaultCountry,
	}
}
