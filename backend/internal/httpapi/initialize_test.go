package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
	"github.com/isucon/isucon2026/backend/internal/httpapi"
	"github.com/isucon/isucon2026/backend/tests/testkit"
	"github.com/labstack/echo/v5"
)

func TestInitializeHandler(t *testing.T) {
	pool := testkit.Database(t)
	clk := clock.NewVirtual()
	handler := httpapi.NewInitializeHandler(pool, clk)

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
}

func TestVirtualTimeAdvanceHandler(t *testing.T) {
	clk := clock.NewVirtual()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	clk.SetBaseTime(base)
	handler := httpapi.NewInitializeHandler(nil, clk)

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
}
