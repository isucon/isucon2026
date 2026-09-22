package contract

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/tests/testkit"
)

func TestMockConfigurationContract(t *testing.T) {
	server := testkit.NewServer(t, nil)
	ctx := context.Background()
	initial, err := server.Mock.GetState(ctx, connect.NewRequest(&portalmockv1.GetStateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !initial.Msg.State.Portal.Converged || initial.Msg.State.Portal.Desired.Config.DefaultCountry != "JP" {
		t.Fatalf("unexpected initial state: %v", initial.Msg.State)
	}
	countries, err := server.Mock.ListSupportedCountries(ctx, connect.NewRequest(&portalmockv1.ListSupportedCountriesRequest{}))
	if err != nil || len(countries.Msg.Countries) != 10 {
		t.Fatalf("countries=%d err=%v", len(countries.Msg.Countries), err)
	}
	main := "JP"
	noOp, err := server.Mock.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{DefaultCountry: &main},
	}))
	if err != nil || noOp.Msg.Changed || noOp.Msg.Desired.SnapshotId != initial.Msg.State.Portal.Desired.SnapshotId {
		t.Fatalf("bad no-op response: %v %v", noOp, err)
	}
	_, err = server.Mock.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{}))
	assertCode(t, err, connect.CodeInvalidArgument)
	invalidCountry := "XX"
	_, err = server.Mock.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{DefaultCountry: &invalidCountry},
	}))
	assertCode(t, err, connect.CodeInvalidArgument)
	reset, err := server.Mock.Reset(ctx, connect.NewRequest(&portalmockv1.ResetRequest{}))
	if err != nil || reset.Msg.State.Portal.Desired.SnapshotId == initial.Msg.State.Portal.Desired.SnapshotId {
		t.Fatalf("bad reset response: %v %v", reset, err)
	}
}

func assertCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if err == nil || connect.CodeOf(err) != code {
		t.Fatalf("got error %v, want code %s", err, code)
	}
}

func TestFixedRegionsRejectUserConfiguration(t *testing.T) {
	server := testkit.NewServer(t, nil)
	ctx := context.Background()
	initial, err := server.Mock.GetState(ctx, connect.NewRequest(&portalmockv1.GetStateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	want := initial.Msg.State.Portal.Desired
	regions := want.Config.ServerRegions
	if regions.Server1 != portalv1.Region_REGION_AP || regions.Server2 != portalv1.Region_REGION_EU || regions.Server3 != portalv1.Region_REGION_US {
		t.Fatalf("unexpected fixed regions: %v", regions)
	}
	for _, region := range []portalv1.Region{portalv1.Region_REGION_UNSPECIFIED, portalv1.Region_REGION_AP, portalv1.Region_REGION_EU, portalv1.Region_REGION_US, 99} {
		for _, patch := range []*portalmockv1.DesiredPatch{
			{Server1Region: region.Enum()},
			{Server2Region: region.Enum()},
			{Server3Region: region.Enum()},
		} {
			// A rejected region must also prevent other fields from changing.
			country := "US"
			patch.DefaultCountry = &country
			_, err := server.Mock.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{Patch: patch}))
			assertCode(t, err, connect.CodeInvalidArgument)
		}
	}
	after, err := server.Mock.GetState(ctx, connect.NewRequest(&portalmockv1.GetStateRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if after.Msg.State.Portal.Desired.SnapshotId != want.SnapshotId || after.Msg.State.Portal.Desired.Config.DefaultCountry != want.Config.DefaultCountry {
		t.Fatal("rejected region update changed desired state")
	}
	reset, err := server.Mock.Reset(ctx, connect.NewRequest(&portalmockv1.ResetRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	resetRegions := reset.Msg.State.Portal.Desired.Config.ServerRegions
	if resetRegions.Server1 != regions.Server1 || resetRegions.Server2 != regions.Server2 || resetRegions.Server3 != regions.Server3 {
		t.Fatalf("reset changed fixed regions: %v", resetRegions)
	}
}
