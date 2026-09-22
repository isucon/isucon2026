package reconcile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
	"github.com/isucon/isucon2026/router/internal/netcontrol"
	statusstore "github.com/isucon/isucon2026/router/internal/status"
	routerxds "github.com/isucon/isucon2026/router/internal/xds"
)

type NetworkPlane interface {
	Prepare(domain.Config) (netcontrol.Candidate, error)
	Apply(context.Context, netcontrol.Candidate) error
	Restore(context.Context, *netcontrol.Candidate) error
	Active() *netcontrol.Candidate
}

type RoutePlane interface {
	Publish(context.Context, string, *routev3.RouteConfiguration) error
	Wait(context.Context, string) error
}

type activeState struct {
	snapshot domain.ActiveSnapshot
	network  netcontrol.Candidate
	routes   *routev3.RouteConfiguration
}

type Controller struct {
	config     config.Config
	portal     Portal
	network    NetworkPlane
	routes     RoutePlane
	status     *statusstore.Store
	pending    chan domain.DesiredSnapshot
	reports    chan ApplyReport
	generation atomic.Uint64
	attempt    atomic.Uint64
	mu         sync.RWMutex
	active     *activeState
}

func NewController(cfg config.Config, portal Portal, network NetworkPlane, routes RoutePlane, state *statusstore.Store) *Controller {
	return &Controller{
		config: cfg, portal: portal, network: network, routes: routes, status: state,
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
		}
	}
}

func (c *Controller) reconcileUntilSuccessOrSuperseded(ctx context.Context, desired domain.DesiredSnapshot) {
	var retry uint64
	for {
		if ctx.Err() != nil {
			return
		}
		if c.isDuplicateActive(desired) {
			c.enqueueReport(ApplyReport{SnapshotID: desired.ID, Config: desired.Config, Success: true})
			return
		}
		c.status.StartApply(desired.ID, "validate", retry)
		err := c.apply(ctx, desired)
		if err == nil {
			return
		}
		retry++
		c.status.RecordFailure(desired.ID, "apply-failed", err.Error(), retry)
		c.enqueueReport(ApplyReport{SnapshotID: desired.ID, Reason: err.Error()})
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
	routeConfig, err := routerxds.BuildRoutes(c.config.RouteConfigName, c.config.Hosts, desired.Config, c.config.Traffic)
	if err != nil {
		return fmt.Errorf("build RDS: %w", err)
	}
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

	version := versionFor(desired, "apply", c.attempt.Add(1))
	c.status.SetPhase("xds-publish")
	if err := c.routes.Publish(ctx, version, routeConfig); err != nil {
		return c.rollback(ctx, previousNetwork, previousActive, fmt.Errorf("publish RDS: %w", err))
	}
	c.status.SetPhase("xds-ack")
	ackCtx, cancel := context.WithTimeout(ctx, c.config.XDSAckTimeout)
	err = c.routes.Wait(ackCtx, version)
	cancel()
	if err != nil {
		return c.rollback(ctx, previousNetwork, previousActive, fmt.Errorf("wait for RDS ACK: %w", err))
	}

	active := &activeState{
		snapshot: domain.ActiveSnapshot{DesiredSnapshot: desired, RDSVersion: version, CommittedAt: time.Now().UTC()},
		network:  candidate,
		routes:   routeConfig,
	}
	c.mu.Lock()
	c.active = active
	c.mu.Unlock()
	c.status.RecordActive(desired.ID, version)
	c.enqueueReport(ApplyReport{SnapshotID: desired.ID, Config: desired.Config, Success: true})
	slog.Info("Router snapshot applied", "snapshot_id", desired.ID, "generation", desired.Generation, "rds_version", version)
	return nil
}

func (c *Controller) rollback(ctx context.Context, previousNetwork *netcontrol.Candidate, previousActive *activeState, applyErr error) error {
	var rollbackErr error
	if err := c.network.Restore(ctx, previousNetwork); err != nil {
		rollbackErr = errors.Join(rollbackErr, fmt.Errorf("network rollback: %w", err))
	}
	if previousActive != nil {
		version := versionFor(previousActive.snapshot.DesiredSnapshot, "rollback", c.attempt.Add(1))
		if err := c.routes.Publish(ctx, version, previousActive.routes); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("RDS rollback publish: %w", err))
		} else {
			ackCtx, cancel := context.WithTimeout(ctx, c.config.XDSAckTimeout)
			err := c.routes.Wait(ackCtx, version)
			cancel()
			if err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("RDS rollback ACK: %w", err))
			}
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

func versionFor(snapshot domain.DesiredSnapshot, purpose string, attempt uint64) string {
	digest := sha256.Sum256([]byte(snapshot.ID))
	return fmt.Sprintf("%s-g%d-a%d-%x", purpose, snapshot.Generation, attempt, digest[:6])
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
