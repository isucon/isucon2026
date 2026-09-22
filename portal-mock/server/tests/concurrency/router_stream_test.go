package concurrency

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

func TestSlowRouterEventuallyReceivesLatestDesired(t *testing.T) {
	server := testkit.NewServer(t, func(cfg *config.Config) {
		cfg.RouterMode = config.RouterModeExternalRouter
		cfg.StandInProfile = ""
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := server.Router.StreamDesired(ctx, connect.NewRequest(&portalv1.StreamDesiredRequest{
		Team: &portalv1.TeamKey{TeamNumber: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var latest string
	for _, main := range []string{
		"DE",
		"US",
		"JP",
	} {
		response, updateErr := server.Mock.UpdateDesired(context.Background(), connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
			Patch: &portalmockv1.DesiredPatch{DefaultCountry: &main},
		}))
		if updateErr != nil {
			t.Fatal(updateErr)
		}
		latest = response.Msg.Desired.SnapshotId
	}
	for stream.Receive() {
		if stream.Msg().Desired.SnapshotId == latest {
			return
		}
	}
	t.Fatalf("latest desired was not received: %v", stream.Err())
}
