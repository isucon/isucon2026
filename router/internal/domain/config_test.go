package domain

import (
	"testing"

	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
)

func TestCountryRegions(t *testing.T) {
	t.Parallel()
	want := map[string]Region{
		"US": RegionUS, "CA": RegionUS, "MX": RegionUS,
		"JP": RegionAP, "KR": RegionAP, "CN": RegionAP, "AU": RegionAP,
		"GB": RegionEU, "DE": RegionEU, "FR": RegionEU,
	}
	if got := Countries(); len(got) != len(want) {
		t.Fatalf("Countries() length = %d, want %d", len(got), len(want))
	}
	for code, region := range want {
		got, ok := CountryRegion(code)
		if !ok || got != region {
			t.Errorf("CountryRegion(%q) = %q, %v; want %q, true", code, got, ok, region)
		}
	}
	for _, code := range []string{"", "jp", "JPN", "XX", "AP", " US"} {
		if _, ok := CountryRegion(code); ok {
			t.Errorf("CountryRegion(%q) unexpectedly succeeded", code)
		}
	}
}

func TestConfigFromProtoRoundTrip(t *testing.T) {
	t.Parallel()
	in := &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			Server1: portalv1.Region_REGION_AP,
			Server2: portalv1.Region_REGION_EU,
			Server3: portalv1.Region_REGION_US,
		},
		MainServer:     portalv1.ServerId_SERVER_ID_SERVER3,
		DefaultCountry: "CA",
	}
	cfg, err := ConfigFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RegionFor(Server2) != RegionEU || cfg.MainServer != Server3 || cfg.DefaultCountry != "CA" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	out := cfg.Proto()
	if out.GetServerRegions().GetServer1() != in.GetServerRegions().GetServer1() ||
		out.GetServerRegions().GetServer2() != in.GetServerRegions().GetServer2() ||
		out.GetServerRegions().GetServer3() != in.GetServerRegions().GetServer3() ||
		out.GetMainServer() != in.GetMainServer() || out.GetDefaultCountry() != in.GetDefaultCountry() {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
	}
}

func TestConfigFromProtoRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	valid := func() *portalv1.Config {
		return &portalv1.Config{
			ServerRegions: &portalv1.ServerRegions{
				Server1: portalv1.Region_REGION_AP,
				Server2: portalv1.Region_REGION_EU,
				Server3: portalv1.Region_REGION_US,
			},
			MainServer: portalv1.ServerId_SERVER_ID_SERVER1, DefaultCountry: "JP",
		}
	}
	tests := map[string]func() *portalv1.Config{
		"nil":             func() *portalv1.Config { return nil },
		"missing regions": func() *portalv1.Config { c := valid(); c.ServerRegions = nil; return c },
		"unspecified region": func() *portalv1.Config {
			c := valid()
			c.ServerRegions.Server2 = portalv1.Region_REGION_UNSPECIFIED
			return c
		},
		"unknown region": func() *portalv1.Config { c := valid(); c.ServerRegions.Server2 = portalv1.Region(99); return c },
		"same region":    func() *portalv1.Config { c := valid(); c.ServerRegions.Server2 = portalv1.Region_REGION_AP; return c },
		"swapped regions": func() *portalv1.Config {
			c := valid()
			c.ServerRegions.Server2, c.ServerRegions.Server3 = c.ServerRegions.Server3, c.ServerRegions.Server2
			return c
		},
		"unknown server":  func() *portalv1.Config { c := valid(); c.MainServer = portalv1.ServerId(99); return c },
		"unknown country": func() *portalv1.Config { c := valid(); c.DefaultCountry = "XX"; return c },
	}
	for name, makeConfig := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ConfigFromProto(makeConfig()); err == nil {
				t.Fatal("ConfigFromProto unexpectedly succeeded")
			}
		})
	}
}

func TestSnapshotFromProto(t *testing.T) {
	t.Parallel()
	_, err := SnapshotFromProto(&portalv1.ConfigSnapshot{SnapshotId: "", Config: validProtoConfig()}, 7)
	if err == nil {
		t.Fatal("empty snapshot ID unexpectedly succeeded")
	}
	s, err := SnapshotFromProto(&portalv1.ConfigSnapshot{SnapshotId: "snapshot-1", Config: validProtoConfig()}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "snapshot-1" || s.Generation != 7 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
}

func validProtoConfig() *portalv1.Config {
	return &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			Server1: portalv1.Region_REGION_AP,
			Server2: portalv1.Region_REGION_EU,
			Server3: portalv1.Region_REGION_US,
		},
		MainServer: portalv1.ServerId_SERVER_ID_SERVER1, DefaultCountry: "JP",
	}
}
