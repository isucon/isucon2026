package contract

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/config"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestRouterStreamInitialUpdateResetAndReconnect(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := server.Router.StreamDesired(ctx, connect.NewRequest(&portalv1.StreamDesiredRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	server2 := "DE"
	update, err := server.Mock.UpdateDesired(context.Background(), connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{DefaultCountry: &server2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Desired.SnapshotId != update.Msg.Desired.SnapshotId {
		t.Fatalf("update not delivered: %v", stream.Err())
	}
	reset, err := server.Mock.Reset(context.Background(), connect.NewRequest(&portalmockv1.ResetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Desired.SnapshotId != reset.Msg.State.Portal.Desired.SnapshotId {
		t.Fatalf("reset not delivered: %v", stream.Err())
	}
	cancel()
	_ = stream.Close()

	reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), time.Second)
	defer reconnectCancel()
	reconnected, err := server.Router.StreamDesired(reconnectCtx, connect.NewRequest(&portalv1.StreamDesiredRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !reconnected.Receive() || reconnected.Msg().Desired.SnapshotId != reset.Msg.State.Portal.Desired.SnapshotId {
		t.Fatalf("reconnect did not receive current desired: %v", reconnected.Err())
	}

	for _, team := range []uint32{0, 2} {
		invalidCtx, invalidCancel := context.WithTimeout(context.Background(), time.Second)
		invalid, callErr := server.Router.StreamDesired(invalidCtx, connect.NewRequest(&portalv1.StreamDesiredRequest{
			Team: &portalv1.TeamKey{TeamNumber: team},
		}))
		if callErr == nil {
			if invalid.Receive() {
				t.Fatalf("team %d unexpectedly received data", team)
			}
			callErr = invalid.Err()
		}
		invalidCancel()
		want := connect.CodeInvalidArgument
		if team == 2 {
			want = connect.CodeNotFound
		}
		assertCode(t, callErr, want)
	}

	response, err := server.HTTP.Client().Get(server.HTTP.URL + "/portal.v2.PortalRouterService/StreamDesired")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("unsupported v2 route returned %d", response.StatusCode)
	}
}
