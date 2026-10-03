package netcontrol

import (
	"fmt"
	"strings"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

func RenderTC(interfaces map[domain.ServerID]string, networks map[domain.ServerID]config.ServerNetwork, routerConfig domain.Config, profile domain.TrafficProfile) (map[domain.ServerID]string, error) {
	if err := validateInterfaces(interfaces); err != nil {
		return nil, err
	}
	if err := routerConfig.Validate(); err != nil {
		return nil, err
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if (profile.SameRegionDelay > 0 && profile.SameRegionDelay < time.Millisecond) || (profile.CrossRegionDelay > 0 && profile.CrossRegionDelay < time.Millisecond) {
		return nil, fmt.Errorf("configured round-trip delay must be zero or at least 1ms")
	}
	for _, server := range domain.ServerIDs() {
		network, ok := networks[server]
		if !ok || !network.ServerIP.Is4() {
			return nil, fmt.Errorf("%s network is missing or invalid", server)
		}
	}

	batches := make(map[domain.ServerID]string, len(domain.ServerIDs()))
	for _, destination := range domain.ServerIDs() {
		device := interfaces[destination]
		destinationRegion := routerConfig.RegionFor(destination)
		var builder strings.Builder
		fmt.Fprintf(&builder, "qdisc replace dev %s root handle 1: htb default 10\n", device)
		fmt.Fprintf(&builder, "class replace dev %s parent 1: classid 1:10 htb rate %s ceil %s\n", device, rateKbit(profile.SameRegionBandwidthBPS), rateKbit(profile.SameRegionBandwidthBPS))
		fmt.Fprintf(&builder, "qdisc replace dev %s parent 1:10 handle 10: netem delay %s\n", device, delayString(oneWayDelay(profile.SameRegionDelay)))
		for _, sourceRegion := range []domain.Region{domain.RegionUS, domain.RegionAP, domain.RegionEU} {
			delay, bandwidth := profile.Class(sourceRegion, destinationRegion)
			classID := regionClassID(sourceRegion)
			fmt.Fprintf(&builder, "class replace dev %s parent 1: classid 1:%s htb rate %s ceil %s\n", device, classID, rateKbit(bandwidth), rateKbit(bandwidth))
			fmt.Fprintf(&builder, "qdisc replace dev %s parent 1:%s handle %s: netem delay %s\n", device, classID, classID, delayString(oneWayDelay(delay)))
		}
		for index, source := range domain.ServerIDs() {
			classID := regionClassID(routerConfig.RegionFor(source))
			fmt.Fprintf(&builder, "filter replace dev %s protocol ip parent 1: prio %d u32 match ip src %s/32 flowid 1:%s\n", device, 11+index, networks[source].ServerIP, classID)
		}
		batches[destination] = builder.String()
	}
	return batches, nil
}

func regionClassID(region domain.Region) string {
	switch region {
	case domain.RegionUS:
		return "11"
	case domain.RegionAP:
		return "12"
	case domain.RegionEU:
		return "13"
	default:
		return "0"
	}
}

func rateKbit(bitsPerSecond uint64) string {
	kbit := (bitsPerSecond + 999) / 1000
	return fmt.Sprintf("%dkbit", kbit)
}

// TrafficProfile delays are end-to-end round-trip budgets. Server-to-server
// packets cross a destination-side netem qdisc in each direction, so each
// qdisc contributes half of the configured delay.
func oneWayDelay(roundTripDelay time.Duration) time.Duration {
	return roundTripDelay / 2
}

func delayString(delay time.Duration) string {
	if delay%time.Millisecond != 0 {
		microseconds := (delay + time.Microsecond - 1) / time.Microsecond
		return fmt.Sprintf("%dus", microseconds)
	}
	return fmt.Sprintf("%dms", delay.Milliseconds())
}
