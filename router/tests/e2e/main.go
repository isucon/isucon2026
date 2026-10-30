package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/gen/go/portal_mock/v1/portal_mockv1connect"
	"github.com/zeebo/xxh3"
)

const (
	portalEndpoint = "http://portal:8080"
	routerEndpoint = "http://router:8080"
	statusEndpoint = "http://router:18081"

	appHost  = "app.team1-abc123.isucon2026.westens.tokyo"
	app1Host = "asia.team1-abc123.isucon2026.westens.tokyo"
	app2Host = "europe.team1-abc123.isucon2026.westens.tokyo"
	app3Host = "america.team1-abc123.isucon2026.westens.tokyo"
)

var initialConfig = expectedConfig{
	app1:    portalv1.Region_REGION_AP,
	app2:    portalv1.Region_REGION_EU,
	app3:    portalv1.Region_REGION_US,
	country: "JP",
}

type expectedConfig struct {
	app1    portalv1.Region
	app2    portalv1.Region
	app3    portalv1.Region
	country string
}

type runner struct {
	portal portal_mockv1connect.PortalMockServiceClient
	api    *http.Client
}

type identityResponse struct {
	Server         string   `json:"server"`
	CountryValues  []string `json:"country_values"`
	ClientIPValues []string `json:"client_ip_values"`
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
	localIP  string
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if len(os.Args) != 2 {
		log.Fatalf("usage: router-e2e <full|verify-initial|digest-fallback>")
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
	case "digest-fallback":
		err = r.assertDigestFallback(ctx)
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
	if err := r.assertDigest(ctx); err != nil {
		return fmt.Errorf("digest: %w", err)
	}

	distinct := expectedConfig{
		app1:    portalv1.Region_REGION_AP,
		app2:    portalv1.Region_REGION_EU,
		app3:    portalv1.Region_REGION_US,
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
	crossDuration, err := r.peerFetch(ctx, app1Host, "JP", "172.31.2.10", 256<<10)
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

	_, err = r.portal.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{App2Region: portalv1.Region_REGION_AP.Enum()},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		return fmt.Errorf("region update must be rejected: %v", err)
	}
	if err := r.waitStable(ctx, distinct); err != nil {
		return err
	}
	sameDB, err := r.peerFetch(ctx, app1Host, "JP", "172.31.4.10", 256<<10)
	if err != nil {
		return fmt.Errorf("same-region DB transfer: %w", err)
	}
	crossDB, err := r.peerFetch(ctx, app2Host, "DE", "172.31.4.10", 256<<10)
	if err != nil {
		return fmt.Errorf("cross-region DB transfer: %w", err)
	}
	if sameDB+750*time.Millisecond >= crossDB || crossDB < 1500*time.Millisecond {
		return fmt.Errorf("DB shaping: same=%s cross=%s", sameDB, crossDB)
	}
	if err := r.assertIdentity(ctx, "db1.team1-abc123.isucon2026.westens.tokyo", []string{"JP"}, "app1", []string{"JP"}); err != nil {
		return fmt.Errorf("DB must not be an HTTP target: %w", err)
	}
	log.Printf("PASS db1 AP placement: same=%s cross=%s; DB excluded from HTTP routing", sameDB, crossDB)
	log.Printf("PASS fixed server regions reject updates")
	return nil
}

func (r *runner) verifyInitial(ctx context.Context) error {
	if err := r.waitStable(ctx, initialConfig); err != nil {
		return err
	}
	if err := r.assertIdentity(ctx, appHost, nil, "app1", []string{"JP"}); err != nil {
		return fmt.Errorf("initial app route: %w", err)
	}
	if _, err := r.peerFetch(ctx, app1Host, "JP", "172.31.2.10", 32<<10); err != nil {
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
	if regions.GetApp1() != c.app1 || regions.GetApp2() != c.app2 || regions.GetApp3() != c.app3 ||
		regions.GetDb1() != portalv1.Region_REGION_AP || actual.GetDefaultCountry() != c.country {
		return fmt.Errorf("desired config differs: got=(%s,%s,%s,%s) want=(%s,%s,%s,%s)",
			regions.GetApp1(), regions.GetApp2(), regions.GetApp3(), actual.GetDefaultCountry(),
			c.app1, c.app2, c.app3, c.country)
	}
	return nil
}

func (r *runner) updateDesired(ctx context.Context, config expectedConfig) error {
	response, err := r.portal.UpdateDesired(ctx, connect.NewRequest(&portalmockv1.UpdateDesiredRequest{
		Patch: &portalmockv1.DesiredPatch{
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

func nearestServer(country string) string {
	switch country {
	case "US", "CA", "MX":
		return "app3"
	case "GB", "DE", "FR":
		return "app2"
	default:
		return "app1"
	}
}

func (r *runner) assertInitialHTTP(ctx context.Context) error {
	if err := r.assertIdentity(ctx, appHost, nil, "app1", []string{"JP"}); err != nil {
		return fmt.Errorf("default country/main route: %w", err)
	}
	for _, test := range []struct {
		host   string
		server string
	}{
		{app1Host, "app1"},
		{app2Host, "app2"},
		{app3Host, "app3"},
	} {
		if err := r.assertIdentity(ctx, test.host, nil, test.server, []string{"JP"}); err != nil {
			return fmt.Errorf("individual host %s: %w", test.server, err)
		}
	}

	for _, country := range []string{"US", "CA", "MX", "JP", "KR", "CN", "AU", "GB", "DE", "FR"} {
		if err := r.assertIdentity(ctx, appHost, []string{country}, nearestServer(country), []string{country}); err != nil {
			return fmt.Errorf("country %s: %w", country, err)
		}
	}
	if err := r.assertHTTPStatus(ctx, appHost, []string{"XX"}, "/identity", http.StatusBadRequest); err != nil {
		return fmt.Errorf("invalid country: %w", err)
	}
	if err := r.assertHTTPStatus(ctx, appHost, []string{"US", "JP"}, "/identity", http.StatusBadRequest); err != nil {
		return fmt.Errorf("duplicate country: %w", err)
	}
	for _, host := range []string{"203.0.113.10", "unknown.team1.invalid"} {
		if err := r.assertIdentity(ctx, host, []string{"DE"}, "app2", []string{"DE"}); err != nil {
			return fmt.Errorf("other host %s: %w", host, err)
		}
	}
	if err := r.assertClientIP(ctx, "203.0.113.7", "203.0.113.7"); err != nil {
		return fmt.Errorf("given client IP: %w", err)
	}
	if err := r.assertClientIP(ctx, "", ""); err != nil {
		return fmt.Errorf("missing client IP: %w", err)
	}
	log.Printf("PASS HTTP host/country/default/client IP/error matrix")
	return nil
}

func (r *runner) assertDynamicHTTP(ctx context.Context) error {
	if err := r.assertIdentity(ctx, appHost, nil, "app3", []string{"US"}); err != nil {
		return fmt.Errorf("dynamic default-country route: %w", err)
	}
	if err := r.assertIdentity(ctx, appHost, []string{"JP"}, "app1", []string{"JP"}); err != nil {
		return fmt.Errorf("dynamic main explicit country: %w", err)
	}
	if err := r.assertIdentity(ctx, app2Host, []string{"DE"}, "app2", []string{"DE"}); err != nil {
		return fmt.Errorf("fixed server region route: %w", err)
	}
	for requestNumber := 1; requestNumber <= 100; requestNumber++ {
		if err := r.assertIdentity(ctx, appHost, nil, "app3", []string{"US"}); err != nil {
			return fmt.Errorf("post-update request %d/100: %w", requestNumber, err)
		}
	}
	log.Printf("PASS dynamic default-country route update and 100 post-update requests")
	return nil
}

func (r *runner) assertHTTPTrafficProfile(ctx context.Context) error {
	sameSamples := make([]time.Duration, 0, 3)
	crossSamples := make([]time.Duration, 0, 3)
	for range 3 {
		same, err := r.identityDuration(ctx, app1Host, "JP")
		if err != nil {
			return err
		}
		cross, err := r.identityDuration(ctx, app1Host, "US")
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
	sameBandwidth, err := r.blobDuration(ctx, app1Host, "JP", blobBytes)
	if err != nil {
		return err
	}
	crossBandwidth, err := r.blobDuration(ctx, app1Host, "US", blobBytes)
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

func (r *runner) assertClientIP(ctx context.Context, clientIP, want string) error {
	result, err := r.send(ctx, appHost, []string{"JP"}, clientIP, "/identity")
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
	if want == "" {
		want = result.localIP
	}
	if !slices.Equal(identity.ClientIPValues, []string{want}) {
		return fmt.Errorf("client IP got=%v want=%s", identity.ClientIPValues, want)
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
		{app1Host, "JP", "172.31.1.10", "172.31.1.2"},
		{app2Host, "DE", "172.31.2.10", "172.31.2.2"},
		{app3Host, "US", "172.31.3.10", "172.31.3.2"},
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
	return r.send(ctx, host, countries, "", path)
}

func (r *runner) send(ctx context.Context, host string, countries []string, clientIP, path string) (httpResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, routerEndpoint+path, nil)
	if err != nil {
		return httpResult{}, err
	}
	request.Host = host
	for _, country := range countries {
		request.Header.Add("X-Country-Code", country)
	}
	if clientIP != "" {
		request.Header.Set("X-Client-IP", clientIP)
	}
	var localIP string
	request = request.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			localIP, _, _ = net.SplitHostPort(info.Conn.LocalAddr().String())
		},
	}))
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
	return httpResult{status: response.StatusCode, body: body, duration: time.Since(started), localIP: localIP}, nil
}

// digestMarker starts an export entry that isureal-digest replaced: the marker,
// then the seed, the XXH3-64 and the length as big-endian uint64.
const digestMarker = "\x00isureal-xxh3:"

// fixtureImage and fixturePosts are what the fixture servers serve.
func fixtureImage(id string, size int) []byte {
	image := make([]byte, size)
	for i := range image {
		image[i] = id[i%len(id)] ^ byte(i>>10)
	}
	return image
}

const fixturePosts = `[{"id":"export","imagePath":"images/export.jpg"}]`

type download struct {
	status   int
	header   http.Header
	body     []byte
	duration time.Duration
}

// download fetches path as the benchmark does when seed is not zero, asking for
// SHA-256 instead of XXH3 if useSHA256 is set.
func (r *runner) download(ctx context.Context, host, country, path string, seed uint64, useSHA256 bool) (download, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, routerEndpoint+path, nil)
	if err != nil {
		return download{}, err
	}
	request.Host = host
	request.Header.Set("X-Country-Code", country)
	if seed != 0 {
		request.Header.Set("X-Benchmark-Request-Id", strconv.FormatUint(seed, 10))
	}
	if useSHA256 {
		request.Header.Set("X-Benchmark-Digest", "sha256")
	}
	started := time.Now()
	response, err := r.api.Do(request)
	if err != nil {
		return download{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 9<<20))
	if err != nil {
		return download{}, err
	}
	return download{status: response.StatusCode, header: response.Header, body: body, duration: time.Since(started)}, nil
}

func gunzip(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(gz)
}

func checkDigest(got download, image []byte, seed uint64) error {
	want := fmt.Sprintf("%016x", xxh3.HashSeed(image, seed))
	if got.status != http.StatusOK || len(got.body) != 0 || got.header.Get("X-Body-Xxh3") != want ||
		got.header.Get("X-Body-Length") != strconv.Itoa(len(image)) || got.header.Get("X-Fixture-Saw") != "" {
		return fmt.Errorf("status %d, %d bytes, X-Body-Xxh3 %q (want %q), X-Body-Length %q, app saw %q",
			got.status, len(got.body), got.header.Get("X-Body-Xxh3"), want, got.header.Get("X-Body-Length"), got.header.Get("X-Fixture-Saw"))
	}
	return nil
}

// assertDigest checks that the benchmark's image and export downloads get
// digests limited like the route, and that browsers still get the bytes.
func (r *runner) assertDigest(ctx context.Context) error {
	const seed = 0x1234_5678_9abc_def0
	image := fixtureImage("photo", 200_000)
	got, err := r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000", seed, false)
	if err != nil {
		return err
	}
	if err := checkDigest(got, image, seed); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	got, err = r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000", seed, true)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(image)
	if want := hex.EncodeToString(sum[:]); got.status != http.StatusOK || len(got.body) != 0 || got.header.Get("X-Body-Sha256") != want ||
		got.header.Get("X-Body-Xxh3") != "" || got.header.Get("X-Body-Length") != strconv.Itoa(len(image)) || got.header.Get("X-Fixture-Saw") != "" {
		return fmt.Errorf("SHA-256 image: status %d, %d bytes, X-Body-Sha256 %q (want %q), X-Body-Length %q, app saw %q",
			got.status, len(got.body), got.header.Get("X-Body-Sha256"), want, got.header.Get("X-Body-Length"), got.header.Get("X-Fixture-Saw"))
	}
	// An app that sends the image's digest headers over an empty body gets the
	// digest of the empty body, not its own headers.
	got, err = r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000&forge=1", seed, true)
	if err != nil {
		return err
	}
	empty := sha256.Sum256(nil)
	if want := digestHeaders(http.Header{"X-Body-Sha256": {hex.EncodeToString(empty[:])}, "X-Body-Length": {"0"}}); got.status != http.StatusOK || len(got.body) != 0 || digestHeaders(got.header) != want {
		return fmt.Errorf("forged SHA-256 image: status %d, %d bytes, %s (want %s)", got.status, len(got.body), digestHeaders(got.header), want)
	}
	got, err = r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000", 0, false)
	if err != nil {
		return err
	}
	if got.status != http.StatusOK || !bytes.Equal(got.body, image) || got.header.Get("X-Body-Xxh3") != "" {
		return fmt.Errorf("browser image: status %d, %d bytes, X-Body-Xxh3 %q", got.status, len(got.body), got.header.Get("X-Body-Xxh3"))
	}
	// An encoded body is passed through: its digest would not match the image
	// the benchmark expects, while the benchmark decodes the body itself.
	got, err = r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000&encoding=gzip", seed, false)
	if err != nil {
		return err
	}
	decoded, err := gunzip(got.body)
	if got.status != http.StatusOK || got.header.Get("Content-Encoding") != "gzip" || got.header.Get("X-Body-Xxh3") != "" ||
		err != nil || !bytes.Equal(decoded, image) {
		return fmt.Errorf("gzip image: status %d, Content-Encoding %q, X-Body-Xxh3 %q, %d bytes (%v)",
			got.status, got.header.Get("Content-Encoding"), got.header.Get("X-Body-Xxh3"), len(decoded), err)
	}

	got, err = r.download(ctx, appHost, "JP", "/api/me/export", seed, false)
	if err != nil {
		return err
	}
	archive, err := zip.NewReader(bytes.NewReader(got.body), int64(len(got.body)))
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	files := map[string][]byte{}
	for _, f := range archive.File {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("export %s: %w", f.Name, err)
		}
		files[f.Name], err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("export %s: %w", f.Name, err)
		}
	}
	exported := fixtureImage("export", 300_000)
	want := binary.BigEndian.AppendUint64([]byte(digestMarker), seed)
	want = binary.BigEndian.AppendUint64(want, xxh3.HashSeed(exported, seed))
	want = binary.BigEndian.AppendUint64(want, uint64(len(exported)))
	if string(files["posts.json"]) != fixturePosts || !bytes.Equal(files["images/export.jpg"], want) || got.header.Get("X-Fixture-Saw") != "" {
		return fmt.Errorf("export: posts.json %q, image %q, app saw %q", files["posts.json"], files["images/export.jpg"], got.header.Get("X-Fixture-Saw"))
	}

	// app2/AP is limited to 1 Mbps here: after one second of burst, the rest of
	// 400 kB takes about 2.2 s although HAProxy forwards only the digest.
	slow := fixtureImage("slow", 400_000)
	got, err = r.download(ctx, app2Host, "JP", "/api/images/slow?bytes=400000", seed, false)
	if err != nil {
		return err
	}
	if err := checkDigest(got, slow, seed); err != nil {
		return fmt.Errorf("limited image: %w", err)
	}
	if got.duration < 1500*time.Millisecond || got.duration > 15*time.Second {
		return fmt.Errorf("limited image took %s, want about 2.3s", got.duration)
	}
	log.Printf("PASS digest image/SHA-256 image/forged headers/export/browser/gzip; 400 kB at 1 Mbps took %s", got.duration)
	return nil
}

// digestHeaders lists every value of the headers isureal-digest answers with.
func digestHeaders(header http.Header) string {
	return fmt.Sprintf("X-Body-Sha256 %q X-Body-Xxh3 %q X-Body-Length %q",
		header.Values("X-Body-Sha256"), header.Values("X-Body-Xxh3"), header.Values("X-Body-Length"))
}

// assertDigestFallback runs while isureal-digest is stopped: the app answers the
// benchmark itself, without the headers meant for the Router, and the digest
// headers it sends are removed.
func (r *runner) assertDigestFallback(ctx context.Context) error {
	image := fixtureImage("photo", 200_000)
	err := waitFor(ctx, 15*time.Second, "app answers the benchmark while isureal-digest is down", func() error {
		got, err := r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000", 7, true)
		if err != nil {
			return err
		}
		if got.status != http.StatusOK || !bytes.Equal(got.body, image) || got.header.Get("X-Fixture-Saw") != "" {
			return fmt.Errorf("status %d, %d bytes, app saw %q", got.status, len(got.body), got.header.Get("X-Fixture-Saw"))
		}
		return nil
	})
	if err != nil {
		return err
	}
	got, err := r.download(ctx, appHost, "JP", "/api/images/photo?bytes=200000&forge=1", 7, true)
	if err != nil {
		return err
	}
	if want := digestHeaders(http.Header{}); got.status != http.StatusOK || len(got.body) != 0 || digestHeaders(got.header) != want {
		return fmt.Errorf("forged headers while isureal-digest is down: status %d, %d bytes, %s (want %s)", got.status, len(got.body), digestHeaders(got.header), want)
	}
	log.Printf("PASS forged digest headers removed while isureal-digest is down")
	return nil
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
