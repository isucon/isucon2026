package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	portalv1 "github.com/isucon/isucon2026/gen/go/portal/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

type RouterHandler struct {
	store *store.Store
}

func NewRouterHandler(stateStore *store.Store) *RouterHandler {
	return &RouterHandler{store: stateStore}
}

func (h *RouterHandler) StreamDesired(
	ctx context.Context,
	request *connect.Request[portalv1.StreamDesiredRequest],
	stream *connect.ServerStream[portalv1.StreamDesiredResponse],
) error {
	if request == nil || request.Msg == nil || request.Msg.Team == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("team is required"))
	}
	if err := teamError(request.Msg.Team.TeamNumber, h.store.Team().Number); err != nil {
		return err
	}
	subscription := h.store.SubscribeDesired(ctx)
	defer subscription.Cancel()
	for {
		select {
		case <-ctx.Done():
			return connect.NewError(connect.CodeCanceled, ctx.Err())
		case snapshot := <-subscription.C:
			if err := stream.Send(&portalv1.StreamDesiredResponse{
				Desired: routerSnapshotToProto(snapshot),
			}); err != nil {
				if ctx.Err() != nil {
					return connect.NewError(connect.CodeCanceled, ctx.Err())
				}
				return err
			}
		}
	}
}

func (h *RouterHandler) ReportApplied(
	_ context.Context,
	request *connect.Request[portalv1.ReportAppliedRequest],
) (*connect.Response[portalv1.ReportAppliedResponse], error) {
	if request == nil || request.Msg == nil || request.Msg.Team == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("team is required"))
	}
	if err := teamError(request.Msg.Team.TeamNumber, h.store.Team().Number); err != nil {
		return nil, err
	}
	switch result := request.Msg.Result.(type) {
	case *portalv1.ReportAppliedRequest_Success:
		if result.Success == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("success is required"))
		}
		cfg, err := configFromProto(result.Success.Applied)
		if err != nil {
			if reportErr := h.store.ReportSuccess(result.Success.SnapshotId, cfg); reportErr != nil {
				return nil, connectError(reportErr)
			}
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if err := h.store.ReportSuccess(result.Success.SnapshotId, cfg); err != nil {
			return nil, connectError(err)
		}
	case *portalv1.ReportAppliedRequest_Failure:
		if result.Failure == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("failure is required"))
		}
		if err := h.store.ReportFailure(result.Failure.SnapshotId, result.Failure.Reason); err != nil {
			return nil, connectError(err)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("success or failure result is required"))
	}
	return connect.NewResponse(&portalv1.ReportAppliedResponse{}), nil
}
