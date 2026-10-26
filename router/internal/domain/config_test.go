package domain

import (
	"testing"
	"time"

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
			App1: portalv1.Region_REGION_AP,
			App2: portalv1.Region_REGION_EU,
			App3: portalv1.Region_REGION_US,
			Db1:  portalv1.Region_REGION_AP,
		},
		DefaultCountry: "CA",
	}
	cfg, err := ConfigFromProto(in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RegionFor(App2) != RegionEU || cfg.DefaultCountry != "CA" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	out := cfg.Proto()
	if out.GetServerRegions().GetApp1() != in.GetServerRegions().GetApp1() ||
		out.GetServerRegions().GetApp2() != in.GetServerRegions().GetApp2() ||
		out.GetServerRegions().GetApp3() != in.GetServerRegions().GetApp3() ||
		out.GetDefaultCountry() != in.GetDefaultCountry() {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
	}
}

func TestConfigFromProtoRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	valid := func() *portalv1.Config {
		return &portalv1.Config{
			ServerRegions: &portalv1.ServerRegions{
				App1: portalv1.Region_REGION_AP,
				App2: portalv1.Region_REGION_EU,
				App3: portalv1.Region_REGION_US,
				Db1:  portalv1.Region_REGION_AP,
			},
			DefaultCountry: "JP",
		}
	}
	tests := map[string]func() *portalv1.Config{
		"nil":             func() *portalv1.Config { return nil },
		"missing regions": func() *portalv1.Config { c := valid(); c.ServerRegions = nil; return c },
		"unspecified region": func() *portalv1.Config {
			c := valid()
			c.ServerRegions.App2 = portalv1.Region_REGION_UNSPECIFIED
			return c
		},
		"unknown region": func() *portalv1.Config { c := valid(); c.ServerRegions.App2 = portalv1.Region(99); return c },
		"same region":    func() *portalv1.Config { c := valid(); c.ServerRegions.App2 = portalv1.Region_REGION_AP; return c },
		"swapped regions": func() *portalv1.Config {
			c := valid()
			c.ServerRegions.App2, c.ServerRegions.App3 = c.ServerRegions.App3, c.ServerRegions.App2
			return c
		},
		"unknown country": func() *portalv1.Config { c := valid(); c.DefaultCountry = "XX"; return c },
		"db outside AP":   func() *portalv1.Config { c := valid(); c.ServerRegions.Db1 = portalv1.Region_REGION_EU; return c },
		"missing db region": func() *portalv1.Config {
			c := valid()
			c.ServerRegions.Db1 = portalv1.Region_REGION_UNSPECIFIED
			return c
		},
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

func TestTrafficProfileClassPerRegionPair(t *testing.T) {
	t.Parallel()
	profile := TrafficProfile{
		SameRegion: TrafficClass{BandwidthBPS: 1000},
		APEU:       TrafficClass{Delay: 1 * time.Millisecond, BandwidthBPS: 100},
		EUUS:       TrafficClass{Delay: 2 * time.Millisecond, BandwidthBPS: 200},
		APUS:       TrafficClass{Delay: 3 * time.Millisecond, BandwidthBPS: 300},
	}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		source, destination Region
		want                TrafficClass
	}{
		{RegionAP, RegionAP, profile.SameRegion},
		{RegionEU, RegionEU, profile.SameRegion},
		{RegionUS, RegionUS, profile.SameRegion},
		{RegionAP, RegionEU, profile.APEU},
		{RegionEU, RegionAP, profile.APEU},
		{RegionEU, RegionUS, profile.EUUS},
		{RegionUS, RegionEU, profile.EUUS},
		{RegionAP, RegionUS, profile.APUS},
		{RegionUS, RegionAP, profile.APUS},
	}
	for _, test := range tests {
		if got := profile.Class(test.source, test.destination); got != test.want {
			t.Errorf("Class(%s, %s) = %+v, want %+v", test.source, test.destination, got, test.want)
		}
	}
}

func TestTrafficProfileRejectsInvalidPair(t *testing.T) {
	t.Parallel()
	valid := func() TrafficProfile {
		class := TrafficClass{Delay: 200 * time.Millisecond, BandwidthBPS: 200}
		return TrafficProfile{SameRegion: TrafficClass{BandwidthBPS: 1000}, APEU: class, EUUS: class, APUS: class}
	}
	tests := map[string]func(*TrafficProfile){
		"negative same delay": func(p *TrafficProfile) { p.SameRegion.Delay = -time.Millisecond },
		"negative pair delay": func(p *TrafficProfile) { p.EUUS.Delay = -time.Millisecond },
		"zero same bandwidth": func(p *TrafficProfile) { p.SameRegion.BandwidthBPS = 0 },
		"zero pair bandwidth": func(p *TrafficProfile) { p.APUS.BandwidthBPS = 0 },
		"pair over same":      func(p *TrafficProfile) { p.APEU.BandwidthBPS = 1001 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profile := valid()
			mutate(&profile)
			if err := profile.Validate(); err == nil {
				t.Fatal("Validate unexpectedly succeeded")
			}
		})
	}
}

func validProtoConfig() *portalv1.Config {
	return &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			App1: portalv1.Region_REGION_AP,
			App2: portalv1.Region_REGION_EU,
			App3: portalv1.Region_REGION_US,
			Db1:  portalv1.Region_REGION_AP,
		},
		DefaultCountry: "JP",
	}
}
