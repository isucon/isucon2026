// Package control serves the fixed competition configuration to a local Router.
package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/gen/go/portal/v1/portalv1connect"
	"github.com/isucon/isucon2026/router/internal/domain"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Config struct {
	TeamNumber     uint32
	ListenAddress  string
	DefaultCountry string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	team, err := strconv.ParseUint(getenv("ROUTER_CONTROL_TEAM_NUMBER"), 10, 32)
	if err != nil {
		return Config{}, fmt.Errorf("ROUTER_CONTROL_TEAM_NUMBER: %w", err)
	}
	cfg := Config{TeamNumber: uint32(team), ListenAddress: "127.0.0.1:18082", DefaultCountry: "JP"}
	if v := getenv("ROUTER_CONTROL_LISTEN_ADDRESS"); v != "" {
		cfg.ListenAddress = v
	}
	if v := getenv("ROUTER_CONTROL_DEFAULT_COUNTRY"); v != "" {
		cfg.DefaultCountry = v
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.TeamNumber == 0 {
		return errors.New("team number must be positive")
	}
	host, port, err := net.SplitHostPort(c.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("Router control must listen on a loopback IP address")
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return errors.New("listen port must be between 1 and 65535")
	}
	return c.desired().Validate()
}

func (c Config) desired() domain.Config {
	return domain.Config{App1Region: domain.RegionAP, App2Region: domain.RegionEU, App3Region: domain.RegionUS, DefaultCountry: c.DefaultCountry}
}

type Service struct {
	team     uint32
	snapshot *portalv1.ConfigSnapshot
	logger   *slog.Logger
}

func New(cfg Config, logger *slog.Logger) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{team: cfg.TeamNumber, logger: logger, snapshot: &portalv1.ConfigSnapshot{
		SnapshotId: rand.Text(), Config: cfg.desired().Proto(), IssuedAt: timestamppb.New(time.Now()),
	}}, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	// Preserve the wire protocol consumed by existing Router clients.
	path, handler := portalv1connect.NewPortalRouterServiceHandler(s)
	mux.Handle(path, handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func (s *Service) checkTeam(team *portalv1.TeamKey) error {
	if team == nil || team.TeamNumber == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("positive team_number is required"))
	}
	if team.TeamNumber != s.team {
		return connect.NewError(connect.CodeNotFound, errors.New("team was not found"))
	}
	return nil
}

func (s *Service) StreamDesired(ctx context.Context, req *connect.Request[portalv1.StreamDesiredRequest], stream *connect.ServerStream[portalv1.StreamDesiredResponse]) error {
	if err := s.checkTeam(req.Msg.Team); err != nil {
		return err
	}
	if err := stream.Send(&portalv1.StreamDesiredResponse{Desired: s.snapshot}); err != nil {
		return err
	}
	// Settings are immutable during a process lifetime. Restart to publish a new snapshot.
	<-ctx.Done()
	return ctx.Err()
}

func (s *Service) ReportApplied(_ context.Context, req *connect.Request[portalv1.ReportAppliedRequest]) (*connect.Response[portalv1.ReportAppliedResponse], error) {
	if err := s.checkTeam(req.Msg.Team); err != nil {
		return nil, err
	}
	switch result := req.Msg.Result.(type) {
	case *portalv1.ReportAppliedRequest_Success:
		if result.Success == nil || result.Success.Applied == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("applied config is required"))
		}
		if result.Success.SnapshotId != s.snapshot.SnapshotId || !proto.Equal(result.Success.Applied, s.snapshot.Config) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("report does not match desired snapshot"))
		}
		s.logger.Info("Router configuration applied", "team", s.team, "snapshot", s.snapshot.SnapshotId)
	case *portalv1.ReportAppliedRequest_Failure:
		if result.Failure == nil || result.Failure.Reason == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("failure reason is required"))
		}
		if result.Failure.SnapshotId != s.snapshot.SnapshotId {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("report does not match desired snapshot"))
		}
		s.logger.Error("Router configuration failed", "team", s.team, "snapshot", s.snapshot.SnapshotId, "reason", result.Failure.Reason)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("success or failure result is required"))
	}
	return connect.NewResponse(&portalv1.ReportAppliedResponse{}), nil
}
