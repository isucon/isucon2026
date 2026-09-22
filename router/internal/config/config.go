package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/isucon/isucon2026/router/internal/domain"
)

type LookupFunc func(string) (string, bool)

type ServerNetwork struct {
	Subnet   netip.Prefix
	RouterIP netip.Addr
	ServerIP netip.Addr
}

type Hostnames struct {
	App     string
	Server1 string
	Server2 string
	Server3 string
}

func (h Hostnames) For(server domain.ServerID) string {
	switch server {
	case domain.Server1:
		return h.Server1
	case domain.Server2:
		return h.Server2
	case domain.Server3:
		return h.Server3
	default:
		return ""
	}
}

type Config struct {
	PortalEndpoint      string
	TeamNumber          uint32
	XDSListenAddress    string
	StatusListenAddress string
	EnvoyNodeID         string
	RouteConfigName     string
	Hosts               Hostnames
	Servers             map[domain.ServerID]ServerNetwork
	Traffic             domain.TrafficProfile
	XDSAckTimeout       time.Duration
	CommandTimeout      time.Duration
	ReportRetryInterval time.Duration
	ReconnectMinBackoff time.Duration
	ReconnectMaxBackoff time.Duration
}

func FromEnv() (Config, error) {
	return Load(os.LookupEnv)
}

func Load(lookup LookupFunc) (Config, error) {
	teamNumber, err := uint32Value(lookup, "ROUTER_TEAM_NUMBER", 1)
	if err != nil || teamNumber == 0 {
		return Config{}, errors.New("ROUTER_TEAM_NUMBER must be a positive uint32")
	}

	cfg := Config{
		PortalEndpoint:      stringValue(lookup, "ROUTER_PORTAL_ENDPOINT", "http://portal:8080"),
		TeamNumber:          teamNumber,
		XDSListenAddress:    stringValue(lookup, "ROUTER_XDS_LISTEN_ADDRESS", "0.0.0.0:18000"),
		StatusListenAddress: stringValue(lookup, "ROUTER_STATUS_LISTEN_ADDRESS", "0.0.0.0:18081"),
		EnvoyNodeID:         stringValue(lookup, "ROUTER_ENVOY_NODE_ID", "isureal-router-envoy"),
		RouteConfigName:     stringValue(lookup, "ROUTER_ROUTE_CONFIG_NAME", "isureal_routes"),
		Hosts: Hostnames{
			App:     stringValue(lookup, "ROUTER_APP_HOST", "app.team1-abc123.isucon2026.westens.tokyo"),
			Server1: stringValue(lookup, "ROUTER_SERVER1_HOST", "server1.team1-abc123.isucon2026.westens.tokyo"),
			Server2: stringValue(lookup, "ROUTER_SERVER2_HOST", "server2.team1-abc123.isucon2026.westens.tokyo"),
			Server3: stringValue(lookup, "ROUTER_SERVER3_HOST", "server3.team1-abc123.isucon2026.westens.tokyo"),
		},
		Servers: make(map[domain.ServerID]ServerNetwork, 3),
	}

	serverDefaults := map[domain.ServerID][3]string{
		domain.Server1: {"172.31.1.0/24", "172.31.1.2", "172.31.1.10"},
		domain.Server2: {"172.31.2.0/24", "172.31.2.2", "172.31.2.10"},
		domain.Server3: {"172.31.3.0/24", "172.31.3.2", "172.31.3.10"},
	}
	for _, server := range domain.ServerIDs() {
		prefix := "ROUTER_" + stringUpper(server)
		defaults := serverDefaults[server]
		subnet, err := netip.ParsePrefix(stringValue(lookup, prefix+"_SUBNET", defaults[0]))
		if err != nil || !subnet.Addr().Is4() || subnet != subnet.Masked() {
			return Config{}, fmt.Errorf("%s_SUBNET must be a canonical IPv4 prefix", prefix)
		}
		routerIP, err := netip.ParseAddr(stringValue(lookup, prefix+"_REGION_LOCAL_IP", defaults[1]))
		if err != nil || !routerIP.Is4() {
			return Config{}, fmt.Errorf("%s_REGION_LOCAL_IP must be IPv4", prefix)
		}
		serverIP, err := netip.ParseAddr(stringValue(lookup, prefix+"_IP", defaults[2]))
		if err != nil || !serverIP.Is4() {
			return Config{}, fmt.Errorf("%s_IP must be IPv4", prefix)
		}
		if !subnet.Contains(routerIP) || !subnet.Contains(serverIP) || routerIP == serverIP {
			return Config{}, fmt.Errorf("%s addresses must be distinct members of subnet", prefix)
		}
		cfg.Servers[server] = ServerNetwork{Subnet: subnet, RouterIP: routerIP, ServerIP: serverIP}
	}

	cfg.Traffic.SameRegionDelay, err = durationValue(lookup, "ROUTER_SAME_REGION_DELAY", 0)
	if err != nil {
		return Config{}, err
	}
	cfg.Traffic.CrossRegionDelay, err = durationValue(lookup, "ROUTER_CROSS_REGION_DELAY", 200*time.Millisecond)
	if err != nil {
		return Config{}, err
	}
	cfg.Traffic.SameRegionBandwidthBPS, err = uint64Value(lookup, "ROUTER_SAME_REGION_BANDWIDTH_BPS", 1_000_000_000)
	if err != nil {
		return Config{}, err
	}
	cfg.Traffic.CrossRegionBandwidthBPS, err = uint64Value(lookup, "ROUTER_CROSS_REGION_BANDWIDTH_BPS", 200_000_000)
	if err != nil {
		return Config{}, err
	}
	cfg.XDSAckTimeout, err = durationValue(lookup, "ROUTER_XDS_ACK_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.CommandTimeout, err = durationValue(lookup, "ROUTER_COMMAND_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.ReportRetryInterval, err = durationValue(lookup, "ROUTER_REPORT_RETRY_INTERVAL", 500*time.Millisecond)
	if err != nil {
		return Config{}, err
	}
	cfg.ReconnectMinBackoff, err = durationValue(lookup, "ROUTER_PORTAL_RECONNECT_MIN_BACKOFF", 100*time.Millisecond)
	if err != nil {
		return Config{}, err
	}
	cfg.ReconnectMaxBackoff, err = durationValue(lookup, "ROUTER_PORTAL_RECONNECT_MAX_BACKOFF", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func (c Config) Validate() error {
	portalURL, err := url.Parse(c.PortalEndpoint)
	if err != nil || (portalURL.Scheme != "http" && portalURL.Scheme != "https") || portalURL.Host == "" {
		return errors.New("ROUTER_PORTAL_ENDPOINT must be an absolute HTTP(S) URL")
	}
	for name, address := range map[string]string{"xds": c.XDSListenAddress, "status": c.StatusListenAddress} {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return fmt.Errorf("%s listen address: %w", name, err)
		}
	}
	if c.EnvoyNodeID == "" || c.RouteConfigName == "" {
		return errors.New("Envoy node ID and route config name are required")
	}
	hosts := []string{c.Hosts.App, c.Hosts.Server1, c.Hosts.Server2, c.Hosts.Server3}
	seenHosts := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		if !hostnamePattern.MatchString(host) {
			return fmt.Errorf("invalid hostname %q", host)
		}
		if _, ok := seenHosts[host]; ok {
			return fmt.Errorf("duplicate hostname %q", host)
		}
		seenHosts[host] = struct{}{}
	}
	for i, leftID := range domain.ServerIDs() {
		left := c.Servers[leftID]
		for _, rightID := range domain.ServerIDs()[i+1:] {
			right := c.Servers[rightID]
			if left.Subnet.Contains(right.Subnet.Addr()) || right.Subnet.Contains(left.Subnet.Addr()) {
				return fmt.Errorf("server subnets %s and %s overlap", leftID, rightID)
			}
		}
	}
	if err := c.Traffic.Validate(); err != nil {
		return err
	}
	if c.XDSAckTimeout <= 0 || c.CommandTimeout <= 0 || c.ReportRetryInterval <= 0 || c.ReconnectMinBackoff <= 0 || c.ReconnectMaxBackoff <= 0 {
		return errors.New("timeouts and backoffs must be positive")
	}
	if c.ReconnectMinBackoff > c.ReconnectMaxBackoff {
		return errors.New("minimum reconnect backoff must not exceed maximum")
	}
	return nil
}

func stringUpper(server domain.ServerID) string {
	switch server {
	case domain.Server1:
		return "SERVER1"
	case domain.Server2:
		return "SERVER2"
	case domain.Server3:
		return "SERVER3"
	default:
		return ""
	}
}

func stringValue(lookup LookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return fallback
}

func durationValue(lookup LookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func uint64Value(lookup LookupFunc, key string, fallback uint64) (uint64, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func uint32Value(lookup LookupFunc, key string, fallback uint32) (uint32, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(parsed), nil
}
