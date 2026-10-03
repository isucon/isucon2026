package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/httpapi"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/labstack/echo/v5"
)

type fakeOffsetStore struct {
	mu     sync.Mutex
	saved  []time.Duration
	failed error
}

func (f *fakeOffsetStore) SaveVirtualOffset(_ context.Context, offset time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed != nil {
		return f.failed
	}
	f.saved = append(f.saved, offset)
	return nil
}

func TestInitializeHandler(t *testing.T) {
	pool := testkit.Database(t)
	_, filename, _, _ := runtime.Caller(0)
	t.Setenv("ISUREAL_SCHEMA_PATH", filepath.Join(filepath.Dir(filename), "..", "..", "schema", "schema.sql"))
	clk := clock.NewVirtual()
	store := &fakeOffsetStore{}
	handler := httpapi.NewInitializeHandler(pool, clk, store)

	harness := testkit.NewHTTPHarness(func(e *echo.Echo) {
		e.POST("/api/v1/initialize", handler.Initialize)
	})

	headers := http.Header{"Content-Type": {"application/json"}}
	resp := harness.Do("POST", "/api/v1/initialize", nil, headers)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var body httpapi.InitializeResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Language != "go" {
		t.Fatalf("language = %q, want %q", body.Language, "go")
	}
	if body.BaseTime.IsZero() {
		t.Fatal("base_time is zero")
	}

	diff := clk.Now().Sub(body.BaseTime).Abs()
	const tolerance = time.Second
	if diff > tolerance {
		t.Fatalf("clock not set to base_time: Now()=%v, base_time=%v, diff=%v", clk.Now(), body.BaseTime, diff)
	}
	if len(store.saved) != 1 {
		t.Fatalf("offset saves = %d, want 1", len(store.saved))
	}
	if store.saved[0] != clk.Offset() {
		t.Fatalf("saved offset = %v, want %v", store.saved[0], clk.Offset())
	}
}

func TestVirtualTimeAdvanceHandler(t *testing.T) {
	clk := clock.NewVirtual()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	clk.SetBaseTime(base)
	store := &fakeOffsetStore{}
	handler := httpapi.NewInitializeHandler(nil, clk, store)

	harness := testkit.NewHTTPHarness(func(e *echo.Echo) {
		e.POST("/api/v1/virtual-time/advance", handler.AdvanceTime)
	})

	body, _ := json.Marshal(httpapi.VirtualTimeAdvanceRequest{OffsetSeconds: 86400})
	headers := http.Header{"Content-Type": {"application/json"}}
	resp := harness.Do("POST", "/api/v1/virtual-time/advance", body, headers)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result httpapi.VirtualTimeAdvanceResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	want := base.Add(24 * time.Hour)
	diff := result.CurrentTime.Sub(want).Abs()
	const tolerance = time.Second
	if diff > tolerance {
		t.Fatalf("current_time = %v, want within %v of %v", result.CurrentTime, tolerance, want)
	}
	if len(store.saved) != 1 {
		t.Fatalf("offset saves = %d, want 1", len(store.saved))
	}
	if store.saved[0] != clk.Offset() {
		t.Fatalf("saved offset = %v, want %v", store.saved[0], clk.Offset())
	}
}
