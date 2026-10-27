package haproxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
)

func testConfig(t *testing.T, values map[string]string) config.Config {
	t.Helper()
	cfg, err := config.Load(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRenderRouteMatrix(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, map[string]string{
		"ROUTER_HAPROXY_DIR":               "/run/test-haproxy",
		"ROUTER_SAME_REGION_BANDWIDTH_BPS": "80000000",
		"ROUTER_AP_EU_DELAY":               "125ms",
		"ROUTER_AP_EU_BANDWIDTH_BPS":       "800000",
		"ROUTER_EU_US_DELAY":               "90ms",
		"ROUTER_EU_US_BANDWIDTH_BPS":       "1600000",
		"ROUTER_AP_US_DELAY":               "150ms",
		"ROUTER_AP_US_BANDWIDTH_BPS":       "800000",
	})
	files, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if files[DefaultCountryMap] != "" || !strings.Contains(files[LuaFile], "core.msleep") {
		t.Fatalf("unexpected side files: %q", files)
	}
	text := files[ConfigFile]
	for _, want := range []string{
		"stats socket ipv4@127.0.0.1:18001 level admin",
		"lua-load /run/test-haproxy/isureal.lua",
		"bind 0.0.0.0:8080",
		// Each bandwidth gets one filter per direction; routes with the same
		// bandwidth share the filter but not the bucket, which is per route.
		"filter bwlim-in up_800000 limit 100000 key var(txn.route) table bandwidth",
		"filter bwlim-out down_800000 limit 100000 key var(txn.route) table bandwidth",
		"filter bwlim-in up_80000000 limit 10000000 key var(txn.route) table bandwidth",
		"str(default),map(/run/test-haproxy/default-country.map)",
		"http-request set-var(txn.target) str(app2) if { var(txn.host) -m str app2.team1-abc123.isucon2026.westens.tokyo }",
		"http-request set-var(txn.pair) var(txn.target),concat(/AP) if { var(txn.country) -m str AU CN JP KR }",
		"http-request lua.isureal_delay 90 if { var(txn.pair) -m str app2/US app3/EU }",
		"http-request lua.isureal_delay 125 if { var(txn.pair) -m str app1/EU app2/AP }",
		"http-request lua.isureal_delay 150 if { var(txn.pair) -m str app1/US app3/AP }",
		"http-request set-bandwidth-limit down_800000 if { var(txn.pair) -m str app1/EU app1/US app2/AP app3/AP }",
		"http-request set-bandwidth-limit up_1600000 if { var(txn.pair) -m str app2/US app3/EU }",
		"http-request set-bandwidth-limit up_80000000 if { var(txn.pair) -m str app/AP app/EU app/US app1/AP app2/EU app3/US }",
		"use_backend app1 if { var(txn.pair) -m str app/AP app1/AP app1/EU app1/US }",
		"use_backend app3 if { var(txn.pair) -m str app/US app3/AP app3/EU app3/US }",
		"server app2 172.31.2.10:8080",
		"timeout server 60s",
	} {
		if !strings.Contains(text, want+"\n") {
			t.Errorf("config is missing %q", want)
		}
	}
	if strings.Contains(text, "ssl crt") {
		t.Error("HTTPS must stay disabled without an address")
	}
	// The checks that answer without an upstream must come before routing.
	order := []string{"return status 503", "return status 404", "return status 400 if", "return status 400 unless", "lua.isureal_delay", "use_backend"}
	last := -1
	for _, marker := range order {
		index := strings.Index(text, marker)
		if index < last {
			t.Fatalf("%q is out of order", marker)
		}
		last = index
	}
}

func TestRenderWithoutDelayOrHTTPS(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, map[string]string{
		"ROUTER_AP_EU_DELAY":          "0s",
		"ROUTER_EU_US_DELAY":          "0s",
		"ROUTER_AP_US_DELAY":          "0s",
		"ROUTER_HTTPS_LISTEN_ADDRESS": "0.0.0.0:443",
		"ROUTER_TLS_CERTIFICATE":      "/etc/isucon/haproxy/tls/server.pem",
		"ROUTER_UPSTREAM_PORT":        "80",
	})
	files, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[LuaFile]; ok || strings.Contains(files[ConfigFile], "lua") {
		t.Fatal("Lua is loaded without any delay")
	}
	for _, want := range []string{"bind 0.0.0.0:443 ssl crt /etc/isucon/haproxy/tls/server.pem\n", "server app1 172.31.1.10:80\n"} {
		if !strings.Contains(files[ConfigFile], want) {
			t.Errorf("config is missing %q", want)
		}
	}
}

func TestRenderRoundsDelayUpToMilliseconds(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, nil)
	cfg.Traffic.APEU.Delay = 1500 * time.Microsecond
	files, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files[ConfigFile], "lua.isureal_delay 2 if { var(txn.pair) -m str app1/EU app2/AP }") {
		t.Fatalf("delay was not rounded up:\n%s", files[ConfigFile])
	}
}

func TestRoutesCoverEveryHostAndRegion(t *testing.T) {
	t.Parallel()
	profile := domain.TrafficProfile{
		SameRegion: domain.TrafficClass{BandwidthBPS: 3},
		APEU:       domain.TrafficClass{BandwidthBPS: 1}, EUUS: domain.TrafficClass{BandwidthBPS: 1}, APUS: domain.TrafficClass{BandwidthBPS: 1},
	}
	want := map[string]domain.ServerID{
		"app/AP": domain.App1, "app/EU": domain.App2, "app/US": domain.App3,
		"app1/EU": domain.App1, "app2/US": domain.App2, "app3/AP": domain.App3,
	}
	got := map[string]route{}
	for _, r := range routes(profile) {
		got[r.key] = r
	}
	if len(got) != 12 {
		t.Fatalf("routes = %d, want 12", len(got))
	}
	for key, server := range want {
		r := got[key]
		same := strings.HasPrefix(key, "app/") || key == "app1/AP"
		if r.server != server || (r.class.BandwidthBPS == 3) != same {
			t.Errorf("%s: %+v", key, r)
		}
	}
}

// TestRenderedConfigIsValid runs HAProxy's own check when HAProxy is installed.
func TestRenderedConfigIsValid(t *testing.T) {
	t.Parallel()
	binary, err := exec.LookPath("haproxy")
	if err != nil {
		t.Skip("haproxy is not installed")
	}
	dir := t.TempDir()
	cfg := testConfig(t, map[string]string{"ROUTER_HAPROXY_DIR": dir})
	files, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "-c", "-f", filepath.Join(dir, ConfigFile)).CombinedOutput(); err != nil {
		t.Fatalf("haproxy -c: %v\n%s", err, output)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Fatalf("Write left %d files", len(entries))
	}
}
