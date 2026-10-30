package domain

import (
	"errors"
	"fmt"
	"regexp"

	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
)

type Region = portalv1.Region
type ServerID = portalv1.ServerId

const (
	RegionUS = portalv1.Region_REGION_US
	RegionAP = portalv1.Region_REGION_AP
	RegionEU = portalv1.Region_REGION_EU

	App1 = portalv1.ServerId_SERVER_ID_APP1
	App2 = portalv1.ServerId_SERVER_ID_APP2
	App3 = portalv1.ServerId_SERVER_ID_APP3
	DB1  = portalv1.ServerId_SERVER_ID_DB1
)

type TeamKey struct {
	Number uint32
}

func (t TeamKey) Validate() error {
	if t.Number == 0 {
		return errors.New("team number must be positive")
	}
	return nil
}

type HostnameConfig struct {
	RandomHash   string
	DomainSuffix string
}

func (h HostnameConfig) Validate() error {
	if h.RandomHash == "" {
		return errors.New("team hostname hash must not be empty")
	}
	if h.DomainSuffix == "" {
		return errors.New("domain suffix must not be empty")
	}
	return nil
}

type Config struct {
	App1Region     Region
	App2Region     Region
	App3Region     Region
	DefaultCountry string
}

func InitialConfig() Config {
	return Config{
		App1Region:     RegionAP,
		App2Region:     RegionEU,
		App3Region:     RegionUS,
		DefaultCountry: "JP",
	}
}

func (c Config) Validate() error {
	if c.App1Region != RegionAP || c.App2Region != RegionEU || c.App3Region != RegionUS {
		return errors.New("server regions are fixed: app1=AP, app2=EU, app3=US")
	}
	if err := ValidateCountryCode(c.DefaultCountry); err != nil {
		return fmt.Errorf("default country: %w", err)
	}
	return nil
}

func ValidRegion(region Region) bool {
	return region == RegionUS || region == RegionAP || region == RegionEU
}

func ValidServer(server ServerID) bool {
	return server == App1 || server == App2 || server == App3
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
		return portalv1.Region_REGION_UNSPECIFIED
	}
}

type CalculatedConfig struct {
	AppHostname          string
	App1Hostname         string
	App2Hostname         string
	App3Hostname         string
	TargetHostname       string
	DefaultCountryRegion Region
}

func Calculate(team TeamKey, host HostnameConfig, cfg Config) (CalculatedConfig, error) {
	if err := team.Validate(); err != nil {
		return CalculatedConfig{}, err
	}
	if err := host.Validate(); err != nil {
		return CalculatedConfig{}, err
	}
	if err := cfg.Validate(); err != nil {
		return CalculatedConfig{}, err
	}
	base := fmt.Sprintf("team%d-%s.%s", team.Number, host.RandomHash, host.DomainSuffix)
	calculated := CalculatedConfig{
		AppHostname:          "app." + base,
		App1Hostname:         "asia." + base,
		App2Hostname:         "europe." + base,
		App3Hostname:         "america." + base,
		DefaultCountryRegion: countryByCode[cfg.DefaultCountry].Region,
	}
	calculated.TargetHostname = calculated.AppHostname
	return calculated, nil
}

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

func ValidateCountryCode(code string) error {
	if !countryCodePattern.MatchString(code) {
		return errors.New("country code must be two uppercase ASCII letters")
	}
	if _, ok := countryByCode[code]; !ok {
		return errors.New("country code is not supported")
	}
	return nil
}
