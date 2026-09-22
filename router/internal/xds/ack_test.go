package xds

import (
	"context"
	"errors"
	"testing"
	"time"

	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
)

func TestACKTrackerACK(t *testing.T) {
	t.Parallel()
	tracker := NewACKTracker(nil, nil)
	if err := tracker.onOpen(context.Background(), 3, resource.RouteType); err != nil {
		t.Fatal(err)
	}
	tracker.onResponse(3, &discoveryv3.DiscoveryResponse{TypeUrl: resource.RouteType, VersionInfo: "v1", Nonce: "n1"})
	if err := tracker.onRequest(3, &discoveryv3.DiscoveryRequest{TypeUrl: resource.RouteType, VersionInfo: "v1", ResponseNonce: "n1"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := tracker.Wait(ctx, "v1"); err != nil {
		t.Fatalf("Wait ACK: %v", err)
	}
}

func TestACKTrackerNACK(t *testing.T) {
	t.Parallel()
	tracker := NewACKTracker(nil, nil)
	if err := tracker.onOpen(context.Background(), 4, resource.RouteType); err != nil {
		t.Fatal(err)
	}
	tracker.onResponse(4, &discoveryv3.DiscoveryResponse{TypeUrl: resource.RouteType, VersionInfo: "v2", Nonce: "n2"})
	if err := tracker.onRequest(4, &discoveryv3.DiscoveryRequest{
		TypeUrl: resource.RouteType, VersionInfo: "v1", ResponseNonce: "n2",
		ErrorDetail: &statuspb.Status{Code: 3, Message: "bad route"},
	}); err != nil {
		t.Fatal(err)
	}
	err := tracker.Wait(context.Background(), "v2")
	var nack *NACKError
	if !errors.As(err, &nack) || nack.Version != "v2" || nack.Reason != "bad route" {
		t.Fatalf("unexpected NACK: %#v", err)
	}
}

func TestACKTrackerTimeoutAndStaleVersion(t *testing.T) {
	t.Parallel()
	tracker := NewACKTracker(nil, nil)
	if err := tracker.onOpen(context.Background(), 1, resource.RouteType); err != nil {
		t.Fatal(err)
	}
	tracker.onResponse(1, &discoveryv3.DiscoveryResponse{TypeUrl: resource.RouteType, VersionInfo: "old", Nonce: "old-nonce"})
	_ = tracker.onRequest(1, &discoveryv3.DiscoveryRequest{TypeUrl: resource.RouteType, VersionInfo: "old", ResponseNonce: "old-nonce"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := tracker.Wait(ctx, "new"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want deadline exceeded", err)
	}
}

func TestACKTrackerConnectionLifecycle(t *testing.T) {
	t.Parallel()
	changes := make(chan bool, 2)
	syncChanges := make(chan bool, 2)
	tracker := NewACKTracker(func(connected bool) { changes <- connected }, func(synced bool) { syncChanges <- synced })
	if err := tracker.onOpen(context.Background(), 9, resource.AnyType); err != nil {
		t.Fatal(err)
	}
	if !tracker.Connected() || !<-changes {
		t.Fatal("tracker did not become connected")
	}
	tracker.onResponse(9, &discoveryv3.DiscoveryResponse{TypeUrl: resource.RouteType, VersionInfo: "v", Nonce: "n"})
	if err := tracker.onRequest(9, &discoveryv3.DiscoveryRequest{TypeUrl: resource.RouteType, VersionInfo: "v", ResponseNonce: "n"}); err != nil {
		t.Fatal(err)
	}
	if !<-syncChanges {
		t.Fatal("tracker did not become synced after ACK")
	}
	tracker.onClose(9)
	if tracker.Connected() || <-changes {
		t.Fatal("tracker did not become disconnected")
	}
	if <-syncChanges {
		t.Fatal("tracker did not become unsynced after close")
	}
	tracker.onResponse(9, &discoveryv3.DiscoveryResponse{TypeUrl: resource.RouteType, VersionInfo: "closed", Nonce: "closed-nonce"})
	_ = tracker.onRequest(9, &discoveryv3.DiscoveryRequest{TypeUrl: resource.RouteType, VersionInfo: "closed", ResponseNonce: "closed-nonce"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := tracker.Wait(ctx, "closed"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("closed stream nonce unexpectedly completed: %v", err)
	}
}
