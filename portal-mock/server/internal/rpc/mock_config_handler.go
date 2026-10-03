package rpc

import (
	"context"

	"connectrpc.com/connect"
	portalmockv1 "github.com/isucon/isucon2026/gen/go/portal_mock/v1"
	"github.com/isucon/isucon2026/portal-mock/server/internal/benchmark"
	"github.com/isucon/isucon2026/portal-mock/server/internal/domain"
	"github.com/isucon/isucon2026/portal-mock/server/internal/store"
)

type MockHandler struct {
	store     *store.Store
	benchmark *benchmark.Runner
}

func NewMockHandler(stateStore *store.Store, benchmarkRunner *benchmark.Runner) *MockHandler {
	return &MockHandler{store: stateStore, benchmark: benchmarkRunner}
}

func (h *MockHandler) GetState(
	context.Context,
	*connect.Request[portalmockv1.GetStateRequest],
) (*connect.Response[portalmockv1.GetStateResponse], error) {
	return connect.NewResponse(&portalmockv1.GetStateResponse{
		State: stateToProto(h.store.Snapshot()),
	}), nil
}

func (h *MockHandler) StreamState(
	ctx context.Context,
	_ *connect.Request[portalmockv1.StreamStateRequest],
	stream *connect.ServerStream[portalmockv1.StreamStateResponse],
) error {
	subscription := h.store.SubscribeState(ctx)
	defer subscription.Cancel()
	for {
		select {
		case <-ctx.Done():
			return connect.NewError(connect.CodeCanceled, ctx.Err())
		case state := <-subscription.C:
			if err := stream.Send(&portalmockv1.StreamStateResponse{State: stateToProto(state)}); err != nil {
				if ctx.Err() != nil {
					return connect.NewError(connect.CodeCanceled, ctx.Err())
				}
				return err
			}
		}
	}
}

func (h *MockHandler) ListSupportedCountries(
	context.Context,
	*connect.Request[portalmockv1.ListSupportedCountriesRequest],
) (*connect.Response[portalmockv1.ListSupportedCountriesResponse], error) {
	countries := domain.SupportedCountries()
	response := &portalmockv1.ListSupportedCountriesResponse{
		Countries: make([]*portalmockv1.SupportedCountry, 0, len(countries)),
	}
	for _, country := range countries {
		response.Countries = append(response.Countries, countryToProto(country))
	}
	return connect.NewResponse(response), nil
}

func (h *MockHandler) UpdateDesired(
	_ context.Context,
	request *connect.Request[portalmockv1.UpdateDesiredRequest],
) (*connect.Response[portalmockv1.UpdateDesiredResponse], error) {
	var protoPatch *portalmockv1.DesiredPatch
	if request != nil && request.Msg != nil {
		protoPatch = request.Msg.Patch
	}
	if protoPatch == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, store.ErrInvalidArgument)
	}
	patch := store.Patch{
		App1Region:     protoPatch.App1Region,
		App2Region:     protoPatch.App2Region,
		App3Region:     protoPatch.App3Region,
		DefaultCountry: protoPatch.DefaultCountry,
	}
	changed, desired, diagnostic, err := h.store.UpdateDesired(patch)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&portalmockv1.UpdateDesiredResponse{
		Changed: changed,
		Desired: mockSnapshotToProto(desired),
		Apply:   diagnosticToProto(diagnostic),
	}), nil
}

func (h *MockHandler) StartBenchmark(
	context.Context,
	*connect.Request[portalmockv1.StartBenchmarkRequest],
) (*connect.Response[portalmockv1.StartBenchmarkResponse], error) {
	runtime, err := h.benchmark.Start()
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&portalmockv1.StartBenchmarkResponse{
		Benchmark: benchmarkRuntimeToProto(runtime),
	}), nil
}

func (h *MockHandler) Reset(
	context.Context,
	*connect.Request[portalmockv1.ResetRequest],
) (*connect.Response[portalmockv1.ResetResponse], error) {
	return connect.NewResponse(&portalmockv1.ResetResponse{
		State: stateToProto(h.store.Reset()),
	}), nil
}
