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

	Server1 = portalv1.ServerId_SERVER_ID_SERVER1
	Server2 = portalv1.ServerId_SERVER_ID_SERVER2
	Server3 = portalv1.ServerId_SERVER_ID_SERVER3
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
	Server1Region  Region
	Server2Region  Region
	Server3Region  Region
	MainServer     ServerID
	DefaultCountry string
}

func InitialConfig() Config {
	return Config{
		Server1Region:  RegionAP,
		Server2Region:  RegionAP,
		Server3Region:  RegionAP,
		MainServer:     Server1,
		DefaultCountry: "JP",
	}
}

func (c Config) Validate() error {
	if !ValidRegion(c.Server1Region) {
		return fmt.Errorf("server1 region is invalid")
	}
	if !ValidRegion(c.Server2Region) {
		return fmt.Errorf("server2 region is invalid")
	}
	if !ValidRegion(c.Server3Region) {
		return fmt.Errorf("server3 region is invalid")
	}
	if !ValidServer(c.MainServer) {
		return errors.New("main server is invalid")
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
	return server == Server1 || server == Server2 || server == Server3
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
		return portalv1.Region_REGION_UNSPECIFIED
	}
}

type CalculatedConfig struct {
	AppHostname          string
	Server1Hostname      string
	Server2Hostname      string
	Server3Hostname      string
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
		Server1Hostname:      "server1." + base,
		Server2Hostname:      "server2." + base,
		Server3Hostname:      "server3." + base,
		DefaultCountryRegion: countryByCode[cfg.DefaultCountry].Region,
	}
	switch cfg.MainServer {
	case Server1:
		calculated.TargetHostname = calculated.Server1Hostname
	case Server2:
		calculated.TargetHostname = calculated.Server2Hostname
	case Server3:
		calculated.TargetHostname = calculated.Server3Hostname
	}
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
