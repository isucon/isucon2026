package netcontrol

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

func TestRenderTCClassifiesServerPairsByRegion(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "s1", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"}
	networks := testNetworks()
	cfg := domain.Config{
		App1Region:     domain.RegionAP,
		App2Region:     domain.RegionEU,
		App3Region:     domain.RegionUS,
		DefaultCountry: "JP",
	}
	profile := domain.TrafficProfile{
		SameRegion: domain.TrafficClass{BandwidthBPS: 100_000_000},
		APEU:       domain.TrafficClass{Delay: 120 * time.Millisecond, BandwidthBPS: 1_000_000},
		EUUS:       domain.TrafficClass{Delay: 80 * time.Millisecond, BandwidthBPS: 2_000_000},
		APUS:       domain.TrafficClass{Delay: 160 * time.Millisecond, BandwidthBPS: 3_000_000},
	}
	batches, err := RenderTC(interfaces, networks, cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 4 {
		t.Fatalf("batch count = %d, want 4", len(batches))
	}
	s1 := batches["s1"]
	for _, required := range []string{
		"qdisc replace dev s1 root handle 1: htb default 10",
		"class replace dev s1 parent 1: classid 1:10 htb rate 100000kbit ceil 100000kbit",
		"class replace dev s1 parent 1: classid 1:22 htb rate 100000kbit ceil 100000kbit",
		"qdisc replace dev s1 parent 1:22 handle 22: pfifo limit 1000",
		"class replace dev s1 parent 1: classid 1:32 htb rate 1000kbit ceil 1000kbit",
		"qdisc replace dev s1 parent 1:32 handle 32: netem delay 60ms limit 1000",
		"class replace dev s1 parent 1: classid 1:13 htb rate 2000kbit ceil 2000kbit",
		"qdisc replace dev s1 parent 1:13 handle 13: netem delay 40ms limit 1000",
		"class replace dev s1 parent 1: classid 1:21 htb rate 3000kbit ceil 3000kbit",
		"qdisc replace dev s1 parent 1:21 handle 21: netem delay 80ms limit 1000",
		"filter replace dev s1 protocol ip parent 1: prio 11 u32 match ip src 172.31.1.10/32 match ip dst 172.31.2.10/32 flowid 1:23",
		"filter replace dev s1 protocol ip parent 1: prio 14 u32 match ip src 172.31.2.10/32 match ip dst 172.31.1.10/32 flowid 1:32",
		"filter replace dev s1 protocol ip parent 1: prio 13 u32 match ip src 172.31.1.10/32 match ip dst 172.31.4.10/32 flowid 1:22",
		"filter replace dev s1 protocol ip parent 1: prio 12 u32 match ip src 172.31.1.10/32 match ip dst 172.31.3.10/32 flowid 1:21",
		"filter replace dev s1 protocol ip parent 1: prio 15 u32 match ip src 172.31.2.10/32 match ip dst 172.31.3.10/32 flowid 1:31",
		"filter replace dev s1 protocol ip parent 1: prio 19 u32 match ip src 172.31.3.10/32 match ip dst 172.31.4.10/32 flowid 1:12",
	} {
		if !strings.Contains(s1, required) {
			t.Errorf("s1 batch missing %q\n%s", required, s1)
		}
	}
	again, err := RenderTC(interfaces, networks, cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batches, again) {
		t.Fatal("RenderTC output is not deterministic")
	}
}

func TestRenderTCSharedInterfaceUsesOneTree(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "data0", domain.App2: "data0", domain.App3: "data0", domain.DB1: "data0"}
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	crossRegion := domain.TrafficClass{Delay: 200 * time.Millisecond, BandwidthBPS: 200_000_000}
	profile := domain.TrafficProfile{SameRegion: domain.TrafficClass{BandwidthBPS: 1_000_000_000}, APEU: crossRegion, EUUS: crossRegion, APUS: crossRegion}
	batches, err := RenderTC(interfaces, testNetworks(), cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 {
		t.Fatalf("batch count = %d, want 1", len(batches))
	}
	batch := batches["data0"]
	if got := strings.Count(batch, "root handle 1:"); got != 1 {
		t.Fatalf("root qdisc count = %d, want 1\n%s", got, batch)
	}
	if got := strings.Count(batch, "u32 match ip src"); got != 12 {
		t.Fatalf("server pair filter count = %d, want 12\n%s", got, batch)
	}
	// 200Mbps for a 100ms one-way delay keeps about 1666 full-sized packets in flight.
	if !strings.Contains(batch, "qdisc replace dev data0 parent 1:23 handle 23: netem delay 100ms limit 3332") {
		t.Fatalf("cross-region netem limit is not sized for the bandwidth-delay product\n%s", batch)
	}
}

func TestRenderTCAllRegionCombinations(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "s1", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"}
	profile := domain.TrafficProfile{
		SameRegion: domain.TrafficClass{BandwidthBPS: 10_000},
		APEU:       domain.TrafficClass{Delay: time.Millisecond, BandwidthBPS: 1_000},
		EUUS:       domain.TrafficClass{Delay: 2 * time.Millisecond, BandwidthBPS: 2_000},
		APUS:       domain.TrafficClass{Delay: 3 * time.Millisecond, BandwidthBPS: 3_000},
	}
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	batches, err := RenderTC(interfaces, testNetworks(), cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		source, destination domain.Region
		wantRate, wantDelay string
	}{
		{domain.RegionUS, domain.RegionUS, "10kbit", ""},
		{domain.RegionAP, domain.RegionAP, "10kbit", ""},
		{domain.RegionEU, domain.RegionEU, "10kbit", ""},
		{domain.RegionAP, domain.RegionEU, "1kbit", "500us"},
		{domain.RegionEU, domain.RegionAP, "1kbit", "500us"},
		{domain.RegionEU, domain.RegionUS, "2kbit", "1ms"},
		{domain.RegionUS, domain.RegionEU, "2kbit", "1ms"},
		{domain.RegionAP, domain.RegionUS, "3kbit", "1500us"},
		{domain.RegionUS, domain.RegionAP, "3kbit", "1500us"},
	}
	for _, batch := range batches {
		for _, test := range tests {
			classID := pairClassID(test.source, test.destination)
			leaf := "handle " + classID + ": netem delay " + test.wantDelay + " "
			if test.wantDelay == "" {
				leaf = "handle " + classID + ": pfifo limit "
			}
			if !strings.Contains(batch, "classid 1:"+classID+" htb rate "+test.wantRate+" ") || !strings.Contains(batch, leaf) {
				t.Errorf("source=%s destination=%s has wrong class\n%s", test.source, test.destination, batch)
			}
		}
	}
}

func TestRenderTCRejectsSubMillisecondPairDelay(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "s1", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"}
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	crossRegion := domain.TrafficClass{Delay: 200 * time.Millisecond, BandwidthBPS: 1_000}
	profile := domain.TrafficProfile{SameRegion: domain.TrafficClass{BandwidthBPS: 10_000}, APEU: crossRegion, EUUS: crossRegion, APUS: crossRegion}
	profile.EUUS.Delay = 500 * time.Microsecond
	if _, err := RenderTC(interfaces, testNetworks(), cfg, profile); err == nil {
		t.Fatal("RenderTC unexpectedly accepted a sub-millisecond EU-US delay")
	}
}

func TestRenderTCRejectsUnsafeInterface(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "s1\" accept", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"}
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	crossRegion := domain.TrafficClass{BandwidthBPS: 1_000}
	profile := domain.TrafficProfile{SameRegion: domain.TrafficClass{BandwidthBPS: 10_000}, APEU: crossRegion, EUUS: crossRegion, APUS: crossRegion}
	if _, err := RenderTC(interfaces, testNetworks(), cfg, profile); err == nil {
		t.Fatal("RenderTC unexpectedly accepted an unsafe interface name")
	}
}

func testNetworks() map[domain.ServerID]config.ServerNetwork {
	return map[domain.ServerID]config.ServerNetwork{
		domain.App1: {Subnet: netip.MustParsePrefix("172.31.1.0/24"), RouterIP: netip.MustParseAddr("172.31.1.2"), ServerIP: netip.MustParseAddr("172.31.1.10")},
		domain.App2: {Subnet: netip.MustParsePrefix("172.31.2.0/24"), RouterIP: netip.MustParseAddr("172.31.2.2"), ServerIP: netip.MustParseAddr("172.31.2.10")},
		domain.App3: {Subnet: netip.MustParsePrefix("172.31.3.0/24"), RouterIP: netip.MustParseAddr("172.31.3.2"), ServerIP: netip.MustParseAddr("172.31.3.10")},
		domain.DB1:  {Subnet: netip.MustParsePrefix("172.31.4.0/24"), RouterIP: netip.MustParseAddr("172.31.4.2"), ServerIP: netip.MustParseAddr("172.31.4.10")},
	}
}
