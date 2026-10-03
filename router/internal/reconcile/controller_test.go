package reconcile

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
	"github.com/isucon/isucon2026/router/internal/netcontrol"
	statusstore "github.com/isucon/isucon2026/router/internal/status"
)

func TestControllerCoalescesPendingSnapshotsAndSkipsDuplicateApply(t *testing.T) {
	t.Parallel()
	network := newFakeNetwork()
	network.blockFirst = make(chan struct{})
	routes := newFakeRoutes()
	portal := newFakePortal()
	controller := newTestController(portal, network, routes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go controller.Run(ctx)

	if err := controller.Submit(testProtoSnapshot("one", "JP")); err != nil {
		t.Fatal(err)
	}
	<-network.firstEntered
	if err := controller.Submit(testProtoSnapshot("two", "DE")); err != nil {
		t.Fatal(err)
	}
	if err := controller.Submit(testProtoSnapshot("three", "US")); err != nil {
		t.Fatal(err)
	}
	close(network.blockFirst)
	waitFor(t, time.Second, func() bool { return network.applyCount() == 2 && portal.successCount() >= 2 })
	if got := network.appliedCountries(); len(got) != 2 || got[0] != "JP" || got[1] != "US" {
		t.Fatalf("applied countries = %v", got)
	}
	if err := controller.Submit(testProtoSnapshot("three", "US")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return portal.successCount() >= 3 })
	if got := network.applyCount(); got != 2 {
		t.Fatalf("duplicate caused apply count %d", got)
	}
}

func TestControllerRollsBackNetworkAfterXDSFailure(t *testing.T) {
	t.Parallel()
	network := newFakeNetwork()
	routes := newFakeRoutes()
	portal := newFakePortal()
	controller := newTestController(portal, network, routes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go controller.Run(ctx)

	if err := controller.Submit(testProtoSnapshot("base", "JP")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return portal.successCount() == 1 })
	routes.setWaitError(errors.New("route NACK"))
	if err := controller.Submit(testProtoSnapshot("broken", "DE")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return network.restoreCount() > 0 && portal.failureCount() > 0 })
	active := network.Active()
	if active == nil || active.Config.DefaultCountry != "JP" {
		t.Fatalf("network did not roll back: %+v", active)
	}
	if snapshot := controller.Active(); snapshot == nil || snapshot.Config.DefaultCountry != "JP" {
		t.Fatalf("controller active changed after failure: %+v", snapshot)
	}
}

func TestControllerRetriesReportWithoutReapplying(t *testing.T) {
	t.Parallel()
	network := newFakeNetwork()
	routes := newFakeRoutes()
	portal := newFakePortal()
	portal.failReports = 2
	controller := newTestController(portal, network, routes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go controller.Run(ctx)
	if err := controller.Submit(testProtoSnapshot("report", "JP")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return portal.successCount() == 1 })
	if portal.reportAttempts() < 3 {
		t.Fatalf("report attempts = %d", portal.reportAttempts())
	}
	if network.applyCount() != 1 || routes.publishCount() != 1 {
		t.Fatalf("report retry reapplied data plane: network=%d routes=%d", network.applyCount(), routes.publishCount())
	}
}

func newTestController(portal *fakePortal, network *fakeNetwork, routes *fakeRoutes) *Controller {
	cfg, err := config.Load(func(key string) (string, bool) {
		values := map[string]string{
			"ROUTER_XDS_ACK_TIMEOUT":              "100ms",
			"ROUTER_REPORT_RETRY_INTERVAL":        "5ms",
			"ROUTER_PORTAL_RECONNECT_MIN_BACKOFF": "5ms",
			"ROUTER_PORTAL_RECONNECT_MAX_BACKOFF": "10ms",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		panic(err)
	}
	return NewController(cfg, portal, network, routes, statusstore.New())
}

type fakePortal struct {
	mu          sync.Mutex
	reports     []ApplyReport
	attempts    int
	failReports int
}

func newFakePortal() *fakePortal { return &fakePortal{} }

func (p *fakePortal) Stream(ctx context.Context, _ func(*portalv1.ConfigSnapshot) error, connected func(bool)) error {
	connected(true)
	defer connected(false)
	<-ctx.Done()
	return ctx.Err()
}

func (p *fakePortal) Report(_ context.Context, report ApplyReport) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.failReports > 0 {
		p.failReports--
		return errors.New("temporary report failure")
	}
	p.reports = append(p.reports, report)
	return nil
}

func (p *fakePortal) successCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, report := range p.reports {
		if report.Success {
			count++
		}
	}
	return count
}

func (p *fakePortal) failureCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reports) - p.successCountLocked()
}

func (p *fakePortal) successCountLocked() int {
	count := 0
	for _, report := range p.reports {
		if report.Success {
			count++
		}
	}
	return count
}

func (p *fakePortal) reportAttempts() int { p.mu.Lock(); defer p.mu.Unlock(); return p.attempts }

type fakeNetwork struct {
	mu           sync.Mutex
	applied      []domain.Config
	active       *netcontrol.Candidate
	restores     int
	firstEntered chan struct{}
	blockFirst   chan struct{}
}

func newFakeNetwork() *fakeNetwork { return &fakeNetwork{firstEntered: make(chan struct{})} }

func (n *fakeNetwork) Prepare(cfg domain.Config) (netcontrol.Candidate, error) {
	return netcontrol.Candidate{Config: cfg}, nil
}

func (n *fakeNetwork) Apply(_ context.Context, candidate netcontrol.Candidate) error {
	n.mu.Lock()
	first := len(n.applied) == 0
	if first {
		close(n.firstEntered)
	}
	n.mu.Unlock()
	if first && n.blockFirst != nil {
		<-n.blockFirst
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.applied = append(n.applied, candidate.Config)
	copyCandidate := candidate
	n.active = &copyCandidate
	return nil
}

func (n *fakeNetwork) Restore(_ context.Context, candidate *netcontrol.Candidate) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.restores++
	if candidate == nil {
		n.active = nil
	} else {
		copyCandidate := *candidate
		n.active = &copyCandidate
	}
	return nil
}

func (n *fakeNetwork) Active() *netcontrol.Candidate {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.active == nil {
		return nil
	}
	copyCandidate := *n.active
	return &copyCandidate
}

func (n *fakeNetwork) applyCount() int   { n.mu.Lock(); defer n.mu.Unlock(); return len(n.applied) }
func (n *fakeNetwork) restoreCount() int { n.mu.Lock(); defer n.mu.Unlock(); return n.restores }
func (n *fakeNetwork) appliedCountries() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	result := make([]string, 0, len(n.applied))
	for _, cfg := range n.applied {
		result = append(result, cfg.DefaultCountry)
	}
	return result
}

type fakeRoutes struct {
	mu        sync.Mutex
	published []string
	waitErr   error
}

func newFakeRoutes() *fakeRoutes { return &fakeRoutes{} }

func (r *fakeRoutes) Publish(_ context.Context, version string, _ *routev3.RouteConfiguration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.published = append(r.published, version)
	return nil
}
func (r *fakeRoutes) Wait(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.waitErr
}
func (r *fakeRoutes) setWaitError(err error) { r.mu.Lock(); defer r.mu.Unlock(); r.waitErr = err }
func (r *fakeRoutes) publishCount() int      { r.mu.Lock(); defer r.mu.Unlock(); return len(r.published) }

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func testDomainConfig() domain.Config {
	return domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: "JP"}
}
