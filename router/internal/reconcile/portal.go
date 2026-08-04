package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/gen/go/portal/v1/portalv1connect"
	"github.com/isucon/isucon2026/router/internal/domain"
)

type ApplyReport struct {
	SnapshotID string
	Config     domain.Config
	Success    bool
	Reason     string
}

type Portal interface {
	Stream(context.Context, func(*portalv1.ConfigSnapshot) error, func(bool)) error
	Report(context.Context, ApplyReport) error
}

type PortalClient struct {
	teamNumber uint32
	client     portalv1connect.PortalRouterServiceClient
	minBackoff time.Duration
	maxBackoff time.Duration
}

func NewPortalClient(endpoint string, teamNumber uint32, httpClient *http.Client, minBackoff, maxBackoff time.Duration) (*PortalClient, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("Portal endpoint must be an absolute HTTP(S) URL")
	}
	if teamNumber == 0 {
		return nil, errors.New("team number must be positive")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if minBackoff <= 0 || maxBackoff < minBackoff {
		return nil, errors.New("invalid Portal reconnect backoff")
	}
	return &PortalClient{
		teamNumber: teamNumber,
		client:     portalv1connect.NewPortalRouterServiceClient(httpClient, endpoint),
		minBackoff: minBackoff,
		maxBackoff: maxBackoff,
	}, nil
}

func (p *PortalClient) Stream(ctx context.Context, receive func(*portalv1.ConfigSnapshot) error, connectionChanged func(bool)) error {
	if receive == nil {
		return errors.New("snapshot receiver is required")
	}
	backoff := p.minBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stream, err := p.client.StreamDesired(ctx, connect.NewRequest(&portalv1.StreamDesiredRequest{
			Team: &portalv1.TeamKey{TeamNumber: p.teamNumber},
		}))
		if err != nil {
			if err := waitBackoff(ctx, backoff); err != nil {
				return err
			}
			backoff = min(backoff*2, p.maxBackoff)
			continue
		}
		if connectionChanged != nil {
			connectionChanged(true)
		}
		receivedAny := false
		for stream.Receive() {
			message := stream.Msg()
			if message == nil || message.Desired == nil {
				continue
			}
			receivedAny = true
			if err := receive(message.Desired); err != nil {
				_ = stream.Close()
				if connectionChanged != nil {
					connectionChanged(false)
				}
				return err
			}
		}
		streamErr := stream.Err()
		_ = stream.Close()
		if connectionChanged != nil {
			connectionChanged(false)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if receivedAny {
			backoff = p.minBackoff
		}
		_ = streamErr
		if err := waitBackoff(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, p.maxBackoff)
	}
}

func (p *PortalClient) Report(ctx context.Context, report ApplyReport) error {
	if report.SnapshotID == "" {
		return errors.New("report snapshot ID is required")
	}
	request := &portalv1.ReportAppliedRequest{Team: &portalv1.TeamKey{TeamNumber: p.teamNumber}}
	if report.Success {
		if err := report.Config.Validate(); err != nil {
			return fmt.Errorf("success report config: %w", err)
		}
		request.Result = &portalv1.ReportAppliedRequest_Success{Success: &portalv1.ApplySuccess{
			SnapshotId: report.SnapshotID,
			Applied:    report.Config.Proto(),
		}}
	} else {
		reason := report.Reason
		if reason == "" {
			reason = "Router apply failed"
		}
		if len(reason) > 512 {
			reason = reason[:512]
		}
		request.Result = &portalv1.ReportAppliedRequest_Failure{Failure: &portalv1.ApplyFailure{
			SnapshotId: report.SnapshotID,
			Reason:     reason,
		}}
	}
	_, err := p.client.ReportApplied(ctx, connect.NewRequest(request))
	if err != nil {
		return fmt.Errorf("report applied: %w", err)
	}
	return nil
}

func waitBackoff(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
