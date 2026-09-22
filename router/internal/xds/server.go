package xds

import (
	"context"
	"fmt"
	"net"
	"sync"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	routegrpc "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"
)

type ManagementServer struct {
	nodeID  string
	cache   cachev3.SnapshotCache
	tracker *ACKTracker
	grpc    *grpc.Server
	mu      sync.Mutex
	lis     net.Listener
}

func NewManagementServer(nodeID string, onConnectionChanged, onSyncChanged func(bool)) (*ManagementServer, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("Envoy node ID is required")
	}
	tracker := NewACKTracker(onConnectionChanged, onSyncChanged)
	cache := cachev3.NewSnapshotCache(true, cachev3.IDHash{}, nil)
	xdsServer := serverv3.NewServer(context.Background(), cache, tracker.Callbacks())
	grpcServer := grpc.NewServer()
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(grpcServer, xdsServer)
	routegrpc.RegisterRouteDiscoveryServiceServer(grpcServer, xdsServer)
	return &ManagementServer{nodeID: nodeID, cache: cache, tracker: tracker, grpc: grpcServer}, nil
}

func (s *ManagementServer) Start(ctx context.Context, address string) error {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen for xDS: %w", err)
	}
	s.mu.Lock()
	s.lis = lis
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.grpc.GracefulStop()
	}()
	go func() {
		_ = s.grpc.Serve(lis)
	}()
	return nil
}

func (s *ManagementServer) Publish(ctx context.Context, version string, routes *routev3.RouteConfiguration) error {
	if version == "" || routes == nil {
		return fmt.Errorf("xDS version and routes are required")
	}
	snapshot, err := cachev3.NewSnapshot(version, map[resource.Type][]types.Resource{
		resource.RouteType: {routes},
	})
	if err != nil {
		return fmt.Errorf("build xDS snapshot: %w", err)
	}
	s.tracker.Forget(version)
	if err := s.cache.SetSnapshot(ctx, s.nodeID, snapshot); err != nil {
		return fmt.Errorf("publish xDS snapshot: %w", err)
	}
	return nil
}

func (s *ManagementServer) Wait(ctx context.Context, version string) error {
	return s.tracker.Wait(ctx, version)
}

func (s *ManagementServer) Connected() bool {
	return s.tracker.Connected()
}

func (s *ManagementServer) Address() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lis == nil {
		return ""
	}
	return s.lis.Addr().String()
}
