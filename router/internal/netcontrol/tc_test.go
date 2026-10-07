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
		SameRegionDelay: 0, CrossRegionDelay: 120 * time.Millisecond,
		SameRegionBandwidthBPS: 100_000_000, CrossRegionBandwidthBPS: 1_000_000,
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
		"qdisc replace dev s1 parent 1:22 handle 22: netem delay 0ms limit 1000",
		"class replace dev s1 parent 1: classid 1:32 htb rate 1000kbit ceil 1000kbit",
		"qdisc replace dev s1 parent 1:32 handle 32: netem delay 60ms limit 1000",
		"filter replace dev s1 protocol ip parent 1: prio 11 u32 match ip src 172.31.1.10/32 match ip dst 172.31.2.10/32 flowid 1:23",
		"filter replace dev s1 protocol ip parent 1: prio 14 u32 match ip src 172.31.2.10/32 match ip dst 172.31.1.10/32 flowid 1:32",
		"filter replace dev s1 protocol ip parent 1: prio 13 u32 match ip src 172.31.1.10/32 match ip dst 172.31.4.10/32 flowid 1:22",
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
	profile := domain.TrafficProfile{CrossRegionDelay: 200 * time.Millisecond, SameRegionBandwidthBPS: 1_000_000_000, CrossRegionBandwidthBPS: 200_000_000}
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
	profile := domain.TrafficProfile{SameRegionBandwidthBPS: 10_000, CrossRegionBandwidthBPS: 1_000, CrossRegionDelay: time.Millisecond}
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	batches, err := RenderTC(interfaces, testNetworks(), cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, batch := range batches {
		for _, source := range []domain.Region{domain.RegionUS, domain.RegionAP, domain.RegionEU} {
			for _, destination := range []domain.Region{domain.RegionUS, domain.RegionAP, domain.RegionEU} {
				classID := pairClassID(source, destination)
				wantRate := "10kbit"
				wantDelay := "0ms"
				if destination != source {
					wantRate = "1kbit"
					wantDelay = "500us"
				}
				if !strings.Contains(batch, "classid 1:"+classID+" htb rate "+wantRate) ||
					!strings.Contains(batch, "handle "+classID+": netem delay "+wantDelay+" ") {
					t.Errorf("source=%s destination=%s has wrong class\n%s", source, destination, batch)
				}
			}
		}
	}
}

func TestRenderTCRejectsUnsafeInterface(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.App1: "s1\" accept", domain.App2: "s2", domain.App3: "s3", domain.DB1: "s4"}
	cfg := domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
	profile := domain.TrafficProfile{SameRegionBandwidthBPS: 10_000, CrossRegionBandwidthBPS: 1_000}
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
