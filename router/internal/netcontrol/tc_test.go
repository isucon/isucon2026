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

func TestRenderTCClassifiesSourcesByRegion(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.Server1: "s1", domain.Server2: "s2", domain.Server3: "s3"}
	networks := testNetworks()
	cfg := domain.Config{
		Server1Region: domain.RegionAP,
		Server2Region: domain.RegionEU,
		Server3Region: domain.RegionUS,
		MainServer:    domain.Server1, DefaultCountry: "JP",
	}
	profile := domain.TrafficProfile{
		SameRegionDelay: 0, CrossRegionDelay: 120 * time.Millisecond,
		SameRegionBandwidthBPS: 100_000_000, CrossRegionBandwidthBPS: 1_000_000,
	}
	batches, err := RenderTC(interfaces, networks, cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	server1 := batches[domain.Server1]
	for _, required := range []string{
		"qdisc replace dev s1 root handle 1: htb default 10",
		"class replace dev s1 parent 1: classid 1:12 htb rate 100000kbit ceil 100000kbit",
		"qdisc replace dev s1 parent 1:12 handle 12: netem delay 0ms",
		"class replace dev s1 parent 1: classid 1:13 htb rate 1000kbit ceil 1000kbit",
		"qdisc replace dev s1 parent 1:13 handle 13: netem delay 60ms",
		"filter replace dev s1 protocol ip parent 1: prio 11 u32 match ip src 172.31.1.10/32 flowid 1:12",
		"filter replace dev s1 protocol ip parent 1: prio 12 u32 match ip src 172.31.2.10/32 flowid 1:13",
		"filter replace dev s1 protocol ip parent 1: prio 13 u32 match ip src 172.31.3.10/32 flowid 1:11",
	} {
		if !strings.Contains(server1, required) {
			t.Errorf("server1 batch missing %q\n%s", required, server1)
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

func TestRenderTCAllRegionCombinations(t *testing.T) {
	t.Parallel()
	interfaces := map[domain.ServerID]string{domain.Server1: "s1", domain.Server2: "s2", domain.Server3: "s3"}
	profile := domain.TrafficProfile{SameRegionBandwidthBPS: 10_000, CrossRegionBandwidthBPS: 1_000, CrossRegionDelay: time.Millisecond}
	cfg := domain.Config{Server1Region: domain.RegionAP, Server2Region: domain.RegionEU, Server3Region: domain.RegionUS, MainServer: domain.Server1, DefaultCountry: "JP"}
	for _, destinationServer := range domain.ServerIDs() {
		for _, sourceServer := range domain.ServerIDs() {
			destination, source := cfg.RegionFor(destinationServer), cfg.RegionFor(sourceServer)
			batches, err := RenderTC(interfaces, testNetworks(), cfg, profile)
			if err != nil {
				t.Fatalf("destination=%s source=%s: %v", destination, source, err)
			}
			classID := regionClassID(source)
			wantRate := "10kbit"
			wantDelay := "0ms"
			if destination != source {
				wantRate = "1kbit"
				wantDelay = "500us"
			}
			if !strings.Contains(batches[destinationServer], "classid 1:"+classID+" htb rate "+wantRate) ||
				!strings.Contains(batches[destinationServer], "handle "+classID+": netem delay "+wantDelay) {
				t.Errorf("destination=%s source=%s has wrong class\n%s", destination, source, batches[destinationServer])
			}
		}
	}
}

func testNetworks() map[domain.ServerID]config.ServerNetwork {
	return map[domain.ServerID]config.ServerNetwork{
		domain.Server1: {Subnet: netip.MustParsePrefix("172.31.1.0/24"), RouterIP: netip.MustParseAddr("172.31.1.2"), ServerIP: netip.MustParseAddr("172.31.1.10")},
		domain.Server2: {Subnet: netip.MustParsePrefix("172.31.2.0/24"), RouterIP: netip.MustParseAddr("172.31.2.2"), ServerIP: netip.MustParseAddr("172.31.2.10")},
		domain.Server3: {Subnet: netip.MustParsePrefix("172.31.3.0/24"), RouterIP: netip.MustParseAddr("172.31.3.2"), ServerIP: netip.MustParseAddr("172.31.3.10")},
	}
}
