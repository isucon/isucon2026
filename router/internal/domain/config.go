package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"

	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
)

type Region string

const (
	RegionUS Region = "US"
	RegionAP Region = "AP"
	RegionEU Region = "EU"
)

func (r Region) Valid() bool {
	return r == RegionUS || r == RegionAP || r == RegionEU
}

type ServerID string

const (
	Server1 ServerID = "server1"
	Server2 ServerID = "server2"
	Server3 ServerID = "server3"
)

var serverIDs = []ServerID{Server1, Server2, Server3}

func ServerIDs() []ServerID {
	return slices.Clone(serverIDs)
}

func (s ServerID) Valid() bool {
	return s == Server1 || s == Server2 || s == Server3
}

var countryRegions = map[string]Region{
	"US": RegionUS,
	"CA": RegionUS,
	"MX": RegionUS,
	"JP": RegionAP,
	"KR": RegionAP,
	"CN": RegionAP,
	"AU": RegionAP,
	"GB": RegionEU,
	"DE": RegionEU,
	"FR": RegionEU,
}

func Countries() []string {
	countries := make([]string, 0, len(countryRegions))
	for code := range countryRegions {
		countries = append(countries, code)
	}
	slices.Sort(countries)
	return countries
}

func CountryRegion(code string) (Region, bool) {
	region, ok := countryRegions[code]
	return region, ok
}

type Config struct {
	Server1Region  Region
	Server2Region  Region
	Server3Region  Region
	MainServer     ServerID
	DefaultCountry string
}

func (c Config) Validate() error {
	if c.Server1Region != RegionAP || c.Server2Region != RegionEU || c.Server3Region != RegionUS {
		return errors.New("server regions are fixed: server1=AP, server2=EU, server3=US")
	}
	if !c.MainServer.Valid() {
		return errors.New("main server is invalid")
	}
	if _, ok := CountryRegion(c.DefaultCountry); !ok {
		return errors.New("default country is unsupported")
	}
	return nil
}

func (c Config) RegionFor(server ServerID) Region {
	switch server {
	case Server1:
		return c.Server1Region
	case Server2:
		return c.Server2Region
	case Server3:
		return c.Server3Region
	default:
		return ""
	}
}

func (c Config) Proto() *portalv1.Config {
	return &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			Server1: regionToProto(c.Server1Region),
			Server2: regionToProto(c.Server2Region),
			Server3: regionToProto(c.Server3Region),
		},
		MainServer:     serverToProto(c.MainServer),
		DefaultCountry: c.DefaultCountry,
	}
}

func ConfigFromProto(value *portalv1.Config) (Config, error) {
	if value == nil {
		return Config{}, errors.New("config is required")
	}
	if value.ServerRegions == nil {
		return Config{}, errors.New("server regions are required")
	}
	server1, err := regionFromProto(value.ServerRegions.Server1)
	if err != nil {
		return Config{}, fmt.Errorf("server1: %w", err)
	}
	server2, err := regionFromProto(value.ServerRegions.Server2)
	if err != nil {
		return Config{}, fmt.Errorf("server2: %w", err)
	}
	server3, err := regionFromProto(value.ServerRegions.Server3)
	if err != nil {
		return Config{}, fmt.Errorf("server3: %w", err)
	}
	mainServer, err := serverFromProto(value.MainServer)
	if err != nil {
		return Config{}, err
	}
	config := Config{
		Server1Region:  server1,
		Server2Region:  server2,
		Server3Region:  server3,
		MainServer:     mainServer,
		DefaultCountry: value.DefaultCountry,
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

type DesiredSnapshot struct {
	ID         string
	Config     Config
	IssuedAt   time.Time
	Generation uint64
}

func SnapshotFromProto(value *portalv1.ConfigSnapshot, generation uint64) (DesiredSnapshot, error) {
	if value == nil {
		return DesiredSnapshot{}, errors.New("desired snapshot is required")
	}
	if value.SnapshotId == "" {
		return DesiredSnapshot{}, errors.New("snapshot ID is required")
	}
	config, err := ConfigFromProto(value.Config)
	if err != nil {
		return DesiredSnapshot{}, fmt.Errorf("snapshot config: %w", err)
	}
	var issuedAt time.Time
	if value.IssuedAt != nil {
		if err := value.IssuedAt.CheckValid(); err != nil {
			return DesiredSnapshot{}, fmt.Errorf("issued at: %w", err)
		}
		issuedAt = value.IssuedAt.AsTime()
	}
	return DesiredSnapshot{ID: value.SnapshotId, Config: config, IssuedAt: issuedAt, Generation: generation}, nil
}

type ActiveSnapshot struct {
	DesiredSnapshot
	RDSVersion  string
	CommittedAt time.Time
}

type TrafficProfile struct {
	SameRegionDelay         time.Duration
	CrossRegionDelay        time.Duration
	SameRegionBandwidthBPS  uint64
	CrossRegionBandwidthBPS uint64
}

func (p TrafficProfile) Validate() error {
	if p.SameRegionDelay < 0 || p.CrossRegionDelay < 0 {
		return errors.New("traffic delay must not be negative")
	}
	if p.SameRegionBandwidthBPS == 0 || p.CrossRegionBandwidthBPS == 0 {
		return errors.New("traffic bandwidth must be positive")
	}
	if p.CrossRegionBandwidthBPS > p.SameRegionBandwidthBPS {
		return errors.New("cross-region bandwidth must not exceed same-region bandwidth")
	}
	return nil
}

func (p TrafficProfile) Class(source, destination Region) (delay time.Duration, bandwidthBPS uint64) {
	if source == destination {
		return p.SameRegionDelay, p.SameRegionBandwidthBPS
	}
	return p.CrossRegionDelay, p.CrossRegionBandwidthBPS
}

func regionFromProto(value portalv1.Region) (Region, error) {
	switch value {
	case portalv1.Region_REGION_US:
		return RegionUS, nil
	case portalv1.Region_REGION_AP:
		return RegionAP, nil
	case portalv1.Region_REGION_EU:
		return RegionEU, nil
	default:
		return "", fmt.Errorf("region %d is invalid", value)
	}
}

func regionToProto(value Region) portalv1.Region {
	switch value {
	case RegionUS:
		return portalv1.Region_REGION_US
	case RegionAP:
		return portalv1.Region_REGION_AP
	case RegionEU:
		return portalv1.Region_REGION_EU
	default:
		return portalv1.Region_REGION_UNSPECIFIED
	}
}

func serverFromProto(value portalv1.ServerId) (ServerID, error) {
	switch value {
	case portalv1.ServerId_SERVER_ID_SERVER1:
		return Server1, nil
	case portalv1.ServerId_SERVER_ID_SERVER2:
		return Server2, nil
	case portalv1.ServerId_SERVER_ID_SERVER3:
		return Server3, nil
	default:
		return "", fmt.Errorf("main server %d is invalid", value)
	}
}

func serverToProto(value ServerID) portalv1.ServerId {
	switch value {
	case Server1:
		return portalv1.ServerId_SERVER_ID_SERVER1
	case Server2:
		return portalv1.ServerId_SERVER_ID_SERVER2
	case Server3:
		return portalv1.ServerId_SERVER_ID_SERVER3
	default:
		return portalv1.ServerId_SERVER_ID_UNSPECIFIED
	}
}
