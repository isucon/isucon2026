package status

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStoreKeepsLastKnownGood(t *testing.T) {
	t.Parallel()
	store := New()
	store.StartApply("one", "network", 0)
	store.RecordActive("one")
	store.StartApply("two", "network", 0)
	store.RecordFailure("two", "apply-failed", "proxy unavailable", 1)
	if state := store.Snapshot(); state.ActiveSnapshot != "one" || state.DesiredSnapshot != "two" {
		t.Fatalf("last-known-good not preserved: %+v", state)
	}
	store.ClearReportError()
	if state := store.Snapshot(); state.LastError != "proxy unavailable" || state.Retry != 1 {
		t.Fatalf("successful failure report erased apply diagnostics: %+v", state)
	}
}

func TestReadinessRequiresActiveSnapshotServedByProxy(t *testing.T) {
	t.Parallel()
	store := New()
	var probeErr error
	probes := 0
	handler := Handler(store, func(context.Context) error { probes++; return probeErr })
	readyz := func() (int, string) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if !strings.Contains(recorder.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("content-type=%q", recorder.Header().Get("Content-Type"))
		}
		return recorder.Code, recorder.Body.String()
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz status=%d", recorder.Code)
	}
	if code, _ := readyz(); code != http.StatusServiceUnavailable || probes != 0 {
		t.Fatalf("ready without a snapshot: status=%d probes=%d", code, probes)
	}
	store.RecordActive("snapshot")
	if code, body := readyz(); code != http.StatusOK || !strings.Contains(body, `"active_snapshot_id":"snapshot"`) || !strings.Contains(body, `"proxy_serving":true`) {
		t.Fatalf("ready response status=%d body=%s", code, body)
	}
	probeErr = errors.New("connection refused")
	if code, body := readyz(); code != http.StatusServiceUnavailable || !strings.Contains(body, `"proxy_error":"connection refused"`) {
		t.Fatalf("stopped proxy still ready: status=%d body=%s", code, body)
	}
}
