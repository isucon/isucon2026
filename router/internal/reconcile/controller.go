package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
	"github.com/isucon/isucon2026/router/internal/netcontrol"
	statusstore "github.com/isucon/isucon2026/router/internal/status"
)

type NetworkPlane interface {
	Prepare(domain.Config) (netcontrol.Candidate, error)
	Apply(context.Context, netcontrol.Candidate) error
	Restore(context.Context, *netcontrol.Candidate) error
	Active() *netcontrol.Candidate
}

// ProxyPlane is the HTTP proxy. Its routes are static; a snapshot only changes
// the default country.
type ProxyPlane interface {
	Apply(context.Context, domain.Config) error
	Serving(context.Context, domain.Config) error
}

type activeState struct {
	snapshot domain.ActiveSnapshot
	network  netcontrol.Candidate
}

type Controller struct {
	config     config.Config
	portal     Portal
	network    NetworkPlane
	proxy      ProxyPlane
	status     *statusstore.Store
	pending    chan domain.DesiredSnapshot
	reports    chan ApplyReport
	generation atomic.Uint64
	mu         sync.RWMutex
	active     *activeState
	// proxyLost is true while the proxy does not serve the active snapshot.
	proxyLost bool
}

func NewController(cfg config.Config, portal Portal, network NetworkPlane, proxy ProxyPlane, state *statusstore.Store) *Controller {
	return &Controller{
		config: cfg, portal: portal, network: network, proxy: proxy, status: state,
		pending: make(chan domain.DesiredSnapshot, 1), reports: make(chan ApplyReport, 1),
	}
}

func (c *Controller) Submit(snapshot *portalv1.ConfigSnapshot) error {
	generation := c.generation.Add(1)
	desired, err := domain.SnapshotFromProto(snapshot, generation)
	if err != nil {
		return err
	}
	enqueueLatest(c.pending, desired)
	return nil
}

func (c *Controller) Active() *domain.ActiveSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.active == nil {
		return nil
	}
	copySnapshot := c.active.snapshot
	return &copySnapshot
}

func (c *Controller) Run(ctx context.Context) error {
	streamResult := make(chan error, 1)
	go func() {
		streamResult <- c.portal.Stream(ctx, func(snapshot *portalv1.ConfigSnapshot) error {
			if err := c.Submit(snapshot); err != nil {
				id := "invalid"
				if snapshot != nil && snapshot.SnapshotId != "" {
					id = snapshot.SnapshotId
				}
				c.enqueueReport(ApplyReport{SnapshotID: id, Reason: err.Error()})
				return nil
			}
			return nil
		}, c.status.SetPortalConnected)
	}()
	go c.reportLoop(ctx)

	// A restarted proxy forgets the default country; the check restores it.
	proxyCheck := time.NewTicker(c.config.ReportRetryInterval)
	defer proxyCheck.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-streamResult:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("Portal stream stopped: %w", err)
		case desired := <-c.pending:
			c.reconcileUntilSuccessOrSuperseded(ctx, desired)
		case <-proxyCheck.C:
			c.restoreProxy(ctx)
		}
	}
}

// restoreProxy returns nil once the proxy serves the active snapshot, writing
// the default country back when a restarted proxy has lost it.
func (c *Controller) restoreProxy(ctx context.Context) error {
	active := c.activeCopy()
	if active == nil {
		return nil
	}
	err := c.proxy.Serving(ctx, active.snapshot.Config)
	if err != nil {
		err = c.proxy.Apply(ctx, active.snapshot.Config)
		if err == nil {
			slog.Info("Proxy default country restored", "snapshot_id", active.snapshot.ID)
		}
	}
	if lost := err != nil; lost != c.proxyLost {
		c.proxyLost = lost
		if lost {
			slog.Warn("Proxy does not serve the active snapshot", "snapshot_id", active.snapshot.ID, "error", err)
		}
	}
	return err
}

func (c *Controller) reconcileUntilSuccessOrSuperseded(ctx context.Context, desired domain.DesiredSnapshot) {
	var retry uint64
	for {
		if ctx.Err() != nil {
			return
		}
		var err error
		duplicate := c.isDuplicateActive(desired)
		if duplicate {
			// The proxy may have restarted since the snapshot was applied, so
			// the repeat is reported applied only once the proxy serves it.
			if err = c.restoreProxy(ctx); err == nil {
				c.enqueueReport(ApplyReport{SnapshotID: desired.ID, Config: desired.Config, Success: true})
				return
			}
			err = fmt.Errorf("proxy does not serve the active snapshot: %w", err)
		} else {
			c.status.StartApply(desired.ID, "validate", retry)
			if err = c.apply(ctx, desired); err == nil {
				return
			}
		}
		retry++
		c.status.RecordFailure(desired.ID, "apply-failed", err.Error(), retry)
		c.enqueueReport(ApplyReport{SnapshotID: desired.ID, Reason: err.Error()})
		if !duplicate {
			// The last-known-good snapshot stays active while this one fails, so
			// a proxy that restarts in the meantime still gets its default country.
			c.restoreProxy(ctx)
		}
		timer := time.NewTimer(c.config.ReportRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case next := <-c.pending:
			timer.Stop()
			desired = next
			retry = 0
		case <-timer.C:
		}
	}
}

func (c *Controller) apply(ctx context.Context, desired domain.DesiredSnapshot) error {
	c.status.SetPhase("network")
	candidate, err := c.network.Prepare(desired.Config)
	if err != nil {
		return fmt.Errorf("prepare network: %w", err)
	}
	previousNetwork := c.network.Active()
	previousActive := c.activeCopy()
	if err := c.network.Apply(ctx, candidate); err != nil {
		return fmt.Errorf("apply network: %w", err)
	}

	c.status.SetPhase("proxy")
	if err := c.proxy.Apply(ctx, desired.Config); err != nil {
		return c.rollback(ctx, previousNetwork, previousActive, fmt.Errorf("apply proxy: %w", err))
	}

	active := &activeState{
		snapshot: domain.ActiveSnapshot{DesiredSnapshot: desired, CommittedAt: time.Now().UTC()},
		network:  candidate,
	}
	c.mu.Lock()
	c.active = active
	c.mu.Unlock()
	c.status.RecordActive(desired.ID)
	c.enqueueReport(ApplyReport{SnapshotID: desired.ID, Config: desired.Config, Success: true})
	slog.Info("Router snapshot applied", "snapshot_id", desired.ID, "generation", desired.Generation)
	return nil
}

func (c *Controller) rollback(ctx context.Context, previousNetwork *netcontrol.Candidate, previousActive *activeState, applyErr error) error {
	var rollbackErr error
	if err := c.network.Restore(ctx, previousNetwork); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("network rollback: %w", err))
	}
	if previousActive != nil {
		if err := c.proxy.Apply(ctx, previousActive.snapshot.Config); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("proxy rollback: %w", err))
		}
	}
	if rollbackErr != nil {
		return errors.Join(applyErr, rollbackErr)
	}
	return applyErr
}

func (c *Controller) reportLoop(ctx context.Context) {
	var current *ApplyReport
	var retry uint64
	for {
		if current == nil {
			select {
			case <-ctx.Done():
				return
			case report := <-c.reports:
				current = &report
				retry = 0
			}
		}
		if err := c.portal.Report(ctx, *current); err == nil {
			c.status.ClearReportError()
			current = nil
			continue
		} else {
			retry++
			c.status.RecordReportError(err.Error(), retry)
		}
		timer := time.NewTimer(c.config.ReportRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case report := <-c.reports:
			timer.Stop()
			current = &report
			retry = 0
		case <-timer.C:
		}
	}
}

func (c *Controller) enqueueReport(report ApplyReport) {
	enqueueLatest(c.reports, report)
}

func (c *Controller) isDuplicateActive(desired domain.DesiredSnapshot) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active != nil && c.active.snapshot.ID == desired.ID && c.active.snapshot.Config == desired.Config
}

func (c *Controller) activeCopy() *activeState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.active == nil {
		return nil
	}
	copyState := *c.active
	return &copyState
}

func enqueueLatest[T any](channel chan T, value T) {
	select {
	case channel <- value:
		return
	default:
	}
	select {
	case <-channel:
	default:
	}
	channel <- value
}
