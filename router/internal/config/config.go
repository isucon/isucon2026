package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
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

type Config struct {
	PortalEndpoint      string
	TeamNumber          uint32
	StatusListenAddress string
	// HTTPListenAddress and HTTPSListenAddress are where HAProxy accepts
	// clients. HTTPS is disabled when its address is empty.
	HTTPListenAddress  string
	HTTPSListenAddress string
	// TLSCertificate is a PEM file holding the certificate chain and the key.
	TLSCertificate string
	// UpstreamPort is the HTTP port of the application servers.
	UpstreamPort uint16
	// HAProxyDir holds the files that HAProxy loads.
	HAProxyDir string
	// HAProxyRuntimeAddress is the TCP address of the HAProxy runtime API.
	HAProxyRuntimeAddress string
	// DigestAddress is where isureal-digest listens. HAProxy sends it the
	// benchmark's image and export downloads, and the apps directly while it is
	// down. Empty sends them to the apps.
	DigestAddress       string
	Servers             map[domain.ServerID]ServerNetwork
	Traffic             domain.TrafficProfile
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

	upstreamPort, err := uint32Value(lookup, "ROUTER_UPSTREAM_PORT", 8080)
	if err != nil || upstreamPort == 0 || upstreamPort > 65535 {
		return Config{}, errors.New("ROUTER_UPSTREAM_PORT must be a TCP port")
	}

	cfg := Config{
		PortalEndpoint:        stringValue(lookup, "ROUTER_PORTAL_ENDPOINT", "http://portal:8080"),
		TeamNumber:            teamNumber,
		StatusListenAddress:   stringValue(lookup, "ROUTER_STATUS_LISTEN_ADDRESS", "0.0.0.0:18081"),
		HTTPListenAddress:     stringValue(lookup, "ROUTER_HTTP_LISTEN_ADDRESS", "0.0.0.0:8080"),
		HTTPSListenAddress:    stringValue(lookup, "ROUTER_HTTPS_LISTEN_ADDRESS", ""),
		TLSCertificate:        stringValue(lookup, "ROUTER_TLS_CERTIFICATE", ""),
		UpstreamPort:          uint16(upstreamPort),
		HAProxyDir:            stringValue(lookup, "ROUTER_HAPROXY_DIR", "/run/isureal-haproxy"),
		HAProxyRuntimeAddress: stringValue(lookup, "ROUTER_HAPROXY_RUNTIME_ADDRESS", "127.0.0.1:18001"),
		DigestAddress:         stringValue(lookup, "ROUTER_DIGEST_ADDRESS", "127.0.0.1:18010"),
		Servers:               make(map[domain.ServerID]ServerNetwork, 4),
	}

	serverDefaults := map[domain.ServerID][3]string{
		domain.App1: {"172.31.1.0/24", "172.31.1.2", "172.31.1.10"},
		domain.App2: {"172.31.2.0/24", "172.31.2.2", "172.31.2.10"},
		domain.App3: {"172.31.3.0/24", "172.31.3.2", "172.31.3.10"},
		domain.DB1:  {"172.31.4.0/24", "172.31.4.2", "172.31.4.10"},
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
		// The router address may sit outside the server subnet when every server
		// subnet is routed through one shared router interface.
		if !subnet.Contains(serverIP) || routerIP == serverIP {
			return Config{}, fmt.Errorf("%s server address must be in its subnet and differ from the router address", prefix)
		}
		cfg.Servers[server] = ServerNetwork{Subnet: subnet, RouterIP: routerIP, ServerIP: serverIP}
	}

	crossRegion := domain.TrafficClass{Delay: 100 * time.Millisecond, BandwidthBPS: 200_000_000}
	for _, class := range []struct {
		prefix   string
		target   *domain.TrafficClass
		fallback domain.TrafficClass
	}{
		{"ROUTER_SAME_REGION", &cfg.Traffic.SameRegion, domain.TrafficClass{BandwidthBPS: 1_000_000_000}},
		{"ROUTER_AP_EU", &cfg.Traffic.APEU, crossRegion},
		{"ROUTER_EU_US", &cfg.Traffic.EUUS, crossRegion},
		{"ROUTER_AP_US", &cfg.Traffic.APUS, crossRegion},
	} {
		class.target.Delay, err = durationValue(lookup, class.prefix+"_DELAY", class.fallback.Delay)
		if err != nil {
			return Config{}, err
		}
		class.target.BandwidthBPS, err = uint64Value(lookup, class.prefix+"_BANDWIDTH_BPS", class.fallback.BandwidthBPS)
		if err != nil {
			return Config{}, err
		}
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

func (c Config) Validate() error {
	portalURL, err := url.Parse(c.PortalEndpoint)
	if err != nil || (portalURL.Scheme != "http" && portalURL.Scheme != "https") || portalURL.Host == "" {
		return errors.New("ROUTER_PORTAL_ENDPOINT must be an absolute HTTP(S) URL")
	}
	for name, address := range map[string]string{"status": c.StatusListenAddress, "HTTP": c.HTTPListenAddress, "HAProxy runtime": c.HAProxyRuntimeAddress} {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return fmt.Errorf("%s address: %w", name, err)
		}
	}
	if c.DigestAddress != "" {
		if _, _, err := net.SplitHostPort(c.DigestAddress); err != nil {
			return fmt.Errorf("digest address: %w", err)
		}
	}
	if c.HTTPSListenAddress != "" {
		if _, _, err := net.SplitHostPort(c.HTTPSListenAddress); err != nil {
			return fmt.Errorf("HTTPS listen address: %w", err)
		}
		if !filepath.IsAbs(c.TLSCertificate) {
			return errors.New("ROUTER_TLS_CERTIFICATE must be an absolute path when HTTPS is enabled")
		}
	}
	if c.UpstreamPort == 0 {
		return errors.New("upstream port is required")
	}
	if !filepath.IsAbs(c.HAProxyDir) {
		return errors.New("ROUTER_HAPROXY_DIR must be an absolute path")
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
	if c.CommandTimeout <= 0 || c.ReportRetryInterval <= 0 || c.ReconnectMinBackoff <= 0 || c.ReconnectMaxBackoff <= 0 {
		return errors.New("timeouts and backoffs must be positive")
	}
	if c.ReconnectMinBackoff > c.ReconnectMaxBackoff {
		return errors.New("minimum reconnect backoff must not exceed maximum")
	}
	return nil
}

func stringUpper(server domain.ServerID) string {
	switch server {
	case domain.App1:
		return "APP1"
	case domain.App2:
		return "APP2"
	case domain.App3:
		return "APP3"
	case domain.DB1:
		return "DB1"
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
