package domain

import "testing"

func TestSupportedCountriesAndCalculation(t *testing.T) {
	countries := SupportedCountries()
	if len(countries) != 10 {
		t.Fatalf("got %d countries", len(countries))
	}
	for _, country := range countries {
		if err := ValidateCountryCode(country.Code); err != nil {
			t.Fatalf("%s: %v", country.Code, err)
		}
	}
	for _, invalid := range []string{"", "jp", "JPN", "XX", "AP", "EU"} {
		if ValidateCountryCode(invalid) == nil {
			t.Fatalf("%q must be rejected", invalid)
		}
	}
	calculated, err := Calculate(
		TeamKey{Number: 1},
		HostnameConfig{RandomHash: "abc123", DomainSuffix: "example.test"},
		InitialConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if calculated.AppHostname != "app.team1-abc123.example.test" ||
		calculated.TargetHostname != "server1.team1-abc123.example.test" ||
		calculated.DefaultCountryRegion != RegionAP {
		t.Fatalf("unexpected calculated config: %+v", calculated)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := InitialConfig()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := []Config{
		{Server1Region: 99, Server2Region: RegionEU, Server3Region: RegionUS, MainServer: Server1, DefaultCountry: "JP"},
		{Server1Region: RegionAP, Server2Region: RegionEU, Server3Region: RegionUS, MainServer: 99, DefaultCountry: "JP"},
		{Server1Region: RegionAP, Server2Region: RegionEU, Server3Region: RegionUS, MainServer: Server1, DefaultCountry: "XX"},
		{Server1Region: RegionAP, Server2Region: RegionAP, Server3Region: RegionAP, MainServer: Server1, DefaultCountry: "JP"},
		{Server1Region: RegionAP, Server2Region: RegionUS, Server3Region: RegionEU, MainServer: Server1, DefaultCountry: "JP"},
	}
	for _, cfg := range invalid {
		if cfg.Validate() == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
