package xds

import (
	"testing"
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	bandwidthv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/bandwidth_limit/v3"
	faultv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/fault/v3"
	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

func TestBuildRoutesMatrix(t *testing.T) {
	t.Parallel()
	hosts := testHosts()
	cfg := domain.Config{
		Server1Region:  domain.RegionAP,
		Server2Region:  domain.RegionEU,
		Server3Region:  domain.RegionUS,
		DefaultCountry: "JP",
	}
	profile := domain.TrafficProfile{
		SameRegionDelay: 0, CrossRegionDelay: 125 * time.Millisecond,
		SameRegionBandwidthBPS: 80_000_000, CrossRegionBandwidthBPS: 800_000,
	}
	routes, err := BuildRoutes("isureal_routes", hosts, cfg, profile)
	if err != nil {
		t.Fatal(err)
	}
	if routes.Name != "isureal_routes" || len(routes.VirtualHosts) != 5 {
		t.Fatalf("unexpected route config: name=%q vhosts=%d", routes.Name, len(routes.VirtualHosts))
	}
	if !routes.IgnorePortInHostMatching {
		t.Fatal("route config must ignore the request port when matching virtual hosts")
	}

	app := virtualHost(t, routes, hosts.App)
	if len(app.Routes) != len(domain.Countries())+2 {
		t.Fatalf("app routes = %d", len(app.Routes))
	}
	for country, server := range map[string]string{
		"JP": "server1", "KR": "server1", "CN": "server1", "AU": "server1",
		"US": "server3", "CA": "server3", "MX": "server3",
		"GB": "server2", "DE": "server2", "FR": "server2",
	} {
		assertCountryRoute(t, app.Routes[routeIndex(t, country)], country, server, 0, profile.SameRegionBandwidthBPS)
		cfg.DefaultCountry = country
		defaultRoutes, err := BuildRoutes("defaults", hosts, cfg, profile)
		if err != nil {
			t.Fatal(err)
		}
		fallback := virtualHost(t, defaultRoutes, hosts.App).Routes[len(domain.Countries())]
		if fallback.GetRoute().GetCluster() != server || fallback.RequestHeadersToAdd[0].GetHeader().GetValue() != country {
			t.Fatalf("default country %s did not select %s", country, server)
		}
	}

	server2 := virtualHost(t, routes, hosts.Server2)
	assertCountryRoute(t, server2.Routes[routeIndex(t, "DE")], "DE", "server2", 0, profile.SameRegionBandwidthBPS)
	assertCountryRoute(t, server2.Routes[routeIndex(t, "JP")], "JP", "server2", profile.CrossRegionDelay, profile.CrossRegionBandwidthBPS)

	absent := app.Routes[len(domain.Countries())]
	if absent.GetRoute().GetCluster() != "server1" {
		t.Fatalf("default route cluster = %q", absent.GetRoute().GetCluster())
	}
	if len(absent.Match.Headers) != 1 || !absent.Match.Headers[0].GetPresentMatch() || !absent.Match.Headers[0].InvertMatch {
		t.Fatalf("default route does not match header absence: %+v", absent.Match.Headers)
	}
	if len(absent.RequestHeadersToAdd) != 1 || absent.RequestHeadersToAdd[0].GetHeader().GetValue() != "JP" {
		t.Fatalf("default country was not inserted: %+v", absent.RequestHeadersToAdd)
	}

	invalid := app.Routes[len(domain.Countries())+1]
	if invalid.GetDirectResponse().GetStatus() != 400 {
		t.Fatalf("invalid country status = %d", invalid.GetDirectResponse().GetStatus())
	}
	unknown := routes.VirtualHosts[len(routes.VirtualHosts)-1]
	if len(unknown.Domains) != 1 || unknown.Domains[0] != "*" || unknown.Routes[0].GetDirectResponse().GetStatus() != 404 {
		t.Fatalf("unexpected unknown-host route: %+v", unknown)
	}
}

func TestBuildRoutesRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	validConfig := domain.Config{Server1Region: domain.RegionAP, Server2Region: domain.RegionEU, Server3Region: domain.RegionUS, DefaultCountry: "JP"}
	validProfile := domain.TrafficProfile{SameRegionBandwidthBPS: 1000, CrossRegionBandwidthBPS: 100}
	if _, err := BuildRoutes("", testHosts(), validConfig, validProfile); err == nil {
		t.Fatal("empty route name unexpectedly succeeded")
	}
	hosts := testHosts()
	hosts.Server2 = hosts.Server1
	if _, err := BuildRoutes("routes", hosts, validConfig, validProfile); err == nil {
		t.Fatal("duplicate host unexpectedly succeeded")
	}
}

func virtualHost(t *testing.T, routes *routev3.RouteConfiguration, domainName string) *routev3.VirtualHost {
	t.Helper()
	for _, host := range routes.VirtualHosts {
		if len(host.Domains) == 1 && host.Domains[0] == domainName {
			return host
		}
	}
	t.Fatalf("virtual host %q not found", domainName)
	return nil
}

func routeIndex(t *testing.T, country string) int {
	t.Helper()
	for index, code := range domain.Countries() {
		if code == country {
			return index
		}
	}
	t.Fatalf("country %q not found", country)
	return -1
}

func assertCountryRoute(t *testing.T, route *routev3.Route, country, cluster string, delay time.Duration, bandwidthBPS uint64) {
	t.Helper()
	if len(route.Match.Headers) != 1 || route.Match.Headers[0].Name != "x-country-code" || route.Match.Headers[0].GetStringMatch().GetExact() != country {
		t.Fatalf("unexpected country matcher: %+v", route.Match.Headers)
	}
	if route.GetRoute().GetCluster() != cluster {
		t.Fatalf("cluster = %q, want %q", route.GetRoute().GetCluster(), cluster)
	}
	bandwidth := new(bandwidthv3.BandwidthLimit)
	if err := route.TypedPerFilterConfig[bandwidthFilterName].UnmarshalTo(bandwidth); err != nil {
		t.Fatal(err)
	}
	wantKiB := (bandwidthBPS + 8191) / 8192
	if bandwidth.GetEnableMode() != bandwidthv3.BandwidthLimit_REQUEST_AND_RESPONSE || bandwidth.GetLimitKbps().GetValue() != wantKiB {
		t.Fatalf("unexpected bandwidth config: %+v", bandwidth)
	}
	if delay == 0 {
		if _, ok := route.TypedPerFilterConfig[faultFilterName]; ok {
			t.Fatal("same-region route unexpectedly has fault override")
		}
		return
	}
	fault := new(faultv3.HTTPFault)
	if err := route.TypedPerFilterConfig[faultFilterName].UnmarshalTo(fault); err != nil {
		t.Fatal(err)
	}
	if fault.GetDelay().GetFixedDelay().AsDuration() != delay || fault.GetDelay().GetPercentage().GetNumerator() != 100 {
		t.Fatalf("unexpected fault config: %+v", fault)
	}
}

func testHosts() config.Hostnames {
	return config.Hostnames{
		App: "app.team1.test", Server1: "server1.team1.test", Server2: "server2.team1.test", Server3: "server3.team1.test",
	}
}
