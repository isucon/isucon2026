package reconcile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/gen/go/portal/v1/portalv1connect"
)

func TestPortalClientReconnectsAndUsesConfiguredTeam(t *testing.T) {
	t.Parallel()
	handler := &portalTestHandler{snapshots: []*portalv1.ConfigSnapshot{
		testProtoSnapshot("first", "JP"),
		testProtoSnapshot("second", "US"),
	}}
	server := newPortalTestServer(t, handler)
	client, err := NewPortalClient(server.URL, 7, server.Client(), 5*time.Millisecond, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	received := make(chan string, 2)
	connections := make(chan bool, 4)
	done := make(chan error, 1)
	go func() {
		done <- client.Stream(ctx, func(snapshot *portalv1.ConfigSnapshot) error {
			received <- snapshot.SnapshotId
			if snapshot.SnapshotId == "second" {
				cancel()
			}
			return nil
		}, func(connected bool) { connections <- connected })
	}()
	if got := <-received; got != "first" {
		t.Fatalf("first snapshot = %q", got)
	}
	if got := <-received; got != "second" {
		t.Fatalf("second snapshot = %q", got)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream = %v", err)
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.streamCalls < 2 || handler.lastTeam != 7 {
		t.Fatalf("stream calls=%d team=%d", handler.streamCalls, handler.lastTeam)
	}
}

func TestPortalClientReportsSuccessAndFailure(t *testing.T) {
	t.Parallel()
	handler := &portalTestHandler{}
	server := newPortalTestServer(t, handler)
	client, err := NewPortalClient(server.URL, 3, server.Client(), time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testDomainConfig()
	if err := client.Report(context.Background(), ApplyReport{SnapshotID: "ok", Config: cfg, Success: true}); err != nil {
		t.Fatal(err)
	}
	if err := client.Report(context.Background(), ApplyReport{SnapshotID: "bad", Success: false, Reason: "tc failed"}); err != nil {
		t.Fatal(err)
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.reports) != 2 || handler.reports[0].GetSuccess().GetSnapshotId() != "ok" || handler.reports[1].GetFailure().GetReason() != "tc failed" {
		t.Fatalf("unexpected reports: %+v", handler.reports)
	}
	for _, report := range handler.reports {
		if report.GetTeam().GetTeamNumber() != 3 {
			t.Fatalf("report team = %d", report.GetTeam().GetTeamNumber())
		}
	}
}

type portalTestHandler struct {
	portalv1connect.UnimplementedPortalRouterServiceHandler
	mu          sync.Mutex
	snapshots   []*portalv1.ConfigSnapshot
	streamCalls int
	lastTeam    uint32
	reports     []*portalv1.ReportAppliedRequest
}

func (h *portalTestHandler) StreamDesired(ctx context.Context, request *connect.Request[portalv1.StreamDesiredRequest], stream *connect.ServerStream[portalv1.StreamDesiredResponse]) error {
	h.mu.Lock()
	call := h.streamCalls
	h.streamCalls++
	h.lastTeam = request.Msg.GetTeam().GetTeamNumber()
	var snapshot *portalv1.ConfigSnapshot
	if call < len(h.snapshots) {
		snapshot = h.snapshots[call]
	}
	h.mu.Unlock()
	if snapshot == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := stream.Send(&portalv1.StreamDesiredResponse{Desired: snapshot}); err != nil {
		return err
	}
	if call == 0 && len(h.snapshots) > 1 {
		return connect.NewError(connect.CodeUnavailable, errors.New("test disconnect"))
	}
	<-ctx.Done()
	return ctx.Err()
}

func (h *portalTestHandler) ReportApplied(_ context.Context, request *connect.Request[portalv1.ReportAppliedRequest]) (*connect.Response[portalv1.ReportAppliedResponse], error) {
	h.mu.Lock()
	h.reports = append(h.reports, request.Msg)
	h.mu.Unlock()
	return connect.NewResponse(&portalv1.ReportAppliedResponse{}), nil
}

func newPortalTestServer(t *testing.T, handler portalv1connect.PortalRouterServiceHandler) *httptest.Server {
	t.Helper()
	path, rpcHandler := portalv1connect.NewPortalRouterServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, rpcHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func testProtoSnapshot(id string, country string) *portalv1.ConfigSnapshot {
	return &portalv1.ConfigSnapshot{SnapshotId: id, Config: &portalv1.Config{
		ServerRegions:  &portalv1.ServerRegions{Server1: portalv1.Region_REGION_AP, Server2: portalv1.Region_REGION_EU, Server3: portalv1.Region_REGION_US},
		DefaultCountry: country,
	}}
}
