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
	App1 ServerID = "app1"
	App2 ServerID = "app2"
	App3 ServerID = "app3"
	DB1  ServerID = "db1"
)

var serverIDs = []ServerID{App1, App2, App3, DB1}

func ServerIDs() []ServerID {
	return slices.Clone(serverIDs)
}

func (s ServerID) Valid() bool {
	return s == App1 || s == App2 || s == App3 || s == DB1
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
	App1Region     Region
	App2Region     Region
	App3Region     Region
	DefaultCountry string
}

func (c Config) Validate() error {
	if c.App1Region != RegionAP || c.App2Region != RegionEU || c.App3Region != RegionUS {
		return errors.New("server regions are fixed: app1=AP, app2=EU, app3=US")
	}
	if _, ok := CountryRegion(c.DefaultCountry); !ok {
		return errors.New("default country is unsupported")
	}
	return nil
}

func (c Config) RegionFor(server ServerID) Region {
	switch server {
	case App1:
		return c.App1Region
	case App2:
		return c.App2Region
	case App3:
		return c.App3Region
	case DB1:
		return RegionAP
	default:
		return ""
	}
}

// ServerForRegion returns the server in the client's region.
func ServerForRegion(region Region) ServerID {
	switch region {
	case RegionAP:
		return App1
	case RegionEU:
		return App2
	case RegionUS:
		return App3
	default:
		return ""
	}
}

func (c Config) Proto() *portalv1.Config {
	return &portalv1.Config{
		ServerRegions: &portalv1.ServerRegions{
			App1: regionToProto(c.App1Region),
			App2: regionToProto(c.App2Region),
			App3: regionToProto(c.App3Region),
			Db1:  portalv1.Region_REGION_AP,
		},
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
	if value.ServerRegions.Db1 != portalv1.Region_REGION_AP {
		return Config{}, errors.New("db1 region is fixed: AP")
	}
	app1, err := regionFromProto(value.ServerRegions.App1)
	if err != nil {
		return Config{}, fmt.Errorf("app1: %w", err)
	}
	app2, err := regionFromProto(value.ServerRegions.App2)
	if err != nil {
		return Config{}, fmt.Errorf("app2: %w", err)
	}
	app3, err := regionFromProto(value.ServerRegions.App3)
	if err != nil {
		return Config{}, fmt.Errorf("app3: %w", err)
	}
	config := Config{
		App1Region:     app1,
		App2Region:     app2,
		App3Region:     app3,
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

type TrafficClass struct {
	Delay        time.Duration
	BandwidthBPS uint64
}

// TrafficProfile has one class per unordered region pair, so both directions
// between two regions are shaped alike.
type TrafficProfile struct {
	SameRegion TrafficClass
	APEU       TrafficClass
	EUUS       TrafficClass
	APUS       TrafficClass
}

func (p TrafficProfile) Validate() error {
	for _, class := range []struct {
		name string
		TrafficClass
	}{
		{"same-region", p.SameRegion},
		{"AP-EU", p.APEU},
		{"EU-US", p.EUUS},
		{"AP-US", p.APUS},
	} {
		if class.Delay < 0 {
			return fmt.Errorf("%s traffic delay must not be negative", class.name)
		}
		if class.BandwidthBPS == 0 {
			return fmt.Errorf("%s traffic bandwidth must be positive", class.name)
		}
		if class.BandwidthBPS > p.SameRegion.BandwidthBPS {
			return fmt.Errorf("%s bandwidth must not exceed same-region bandwidth", class.name)
		}
	}
	return nil
}

func (p TrafficProfile) Class(source, destination Region) TrafficClass {
	between := func(left, right Region) bool {
		return (source == left && destination == right) || (source == right && destination == left)
	}
	switch {
	case source == destination:
		return p.SameRegion
	case between(RegionAP, RegionEU):
		return p.APEU
	case between(RegionEU, RegionUS):
		return p.EUUS
	case between(RegionAP, RegionUS):
		return p.APUS
	default:
		return TrafficClass{}
	}
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
