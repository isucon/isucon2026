package geo_test

import (
	"testing"

	"github.com/isucon/isucon2026/bench/internal/geo"
)

func TestCountriesRouteToTheirRegion(t *testing.T) {
	for _, region := range geo.Regions {
		for index := range 8 {
			country := geo.Country(region, index)
			if got := geo.RegionOf(country); got != region {
				t.Fatalf("Country(%s, %d) = %s routes to %q", region, index, country, got)
			}
		}
	}
	if geo.Country("mars", 0) != "" || geo.RegionOf("XX") != "" || geo.Valid("mars") {
		t.Fatal("unknown region or country accepted")
	}
	if geo.CountryCount(geo.Asia) != 4 || geo.CountryCount(geo.Europe) != 3 || geo.CountryCount(geo.America) != 3 || geo.CountryCount("mars") != 0 {
		t.Fatal("wrong number of countries")
	}
}
