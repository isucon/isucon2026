package control_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/gen/go/portal/v1/portalv1connect"
	"github.com/isucon/isucon2026/router/internal/control"
	"google.golang.org/protobuf/proto"
)

func TestConfig(t *testing.T) {
	cfg, err := control.LoadConfig(func(key string) string {
		if key == "ROUTER_CONTROL_TEAM_NUMBER" {
			return "7"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TeamNumber != 7 || cfg.ListenAddress != "127.0.0.1:18082" || cfg.DefaultCountry != "JP" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	for _, vars := range []map[string]string{
		{}, {"ROUTER_CONTROL_TEAM_NUMBER": "0"}, {"ROUTER_CONTROL_TEAM_NUMBER": "-1"},
		{"ROUTER_CONTROL_TEAM_NUMBER": "4294967296"},
		{"ROUTER_CONTROL_TEAM_NUMBER": "7", "ROUTER_CONTROL_DEFAULT_COUNTRY": "ZZ"},
		{"ROUTER_CONTROL_TEAM_NUMBER": "7", "ROUTER_CONTROL_LISTEN_ADDRESS": "0.0.0.0:18082"},
	} {
		if _, err := control.LoadConfig(func(k string) string { return vars[k] }); err == nil {
			t.Errorf("accepted invalid config: %v", vars)
		}
	}
}

func newServer(t *testing.T) portalv1connect.PortalRouterServiceClient {
	t.Helper()
	service, err := control.New(control.Config{TeamNumber: 7, ListenAddress: "127.0.0.1:18082", DefaultCountry: "JP"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	t.Cleanup(server.Close)
	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("health: %d", response.StatusCode)
	}
	// Only the Router protocol and health endpoint are exposed.
	response, err = server.Client().Get(server.URL + "/portal_mock.v1.PortalMockService/GetState")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("retired API: %d", response.StatusCode)
	}
	return portalv1connect.NewPortalRouterServiceClient(server.Client(), server.URL)
}

func snapshot(t *testing.T, client portalv1connect.PortalRouterServiceClient) *portalv1.ConfigSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.StreamDesired(ctx, connect.NewRequest(&portalv1.StreamDesiredRequest{Team: &portalv1.TeamKey{TeamNumber: 7}}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	return stream.Msg().Desired
}

func TestRouterProtocol(t *testing.T) {
	client := newServer(t)
	desired := snapshot(t, client)
	if desired.SnapshotId == "" || desired.IssuedAt.CheckValid() != nil {
		t.Fatalf("invalid snapshot: %v", desired)
	}
	regions := desired.Config.ServerRegions
	if regions.App1 != portalv1.Region_REGION_AP || regions.App2 != portalv1.Region_REGION_EU || regions.App3 != portalv1.Region_REGION_US || regions.Db1 != portalv1.Region_REGION_AP || desired.Config.DefaultCountry != "JP" {
		t.Fatalf("wrong configuration: %v", desired)
	}
	if !proto.Equal(desired, snapshot(t, client)) {
		t.Fatal("reconnect changed the desired snapshot")
	}
	if desired.SnapshotId == snapshot(t, newServer(t)).SnapshotId {
		t.Fatal("restart must issue a new snapshot")
	}
	report := &portalv1.ReportAppliedRequest{Team: &portalv1.TeamKey{TeamNumber: 7}, Result: &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{SnapshotId: desired.SnapshotId, Applied: desired.Config}}}
	if _, err := client.ReportApplied(context.Background(), connect.NewRequest(report)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		request *portalv1.ReportAppliedRequest
		code    connect.Code
	}{
		{"missing team", &portalv1.ReportAppliedRequest{}, connect.CodeInvalidArgument},
		{"wrong team", &portalv1.ReportAppliedRequest{Team: &portalv1.TeamKey{TeamNumber: 8}}, connect.CodeNotFound},
		{"missing result", &portalv1.ReportAppliedRequest{Team: report.Team}, connect.CodeInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.ReportApplied(context.Background(), connect.NewRequest(tc.request))
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("got %v", err)
			}
		})
	}
	changed := proto.Clone(report).(*portalv1.ReportAppliedRequest)
	changed.GetSuccess().Applied.DefaultCountry = "US"
	_, err := client.ReportApplied(context.Background(), connect.NewRequest(changed))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("accepted mismatched config: %v", err)
	}
	changed = proto.Clone(report).(*portalv1.ReportAppliedRequest)
	changed.GetSuccess().SnapshotId = "stale"
	_, err = client.ReportApplied(context.Background(), connect.NewRequest(changed))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("accepted stale snapshot: %v", err)
	}
	failure := &portalv1.ReportAppliedRequest{Team: report.Team, Result: &portalv1.ReportAppliedRequest_Failure{Failure: &portalv1.ApplyFailure{SnapshotId: desired.SnapshotId, Reason: "network unavailable"}}}
	if _, err := client.ReportApplied(context.Background(), connect.NewRequest(failure)); err != nil {
		t.Fatal(err)
	}
	// Failure reports must not replace the desired configuration.
	if !proto.Equal(desired, snapshot(t, client)) {
		t.Fatal("failure changed desired")
	}
}

func TestStreamRejectsOtherTeams(t *testing.T) {
	client := newServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.StreamDesired(ctx, connect.NewRequest(&portalv1.StreamDesiredRequest{Team: &portalv1.TeamKey{TeamNumber: 8}}))
	if err == nil {
		defer stream.Close()
		if stream.Receive() {
			t.Fatal("received another team's config")
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("got %v", err)
	}
}
