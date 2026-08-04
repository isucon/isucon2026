package xds

import (
	"context"
	"fmt"
	"sync"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
)

type NACKError struct {
	Version string
	Reason  string
}

func (e *NACKError) Error() string {
	return fmt.Sprintf("Envoy NACK for %s: %s", e.Version, e.Reason)
}

type ackOutcome struct {
	err error
}

type ACKTracker struct {
	mu                  sync.Mutex
	streams             map[int64]struct{}
	syncedStreams       map[int64]struct{}
	nonceVersions       map[int64]map[string]string
	outcomes            map[string]ackOutcome
	waiters             map[string][]chan error
	onConnectionChanged func(bool)
	onSyncChanged       func(bool)
}

func NewACKTracker(onConnectionChanged, onSyncChanged func(bool)) *ACKTracker {
	return &ACKTracker{
		streams:             make(map[int64]struct{}),
		syncedStreams:       make(map[int64]struct{}),
		nonceVersions:       make(map[int64]map[string]string),
		outcomes:            make(map[string]ackOutcome),
		waiters:             make(map[string][]chan error),
		onConnectionChanged: onConnectionChanged,
		onSyncChanged:       onSyncChanged,
	}
}

func (t *ACKTracker) Callbacks() serverv3.Callbacks {
	return serverv3.CallbackFuncs{
		StreamOpenFunc: func(ctx context.Context, streamID int64, typeURL string) error {
			return t.onOpen(ctx, streamID, typeURL)
		},
		StreamClosedFunc: func(streamID int64, _ *corev3.Node) { t.onClose(streamID) },
		StreamRequestFunc: func(streamID int64, request *discoveryv3.DiscoveryRequest) error {
			return t.onRequest(streamID, request)
		},
		StreamResponseFunc: func(_ context.Context, streamID int64, _ *discoveryv3.DiscoveryRequest, response *discoveryv3.DiscoveryResponse) {
			t.onResponse(streamID, response)
		},
	}
}

func (t *ACKTracker) Connected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.streams) > 0
}

func (t *ACKTracker) Wait(ctx context.Context, version string) error {
	if version == "" {
		return fmt.Errorf("xDS version is required")
	}
	t.mu.Lock()
	if outcome, ok := t.outcomes[version]; ok {
		t.mu.Unlock()
		return outcome.err
	}
	waiter := make(chan error, 1)
	t.waiters[version] = append(t.waiters[version], waiter)
	t.mu.Unlock()

	select {
	case err := <-waiter:
		return err
	case <-ctx.Done():
		t.mu.Lock()
		waiters := t.waiters[version]
		for index, candidate := range waiters {
			if candidate == waiter {
				t.waiters[version] = append(waiters[:index], waiters[index+1:]...)
				break
			}
		}
		if len(t.waiters[version]) == 0 {
			delete(t.waiters, version)
		}
		t.mu.Unlock()
		return ctx.Err()
	}
}

func (t *ACKTracker) Forget(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.outcomes, version)
}

func (t *ACKTracker) onOpen(_ context.Context, streamID int64, _ string) error {
	t.mu.Lock()
	wasConnected := len(t.streams) > 0
	t.streams[streamID] = struct{}{}
	t.nonceVersions[streamID] = make(map[string]string)
	isConnected := len(t.streams) > 0
	t.mu.Unlock()
	if !wasConnected && isConnected && t.onConnectionChanged != nil {
		t.onConnectionChanged(true)
	}
	return nil
}

func (t *ACKTracker) onClose(streamID int64) {
	t.mu.Lock()
	wasConnected := len(t.streams) > 0
	wasSynced := len(t.syncedStreams) > 0
	delete(t.streams, streamID)
	delete(t.syncedStreams, streamID)
	delete(t.nonceVersions, streamID)
	isConnected := len(t.streams) > 0
	isSynced := len(t.syncedStreams) > 0
	t.mu.Unlock()
	if wasConnected && !isConnected && t.onConnectionChanged != nil {
		t.onConnectionChanged(false)
	}
	if wasSynced && !isSynced && t.onSyncChanged != nil {
		t.onSyncChanged(false)
	}
}

func (t *ACKTracker) onResponse(streamID int64, response *discoveryv3.DiscoveryResponse) {
	if response == nil || response.TypeUrl != resource.RouteType || response.Nonce == "" || response.VersionInfo == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	stream, ok := t.nonceVersions[streamID]
	if !ok {
		return
	}
	stream[response.Nonce] = response.VersionInfo
}

func (t *ACKTracker) onRequest(streamID int64, request *discoveryv3.DiscoveryRequest) error {
	if request == nil || request.TypeUrl != resource.RouteType || request.ResponseNonce == "" {
		return nil
	}
	t.mu.Lock()
	stream, ok := t.nonceVersions[streamID]
	if !ok {
		t.mu.Unlock()
		return nil
	}
	version, ok := stream[request.ResponseNonce]
	if !ok {
		t.mu.Unlock()
		return nil
	}
	delete(stream, request.ResponseNonce)
	var result error
	if request.ErrorDetail != nil {
		reason := request.ErrorDetail.Message
		if len(reason) > 512 {
			reason = reason[:512]
		}
		result = &NACKError{Version: version, Reason: reason}
	} else if request.VersionInfo != version {
		t.mu.Unlock()
		return nil
	}
	wasSynced := len(t.syncedStreams) > 0
	if result == nil {
		t.syncedStreams[streamID] = struct{}{}
	}
	isSynced := len(t.syncedStreams) > 0
	t.completeLocked(version, result)
	t.mu.Unlock()
	if !wasSynced && isSynced && t.onSyncChanged != nil {
		t.onSyncChanged(true)
	}
	return nil
}

func (t *ACKTracker) completeLocked(version string, err error) {
	t.outcomes[version] = ackOutcome{err: err}
	for _, waiter := range t.waiters[version] {
		waiter <- err
		close(waiter)
	}
	delete(t.waiters, version)
}
