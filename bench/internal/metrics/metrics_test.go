package metrics_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

var start = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

func op(at time.Duration, country, route string, err error) client.Record {
	return client.Record{Route: route, Country: country, Start: start.Add(at), Duration: 10 * time.Millisecond, Err: err}
}

func TestWindowsAndRegions(t *testing.T) {
	recorder := metrics.New(start, 5*time.Second)
	failure := errors.New("boom")
	recorder.Observe(op(time.Second, "JP", "GET /api/me", nil))
	recorder.Observe(op(2*time.Second, "KR", "POST /api/posts", failure))
	recorder.Observe(op(3*time.Second, "DE", "GET /api/me", nil))
	recorder.Observe(op(6*time.Second, "US", "GET /api/me", nil))
	recorder.Observe(op(7*time.Second, "", "POST /api/admin/posting-window", nil))

	if got := recorder.Windows(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("windows=%v", got)
	}
	regions := recorder.Regions(0)
	if len(regions) != 2 || regions[0].Region != "asia" || regions[0].Ops != 2 || regions[0].Failures != 1 || regions[0].Reads != 1 || regions[1].Region != "europe" {
		t.Fatalf("regions=%+v", regions)
	}
	if regions := recorder.Regions(1); len(regions) != 2 || regions[0].Region != "america" || regions[1].Region != metrics.UnknownRegion {
		t.Fatalf("regions=%+v", regions)
	}
	if total := recorder.Total(0); total.Ops != 3 || total.Failures != 1 {
		t.Fatalf("total=%+v", total)
	}
}

func TestReadP99(t *testing.T) {
	recorder := metrics.New(start, 5*time.Second)
	for i := range 100 {
		recorder.Observe(client.Record{Route: "GET /api/feeds/public", Country: "JP", Start: start, Duration: time.Duration(i+1) * time.Millisecond})
	}
	// 書き込みと失敗した読み取りは、読み取りレイテンシの標本にしない。
	recorder.Observe(client.Record{Route: "POST /api/posts", Country: "JP", Start: start, Duration: time.Second})
	recorder.Observe(client.Record{Route: "GET /api/me", Country: "JP", Start: start, Duration: time.Second, Err: errors.New("timeout"), Timeout: true})
	stats := recorder.Regions(0)[0]
	if stats.ReadP99 != 99*time.Millisecond || stats.Reads != 100 || stats.Ops != 102 || stats.Timeouts != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestViolation(t *testing.T) {
	slo := metrics.SLO{FailRate: 0.1, MinOps: 10}
	failure := errors.New("boom")

	recorder := metrics.New(start, 5*time.Second)
	// 観測窓 0: 操作数が少なすぎて判定しない。
	for range 5 {
		recorder.Observe(op(time.Second, "JP", "GET /api/me", failure))
	}
	// 観測窓 1: 10 操作中 1 件の失敗は SLO の範囲内。
	for i := range 10 {
		var err error
		if i == 0 {
			err = failure
		}
		recorder.Observe(op(6*time.Second, "DE", "GET /api/me", err))
	}
	if reason := recorder.Violation(slo, 2); reason != "" {
		t.Fatalf("unexpected violation: %s", reason)
	}
	// 検証の失敗は観測窓 1 の分子に足される。
	recorder.AddFailure("europe", start.Add(7*time.Second))
	if reason := recorder.Violation(slo, 1); reason != "" {
		t.Fatalf("open window must not be judged: %s", reason)
	}
	if reason := recorder.Violation(slo, 2); !strings.Contains(reason, "20.0%") || !strings.Contains(reason, "5s〜10s") {
		t.Fatalf("reason=%q", reason)
	}
}

// TestSealCountsInFlight は、観測窓を閉じたときに処理中の操作を、窓の終わりまでの経過時間で
// p99 に含めることを確かめる。
func TestSealCountsInFlight(t *testing.T) {
	start := time.Now()
	r := metrics.New(start, 5*time.Second)
	for i := range 50 {
		r.Observe(client.Record{Route: "GET /api/feeds/:feed", Country: "JP", Start: start.Add(time.Duration(i) * time.Millisecond), Duration: 10 * time.Millisecond})
	}
	stuck := r.Begin("POST /api/auth/register", "JP", start.Add(time.Second))
	done := r.Begin("GET /api/me", "JP", start.Add(2*time.Second))
	done()
	later := r.Begin("GET /api/me", "JP", start.Add(6*time.Second))
	defer later()
	r.Seal(0)
	r.Seal(0)
	s := r.Regions(0)[0]
	if s.Ops != 50 || s.OpP99 != 4*time.Second || s.ReadP99 != 10*time.Millisecond {
		t.Fatalf("stats = %+v, want 50 ops, op p99 4s (in flight since 1s), read p99 10ms", s)
	}
	stuck()
}
