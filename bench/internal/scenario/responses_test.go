package scenario

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// observeAll は u の操作を打ち切りの判定と成長の判定の両方に数える計測を始め、記録先を返す。
func observeAll(r *Runner, u *user, base string) *metrics.Recorder {
	rec := metrics.New(time.Now(), time.Minute)
	r.recorder.Store(rec)
	r.books.Store(newBook(r.tt, time.Now()))
	u.API = client.New(base).WithCountry(u.Country()).WithObserver(&accounting{r: r, slo: true, growth: true})
	return rec
}

// routeFailures は rec のルート route の操作数と失敗数（全リージョンの合計）。
func routeFailures(rec *metrics.Recorder, route string) (ops, failures int64) {
	for _, rs := range rec.Routes() {
		if rs.Route == route {
			ops += rs.Ops
			failures += rs.Failures
		}
	}
	return ops, failures
}

// TestMalformedJSONCountsAsFailure は、200 で JSON として読めない応答を、打ち切りのエラー率、
// ルートごとの集計、成長の判定のそれぞれに 1 件の失敗として数えることを確かめる（二重に
// 数えない）。
func TestMalformedJSONCountsAsFailure(t *testing.T) {
	r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":`)
	}))
	defer server.Close()
	u := r.usersSnapshot()[0]
	rec := observeAll(r, u, server.URL)
	for range 20 {
		r.readFeed(t.Context(), u, "friends", nil, 1)
	}
	stats := rec.Total(0)
	if stats.Ops != 20 || stats.Failures != 20 {
		t.Fatalf("stats=%+v, want 20 failures of 20 operations", stats)
	}
	if ops, failures := routeFailures(rec, "GET /api/feeds/friends"); ops != 20 || failures != 20 {
		t.Fatalf("route ops=%d failures=%d", ops, failures)
	}
	if rec.Violation(metrics.DefaultSLO, 1) == "" {
		t.Fatal("malformed responses did not violate the SLO")
	}
	if s := r.books.Load().stats(u.Region, 1); s.ops != 20 || s.failures != 20 {
		t.Fatalf("growth ops=%d failures=%d", s.ops, s.failures)
	}
	if got := r.board.ErrorCount(); got != 20 {
		t.Fatalf("errors=%d, want 20", got)
	}
}

// TestDeletedSessionCheckCountsOnce は、退会したユーザーのセッションの確認が 401 以外で
// 終わったとき、その 1 操作を失敗として 1 回だけ数えることを確かめる。
func TestDeletedSessionCheckCountsOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		failures int64
	}{
		{"401", http.StatusUnauthorized, `{"error":"unauthorized"}`, 0},
		{"200", http.StatusOK, `{"id":"x"}`, 1},
		{"malformed 200", http.StatusOK, `{"id":`, 1},
		{"500", http.StatusInternalServerError, `{"error":"internal"}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/me/account-deletion" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			u := r.usersSnapshot()[0]
			rec := observeAll(r, u, server.URL)
			r.deleteAccount(t.Context(), u)
			if stats := rec.Total(0); stats.Ops != 2 || stats.Failures != tc.failures {
				t.Fatalf("stats=%+v, want %d failures", stats, tc.failures)
			}
			if s := r.books.Load().stats(u.Region, 1); s.failures != tc.failures {
				t.Fatalf("growth failures=%d, want %d", s.failures, tc.failures)
			}
		})
	}
}
