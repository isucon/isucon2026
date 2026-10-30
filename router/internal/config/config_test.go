package config

import (
	"testing"
	"time"

	"github.com/isucon/isucon2026/router/internal/domain"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TeamNumber != 1 || cfg.PortalEndpoint != "http://portal:8080" {
		t.Fatalf("unexpected portal defaults: %+v", cfg)
	}
	crossRegion := domain.TrafficClass{Delay: 100 * time.Millisecond, BandwidthBPS: 200_000_000}
	wantTraffic := domain.TrafficProfile{
		SameRegion: domain.TrafficClass{BandwidthBPS: 1_000_000_000},
		APEU:       crossRegion, EUUS: crossRegion, APUS: crossRegion,
	}
	if cfg.Traffic != wantTraffic {
		t.Fatalf("unexpected traffic defaults: %+v", cfg.Traffic)
	}
	if cfg.Servers[domain.App1].RouterIP.String() != "172.31.1.2" || cfg.Servers[domain.App3].ServerIP.String() != "172.31.3.10" {
		t.Fatalf("unexpected topology: %+v", cfg.Servers)
	}
	if cfg.HTTPListenAddress != "0.0.0.0:8080" || cfg.HTTPSListenAddress != "" || cfg.UpstreamPort != 8080 ||
		cfg.HAProxyDir != "/run/isureal-haproxy" || cfg.HAProxyRuntimeAddress != "127.0.0.1:18001" {
		t.Fatalf("unexpected proxy defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"ROUTER_TEAM_NUMBER":                  "9",
		"ROUTER_PORTAL_ENDPOINT":              "http://portal-control:9000",
		"ROUTER_SAME_REGION_BANDWIDTH_BPS":    "100000000",
		"ROUTER_AP_EU_DELAY":                  "230ms",
		"ROUTER_AP_EU_BANDWIDTH_BPS":          "1000000",
		"ROUTER_EU_US_DELAY":                  "90ms",
		"ROUTER_EU_US_BANDWIDTH_BPS":          "2000000",
		"ROUTER_AP_US_DELAY":                  "150ms",
		"ROUTER_AP_US_BANDWIDTH_BPS":          "3000000",
		"ROUTER_APP2_REGION_LOCAL_IP":         "10.42.2.2",
		"ROUTER_APP2_IP":                      "10.42.2.10",
		"ROUTER_APP2_SUBNET":                  "10.42.2.0/24",
		"ROUTER_COMMAND_TIMEOUT":              "7s",
		"ROUTER_HTTPS_LISTEN_ADDRESS":         "0.0.0.0:443",
		"ROUTER_TLS_CERTIFICATE":              "/etc/isucon/haproxy/tls/server.pem",
		"ROUTER_UPSTREAM_PORT":                "80",
		"ROUTER_PORTAL_RECONNECT_MIN_BACKOFF": "20ms",
		"ROUTER_PORTAL_RECONNECT_MAX_BACKOFF": "2s",
	}
	cfg, err := Load(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TeamNumber != 9 || cfg.CommandTimeout != 7*time.Second || cfg.UpstreamPort != 80 ||
		cfg.HTTPSListenAddress != "0.0.0.0:443" || cfg.TLSCertificate != "/etc/isucon/haproxy/tls/server.pem" {
		t.Fatalf("overrides not loaded: %+v", cfg)
	}
	wantTraffic := domain.TrafficProfile{
		SameRegion: domain.TrafficClass{BandwidthBPS: 100_000_000},
		APEU:       domain.TrafficClass{Delay: 230 * time.Millisecond, BandwidthBPS: 1_000_000},
		EUUS:       domain.TrafficClass{Delay: 90 * time.Millisecond, BandwidthBPS: 2_000_000},
		APUS:       domain.TrafficClass{Delay: 150 * time.Millisecond, BandwidthBPS: 3_000_000},
	}
	if cfg.Traffic != wantTraffic {
		t.Fatalf("traffic overrides not loaded: %+v", cfg.Traffic)
	}
	if got := cfg.Servers[domain.App2].RouterIP.String(); got != "10.42.2.2" {
		t.Fatalf("app2 router IP = %s", got)
	}
}

func TestLoadAcceptsSharedRouterAddressOutsideServerSubnets(t *testing.T) {
	t.Parallel()
	values := map[string]string{}
	for _, prefix := range []string{"ROUTER_APP1", "ROUTER_APP2", "ROUTER_APP3", "ROUTER_DB1"} {
		values[prefix+"_REGION_LOCAL_IP"] = "192.168.10.10"
	}
	cfg, err := Load(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers[domain.DB1].RouterIP.String(); got != "192.168.10.10" {
		t.Fatalf("db1 router IP = %s", got)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	tests := map[string]map[string]string{
		"zero team":         {"ROUTER_TEAM_NUMBER": "0"},
		"bad endpoint":      {"ROUTER_PORTAL_ENDPOINT": "://bad"},
		"cross over same":   {"ROUTER_SAME_REGION_BANDWIDTH_BPS": "100", "ROUTER_AP_EU_BANDWIDTH_BPS": "100", "ROUTER_EU_US_BANDWIDTH_BPS": "100", "ROUTER_AP_US_BANDWIDTH_BPS": "101"},
		"zero bandwidth":    {"ROUTER_EU_US_BANDWIDTH_BPS": "0"},
		"negative delay":    {"ROUTER_AP_US_DELAY": "-1ms"},
		"bad delay":         {"ROUTER_AP_EU_DELAY": "200"},
		"overlap subnet":    {"ROUTER_APP2_SUBNET": "172.31.1.0/24", "ROUTER_APP2_REGION_LOCAL_IP": "172.31.1.3", "ROUTER_APP2_IP": "172.31.1.11"},
		"server outside":    {"ROUTER_APP1_IP": "192.0.2.1"},
		"duplicate address": {"ROUTER_APP2_REGION_LOCAL_IP": "172.31.2.10"},
		"bad backoff":       {"ROUTER_PORTAL_RECONNECT_MIN_BACKOFF": "3s", "ROUTER_PORTAL_RECONNECT_MAX_BACKOFF": "1s"},
		"https without key": {"ROUTER_HTTPS_LISTEN_ADDRESS": "0.0.0.0:443"},
		"zero port":         {"ROUTER_UPSTREAM_PORT": "0"},
		"large port":        {"ROUTER_UPSTREAM_PORT": "65536"},
		"relative dir":      {"ROUTER_HAPROXY_DIR": "haproxy"},
		"bad runtime":       {"ROUTER_HAPROXY_RUNTIME_ADDRESS": "localhost"},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
			if err == nil {
				t.Fatal("Load unexpectedly succeeded")
			}
		})
	}
}
