package xds

import (
	"os"
	"strings"
	"testing"
)

func TestBootstrapPinsStaticBoundary(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("../../envoy/bootstrap.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	required := []string{
		"id: isureal-router-envoy",
		"route_config_name: isureal_routes",
		"ads: {}",
		"envoy.filters.http.fault",
		"envoy.filters.http.bandwidth_limit",
		"envoy.filters.http.router",
		"internal_address_config:",
		"address_prefix: 172.16.0.0",
		"address: 127.0.0.1",
		"name: app1",
		"name: app2",
		"name: app3",
		"address: 172.31.1.10",
		"address: 172.31.2.10",
		"address: 172.31.3.10",
	}
	for _, value := range required {
		if !strings.Contains(text, value) {
			t.Errorf("bootstrap missing %q", value)
		}
	}
	if strings.Contains(text, "cds_config:") || strings.Contains(text, "lds_config:") {
		t.Fatal("bootstrap must keep listeners and upstream clusters static")
	}
}
