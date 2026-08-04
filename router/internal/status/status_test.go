package status

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessTransitions(t *testing.T) {
	t.Parallel()
	store := New()
	if store.Snapshot().Ready {
		t.Fatal("initial state unexpectedly ready")
	}
	store.SetEnvoyConnected(true)
	store.StartApply("one", "network", 0)
	if store.Snapshot().Ready {
		t.Fatal("initial apply unexpectedly ready")
	}
	store.RecordActive("one", "v-one")
	if store.Snapshot().Ready {
		t.Fatal("state became ready before Envoy ACKed on its current stream")
	}
	store.SetEnvoySynced(true)
	if !store.Snapshot().Ready {
		t.Fatal("active ACKed state is not ready")
	}
	store.StartApply("two", "network", 0)
	if !store.Snapshot().Ready {
		t.Fatal("last-known-good became unready during new apply")
	}
	store.RecordFailure("two", "xds-ack", "nack", 1)
	if !store.Snapshot().Ready || store.Snapshot().ActiveSnapshot != "one" {
		t.Fatalf("last-known-good not preserved: %+v", store.Snapshot())
	}
	store.ClearReportError()
	if store.Snapshot().LastError != "nack" || store.Snapshot().Retry != 1 {
		t.Fatalf("successful failure report erased apply diagnostics: %+v", store.Snapshot())
	}
	store.SetEnvoyConnected(false)
	if store.Snapshot().Ready {
		t.Fatal("disconnected Envoy unexpectedly ready")
	}
}

func TestHTTPHealthAndReadiness(t *testing.T) {
	t.Parallel()
	store := New()
	handler := Handler(store)
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want || !strings.Contains(recorder.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s status=%d content-type=%q", path, recorder.Code, recorder.Header().Get("Content-Type"))
		}
	}
	store.SetEnvoyConnected(true)
	store.SetEnvoySynced(true)
	store.RecordActive("snapshot", "version")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"active_snapshot_id":"snapshot"`) {
		t.Fatalf("ready response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
