package scenario

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// TestRejectedVisibilityChange は、開いている投稿期間の投稿の公開範囲の変更を 403 で拒否されたら
// 検証の失敗として数え、次の投稿期間が開いたあとの 403 と BAN は数えないことを確かめる（#122）。
func TestRejectedVisibilityChange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		code     string
		nextDay  bool
		failures int
		banned   bool
	}{
		{name: "open", code: "forbidden", failures: 1},
		{name: "next-window", code: "forbidden", nextDay: true},
		{name: "banned", code: "ip_banned", banned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
			r.setDuration(1000 * time.Second)
			r.books.Store(newBook(r.tt, time.Now()))
			r.deleteRate, r.changeRate = 0, 1
			rec := metrics.New(time.Now(), time.Hour)
			r.recorder.Store(rec)
			window, err := r.openDay(t.Context(), geo.Asia, 1)
			if err != nil {
				t.Fatal(err)
			}
			u := r.users[geo.Asia][0]
			post, visibility, ok := r.post(t.Context(), u, window)
			if !ok {
				t.Fatalf("post failed: %v", r.board.Errors())
			}
			if tc.nextDay {
				if _, err := r.openDay(t.Context(), geo.Asia, 2); err != nil {
					t.Fatal(err)
				}
			}
			serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
				if req.Method != http.MethodPatch || !strings.HasPrefix(req.URL.Path, "/api/posts/") {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"` + tc.code + `"}`))
				return true
			})
			before := r.board.ErrorCount()
			r.afterPost(t.Context(), u, post, visibility)
			failures := rec.Total(0).Failures
			if got := int(r.board.ErrorCount() - before); got != tc.failures || int(failures) != tc.failures || u.banned.Load() != tc.banned {
				t.Fatalf("errors=%d failures=%d banned=%v, want %d/%v (%v)", got, failures, u.banned.Load(), tc.failures, tc.banned, r.board.Errors())
			}
		})
	}
}
