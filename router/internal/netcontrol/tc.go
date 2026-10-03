package netcontrol

import (
	"fmt"
	"strings"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

// RenderTC returns one tc batch per data interface. Server-to-server packets are
// classified by their source and destination addresses, so the same rules work
// whether every server has a dedicated router interface or all servers share a
// single interface that the VPC route tables hairpin through the router.
func RenderTC(interfaces map[domain.ServerID]string, networks map[domain.ServerID]config.ServerNetwork, routerConfig domain.Config, profile domain.TrafficProfile) (map[string]string, error) {
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
	if err := validateNetworks(networks); err != nil {
		return nil, err
	}

	regions := []domain.Region{domain.RegionUS, domain.RegionAP, domain.RegionEU}
	batches := make(map[string]string)
	for _, device := range devices(interfaces) {
		var builder strings.Builder
		// Traffic that does not match a server pair, such as Envoy forwarding to
		// an application, uses the same-region class.
		fmt.Fprintf(&builder, "qdisc replace dev %s root handle 1: htb default 10\n", device)
		writeClass(&builder, device, "10", profile.SameRegionDelay, profile.SameRegionBandwidthBPS)
		for _, sourceRegion := range regions {
			for _, destinationRegion := range regions {
				delay, bandwidth := profile.Class(sourceRegion, destinationRegion)
				writeClass(&builder, device, pairClassID(sourceRegion, destinationRegion), delay, bandwidth)
			}
		}
		priority := 11
		for _, source := range domain.ServerIDs() {
			for _, destination := range domain.ServerIDs() {
				if source == destination {
					continue
				}
				classID := pairClassID(routerConfig.RegionFor(source), routerConfig.RegionFor(destination))
				fmt.Fprintf(&builder, "filter replace dev %s protocol ip parent 1: prio %d u32 match ip src %s/32 match ip dst %s/32 flowid 1:%s\n", device, priority, networks[source].ServerIP, networks[destination].ServerIP, classID)
				priority++
			}
		}
		batches[device] = builder.String()
	}
	return batches, nil
}

func writeClass(builder *strings.Builder, device, classID string, roundTripDelay time.Duration, bandwidthBPS uint64) {
	delay := oneWayDelay(roundTripDelay)
	fmt.Fprintf(builder, "class replace dev %s parent 1: classid 1:%s htb rate %s ceil %s\n", device, classID, rateKbit(bandwidthBPS), rateKbit(bandwidthBPS))
	fmt.Fprintf(builder, "qdisc replace dev %s parent 1:%s handle %s: netem delay %s limit %d\n", device, classID, classID, delayString(delay), netemLimit(delay, bandwidthBPS))
}

func pairClassID(source, destination domain.Region) string {
	return regionDigit(source) + regionDigit(destination)
}

func regionDigit(region domain.Region) string {
	switch region {
	case domain.RegionUS:
		return "1"
	case domain.RegionAP:
		return "2"
	case domain.RegionEU:
		return "3"
	default:
		return "0"
	}
}

// netem holds every delayed packet in its queue. The default limit of 1000
// packets cannot cover the bandwidth-delay product of a 200Mbps, 100ms link, so
// size the queue for twice the product of full-sized packets.
func netemLimit(oneWayDelay time.Duration, bitsPerSecond uint64) uint64 {
	const defaultLimit = 1000
	const packetBits = 1500 * 8
	inFlight := uint64(oneWayDelay.Seconds()*float64(bitsPerSecond)) / packetBits
	return max(defaultLimit, 2*inFlight)
}

func devices(interfaces map[domain.ServerID]string) []string {
	seen := make(map[string]struct{}, len(interfaces))
	var names []string
	for _, server := range domain.ServerIDs() {
		name := interfaces[server]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
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
