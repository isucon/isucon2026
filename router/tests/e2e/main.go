package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/gen/go/portal_mock/v1/portal_mockv1connect"
)

const (
	portalEndpoint = "http://portal:8080"
	routerEndpoint = "http://router:8080"
	statusEndpoint = "http://router:18081"

	appHost     = "app.team1-abc123.isucon2026.westens.tokyo"
	server1Host = "server1.team1-abc123.isucon2026.westens.tokyo"
	server2Host = "server2.team1-abc123.isucon2026.westens.tokyo"
	server3Host = "server3.team1-abc123.isucon2026.westens.tokyo"
)

var initialConfig = expectedConfig{
	server1: portalv1.Region_REGION_AP,
	server2: portalv1.Region_REGION_AP,
	server3: portalv1.Region_REGION_AP,
	main:    portalv1.ServerId_SERVER_ID_SERVER1,
	country: "JP",
}

type expectedConfig struct {
	server1 portalv1.Region
	server2 portalv1.Region
	server3 portalv1.Region
	main    portalv1.ServerId
	country string
}

type runner struct {
	portal portal_mockv1connect.PortalMockServiceClient
	api    *http.Client
}

type identityResponse struct {
	Server        string   `json:"server"`
	CountryValues []string `json:"country_values"`
}

type routeResponse struct {
	Target string `json:"target"`
	Route  string `json:"route"`
}

type fetchResponse struct {
	Status         int    `json:"status"`
	Bytes          int    `json:"bytes"`
	SHA256         string `json:"sha256"`
	DurationMillis int64  `json:"duration_millis"`
}

type httpResult struct {
	status   int
	body     []byte
	duration time.Duration
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if len(os.Args) != 2 {
		log.Fatalf("usage: router-e2e <full|verify-initial>")
	}

	transport := &http.Transport{
		Proxy:              nil,
		DisableCompression: true,
	}
	portalHTTP := &http.Client{Transport: transport.Clone(), Timeout: 3 * time.Second}
	r := &runner{
		portal: portal_mockv1connect.NewPortalMockServiceClient(portalHTTP, portalEndpoint),
		api:    &http.Client{Transport: transport.Clone(), Timeout: 30 * time.Second},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var err error
	switch os.Args[1] {
	case "full":
		err = r.full(ctx)
	case "verify-initial":
		err = r.verifyInitial(ctx)
	default:
		err = fmt.Errorf("unknown mode %q", os.Args[1])
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("PASS scenario=%s", os.Args[1])
}

func (r *runner) full(ctx context.Context) error {
	if err := r.waitPortal(ctx); err != nil {
		return err
	}
	if _, err := r.portal.Reset(ctx, connect.NewRequest(&portalmockv1.ResetRequest{})); err != nil {
		return fmt.Errorf("reset Portal: %w", err)
	}
	if err := r.waitStable(ctx, initialConfig); err != nil {
		return err
	}
	if err := r.assertInitialHTTP(ctx); err != nil {
		return err
	}
	if err := r.assertHTTPTrafficProfile(ctx); err != nil {
		return err
	}

	distinct := expectedConfig{
		server1: portalv1.Region_REGION_AP,
		server2: portalv1.Region_REGION_EU,
		server3: portalv1.Region_REGION_US,
		main:    portalv1.ServerId_SERVER_ID_SERVER3,
		country: "US",
	}
	updateStarted := time.Now()
	if err := r.updateDesired(ctx, distinct); err != nil {
		return err
	}
	if err := r.waitStable(ctx, distinct); err != nil {
		return err
	}
	if elapsed := time.Since(updateStarted); elapsed > 10*time.Second {
		return fmt.Errorf("dynamic configuration took longer than 10s: %s", elapsed)
	}
	if err := r.assertDynamicHTTP(ctx); err != nil {
		return err
	}
	if err := r.assertSixPeerRoutes(ctx); err != nil {
		return err
	}
	crossDuration, err := r.peerFetch(ctx, server1Host, "JP", "172.31.2.10", 256<<10)
	if err != nil {
		return fmt.Errorf("cross-region peer transfer: %w", err)
	}
	if crossDuration < 1500*time.Millisecond {
		return fmt.Errorf("cross-region peer transfer was not rate limited: %s", crossDuration)
	}
	if rate := bitsPerSecond(256<<10, crossDuration); rate < 650_000 || rate > 1_350_000 {
		return fmt.Errorf("cross-region peer rate %.0f bps is outside 1Mbps ±35%%", rate)
	}
	log.Printf("PASS peer-transfer cross=%s rate=%.0fbps", crossDuration, bitsPerSecond(256<<10, crossDuration))

	same := distinct
	same.server2 = portalv1.Region_REGION_AP
	if err := r.updateDesired(ctx, same); err != nil {
		return err
	}
	if err := r.waitStable(ctx, same); err != nil {
		return err
	}
	sameDuration, err := r.peerFetch(ctx, server1Host, "JP", "172.31.2.10", 256<<10)
	if err != nil {
		return fmt.Errorf("same-region peer transfer: %w", err)
	}
	if sameDuration+750*time.Millisecond >= crossDuration {
		return fmt.Errorf("peer shaping did not distinguish regions: same=%s cross=%s", sameDuration, crossDuration)
	}
	log.Printf("PASS peer-transfer same=%s cross=%s", sameDuration, crossDuration)
	return nil
}

func (r *runner) verifyInitial(ctx context.Context) error {
	if err := r.waitStable(ctx, initialConfig); err != nil {
		return err
	}
	if err := r.assertIdentity(ctx, appHost, nil, "server1", []string{"JP"}); err != nil {
		return fmt.Errorf("initial app route: %w", err)
	}
	if _, err := r.peerFetch(ctx, server1Host, "JP", "172.31.2.10", 32<<10); err != nil {
		return fmt.Errorf("peer path after recovery: %w", err)
	}
	log.Printf("PASS initial configuration and peer path recovered")
	return nil
}

func (r *runner) waitPortal(ctx context.Context) error {
	return waitFor(ctx, 45*time.Second, "Portal availability", func() error {
		_, err := r.portal.GetState(ctx, connect.NewRequest(&portalmockv1.GetStateRequest{}))
		return err
	})
}

func (r *runner) waitStable(ctx context.Context, want expectedConfig) error {
	if err := waitFor(ctx, 30*time.Second, "Portal convergence", func() error {
		response, err := r.portal.GetState(ctx, connect.NewRequest(&portalmockv1.GetStateRequest{}))
		if err != nil {
			return err
		}
		portal := response.Msg.GetState().GetPortal()
		if portal == nil || portal.GetDesired() == nil {
			return errors.New("Portal state has no desired snapshot")
		}
		if err := want.matches(portal.GetDesired().GetConfig()); err != nil {
			return err
		}
		if !portal.GetConverged() {
			return fmt.Errorf("not converged: desired=%q applied=%q state=%s reason=%q",
				portal.GetDesired().GetSnapshotId(), portal.GetApplied().GetSnapshotId(),
				portal.GetApply().GetState(), portal.GetApply().GetReason())
		}
		if portal.GetApplied().GetSnapshotId() != portal.GetDesired().GetSnapshotId() {
			return fmt.Errorf("snapshot mismatch: desired=%q applied=%q", portal.GetDesired().GetSnapshotId(), portal.GetApplied().GetSnapshotId())
		}
		return nil
	}); err != nil {
		return err
	}
	return waitFor(ctx, 30*time.Second, "Router readiness", func() error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, statusEndpoint+"/readyz", nil)
		if err != nil {
			return err
		}
		response, err := r.api.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("readyz status=%d body=%s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil
	})
}

func (c expectedConfig) matches(actual *portalv1.Config) error {
	if actual == nil || actual.GetServerRegions() == nil {
		return errors.New("desired config is incomplete")
	}
	regions := actual.GetServerRegions()
	if regions.GetServer1() != c.server1 || regions.GetServer2() != c.server2 || regions.GetServer3() != c.server3 ||
		actual.GetMainServer() != c.main || actual.GetDefaultCountry() != c.country {
		return fmt.Errorf("desired config differs: got=(%s,%s,%s,%s,%s) want=(%s,%s,%s,%s,%s)",
			regions.GetServer1(), regions.GetServer2(), regions.GetServer3(), actual.GetMainServer(), actual.GetDefaultCountry(),
			c.server1, c.server2, c.server3, c.main, c.country)
	}
	return nil
}

func (r *runner) updateDesired(ctx context.Context, config expectedConfig) error {
	response, err := r.portal.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{
			Server1Region:  config.server1.Enum(),
			Server2Region:  config.server2.Enum(),
			Server3Region:  config.server3.Enum(),
			MainServer:     config.main.Enum(),
			DefaultCountry: &config.country,
		},
	}))
	if err != nil {
		return fmt.Errorf("update desired: %w", err)
	}
	if !response.Msg.GetChanged() {
		return errors.New("Portal reported no desired change")
	}
	log.Printf("PASS desired update snapshot=%s", response.Msg.GetDesired().GetSnapshotId())
	return nil
}

func (r *runner) assertInitialHTTP(ctx context.Context) error {
	if err := r.assertIdentity(ctx, appHost, nil, "server1", []string{"JP"}); err != nil {
		return fmt.Errorf("default country/main route: %w", err)
	}
	for _, test := range []struct {
		host   string
		server string
	}{
		{server1Host, "server1"},
		{server2Host, "server2"},
		{server3Host, "server3"},
	} {
		if err := r.assertIdentity(ctx, test.host, nil, test.server, []string{"JP"}); err != nil {
			return fmt.Errorf("individual host %s: %w", test.server, err)
		}
	}

	for _, country := range []string{"US", "CA", "MX", "JP", "KR", "CN", "AU", "GB", "DE", "FR"} {
		if err := r.assertIdentity(ctx, appHost, []string{country}, "server1", []string{country}); err != nil {
			return fmt.Errorf("country %s: %w", country, err)
		}
	}
	if err := r.assertHTTPStatus(ctx, appHost, []string{"XX"}, "/identity", http.StatusBadRequest); err != nil {
		return fmt.Errorf("invalid country: %w", err)
	}
	if err := r.assertHTTPStatus(ctx, appHost, []string{"US", "JP"}, "/identity", http.StatusBadRequest); err != nil {
		return fmt.Errorf("duplicate country: %w", err)
	}
	if err := r.assertHTTPStatus(ctx, "unknown.team1.invalid", []string{"JP"}, "/identity", http.StatusNotFound); err != nil {
		return fmt.Errorf("unknown host: %w", err)
	}
	log.Printf("PASS HTTP host/country/default/error matrix")
	return nil
}

func (r *runner) assertDynamicHTTP(ctx context.Context) error {
	if err := r.assertIdentity(ctx, appHost, nil, "server3", []string{"US"}); err != nil {
		return fmt.Errorf("dynamic main/default route: %w", err)
	}
	if err := r.assertIdentity(ctx, appHost, []string{"JP"}, "server3", []string{"JP"}); err != nil {
		return fmt.Errorf("dynamic main explicit country: %w", err)
	}
	if err := r.assertIdentity(ctx, server2Host, []string{"DE"}, "server2", []string{"DE"}); err != nil {
		return fmt.Errorf("dynamic server region route: %w", err)
	}
	for requestNumber := 1; requestNumber <= 100; requestNumber++ {
		if err := r.assertIdentity(ctx, appHost, nil, "server3", []string{"US"}); err != nil {
			return fmt.Errorf("post-update request %d/100: %w", requestNumber, err)
		}
	}
	log.Printf("PASS dynamic main/default/region route update and 100 post-update requests")
	return nil
}

func (r *runner) assertHTTPTrafficProfile(ctx context.Context) error {
	sameSamples := make([]time.Duration, 0, 3)
	crossSamples := make([]time.Duration, 0, 3)
	for range 3 {
		same, err := r.identityDuration(ctx, server1Host, "JP")
		if err != nil {
			return err
		}
		cross, err := r.identityDuration(ctx, server1Host, "US")
		if err != nil {
			return err
		}
		sameSamples = append(sameSamples, same)
		crossSamples = append(crossSamples, cross)
	}
	slices.Sort(sameSamples)
	slices.Sort(crossSamples)
	sameDelay, crossDelay := sameSamples[1], crossSamples[1]
	if crossDelay < 100*time.Millisecond || crossDelay-sameDelay < 96*time.Millisecond {
		return fmt.Errorf("HTTP delay profile not observed: same=%s cross=%s", sameDelay, crossDelay)
	}

	const blobBytes = 384 << 10
	sameBandwidth, err := r.blobDuration(ctx, server1Host, "JP", blobBytes)
	if err != nil {
		return err
	}
	crossBandwidth, err := r.blobDuration(ctx, server1Host, "US", blobBytes)
	if err != nil {
		return err
	}
	if crossBandwidth < 2*time.Second || crossBandwidth <= sameBandwidth*2 {
		return fmt.Errorf("HTTP bandwidth profile not observed: same=%s cross=%s", sameBandwidth, crossBandwidth)
	}
	crossRate := bitsPerSecond(blobBytes, crossBandwidth)
	if crossRate < 650_000 || crossRate > 1_350_000 {
		return fmt.Errorf("cross-region HTTP rate %.0f bps is outside 1Mbps ±35%%", crossRate)
	}
	log.Printf("PASS HTTP profile delay_same=%s delay_cross=%s bandwidth_same=%s bandwidth_cross=%s cross_rate=%.0fbps",
		sameDelay, crossDelay, sameBandwidth, crossBandwidth, crossRate)
	return nil
}

func (r *runner) identityDuration(ctx context.Context, host, country string) (time.Duration, error) {
	result, err := r.do(ctx, host, []string{country}, "/identity")
	if err != nil {
		return 0, err
	}
	if result.status != http.StatusOK {
		return 0, fmt.Errorf("identity status=%d body=%s", result.status, strings.TrimSpace(string(result.body)))
	}
	return result.duration, nil
}

func (r *runner) blobDuration(ctx context.Context, host, country string, size int) (time.Duration, error) {
	result, err := r.do(ctx, host, []string{country}, fmt.Sprintf("/blob?bytes=%d", size))
	if err != nil {
		return 0, err
	}
	if result.status != http.StatusOK || len(result.body) != size {
		return 0, fmt.Errorf("blob status=%d bytes=%d want=%d", result.status, len(result.body), size)
	}
	return result.duration, nil
}

func (r *runner) assertIdentity(ctx context.Context, host string, countries []string, wantServer string, wantCountries []string) error {
	result, err := r.do(ctx, host, countries, "/identity")
	if err != nil {
		return err
	}
	if result.status != http.StatusOK {
		return fmt.Errorf("status=%d body=%s", result.status, strings.TrimSpace(string(result.body)))
	}
	var identity identityResponse
	if err := json.Unmarshal(result.body, &identity); err != nil {
		return fmt.Errorf("decode identity: %w", err)
	}
	if identity.Server != wantServer || !slices.Equal(identity.CountryValues, wantCountries) {
		return fmt.Errorf("identity got=%+v want_server=%s want_countries=%v", identity, wantServer, wantCountries)
	}
	return nil
}

func (r *runner) assertHTTPStatus(ctx context.Context, host string, countries []string, path string, want int) error {
	result, err := r.do(ctx, host, countries, path)
	if err != nil {
		return err
	}
	if result.status != want {
		return fmt.Errorf("status=%d want=%d body=%s", result.status, want, strings.TrimSpace(string(result.body)))
	}
	return nil
}

func (r *runner) assertSixPeerRoutes(ctx context.Context) error {
	servers := []struct {
		host     string
		country  string
		address  string
		routerIP string
	}{
		{server1Host, "JP", "172.31.1.10", "172.31.1.2"},
		{server2Host, "DE", "172.31.2.10", "172.31.2.2"},
		{server3Host, "US", "172.31.3.10", "172.31.3.2"},
	}
	for sourceIndex, source := range servers {
		for targetIndex, target := range servers {
			if sourceIndex == targetIndex {
				continue
			}
			path := "/route?target=" + url.QueryEscape(target.address)
			result, err := r.do(ctx, source.host, []string{source.country}, path)
			if err != nil {
				return err
			}
			if result.status != http.StatusOK {
				return fmt.Errorf("route %d->%d status=%d body=%s", sourceIndex+1, targetIndex+1, result.status, strings.TrimSpace(string(result.body)))
			}
			var route routeResponse
			if err := json.Unmarshal(result.body, &route); err != nil {
				return fmt.Errorf("decode route: %w", err)
			}
			if route.Target != target.address || !strings.Contains(route.Route, "via "+source.routerIP) {
				return fmt.Errorf("route %d->%d bypasses Router: %+v", sourceIndex+1, targetIndex+1, route)
			}
		}
	}
	log.Printf("PASS all six peer routes use Router")
	return nil
}

func (r *runner) peerFetch(ctx context.Context, sourceHost, country, target string, size int) (time.Duration, error) {
	targetURL := fmt.Sprintf("http://%s:8080/blob?bytes=%d", target, size)
	path := "/fetch?url=" + url.QueryEscape(targetURL)
	result, err := r.do(ctx, sourceHost, []string{country}, path)
	if err != nil {
		return 0, err
	}
	if result.status != http.StatusOK {
		return 0, fmt.Errorf("fetch status=%d body=%s", result.status, strings.TrimSpace(string(result.body)))
	}
	var fetch fetchResponse
	if err := json.Unmarshal(result.body, &fetch); err != nil {
		return 0, fmt.Errorf("decode fetch: %w", err)
	}
	if fetch.Status != http.StatusOK || fetch.Bytes != size || len(fetch.SHA256) != 64 {
		return 0, fmt.Errorf("unexpected fetch result: %+v", fetch)
	}
	return time.Duration(fetch.DurationMillis) * time.Millisecond, nil
}

func (r *runner) do(ctx context.Context, host string, countries []string, path string) (httpResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, routerEndpoint+path, nil)
	if err != nil {
		return httpResult{}, err
	}
	request.Host = host
	for _, country := range countries {
		request.Header.Add("X-Country-Code", country)
	}
	started := time.Now()
	response, err := r.api.Do(request)
	if err != nil {
		return httpResult{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 9<<20))
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{status: response.StatusCode, body: body, duration: time.Since(started)}, nil
}

func bitsPerSecond(bytes int, duration time.Duration) float64 {
	return float64(bytes*8) / duration.Seconds()
}

func waitFor(ctx context.Context, timeout time.Duration, label string, check func() error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := check(); err == nil {
			log.Printf("PASS %s", label)
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w (last error: %v)", label, ctx.Err(), lastErr)
		case <-deadline.C:
			return fmt.Errorf("%s timed out: %w", label, lastErr)
		case <-ticker.C:
		}
	}
}
