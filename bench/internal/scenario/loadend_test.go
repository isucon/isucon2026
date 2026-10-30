package scenario

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// TestHungSubscriptions は、負荷ユーザーの誰の通知の購読にも応答がなければ負荷走行を始めずに
// fail とし（#249）、一部のユーザーだけなら待ちの上限のあとで負荷走行を始めることを確かめる。
func TestHungSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		hang func(country string) bool
		want string
	}{
		{"everyone", func(string) bool { return true }, "通知の購読"},
		{"europe only", func(country string) bool { return geo.RegionOf(country) == geo.Europe }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
			serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
				if req.URL.Path != "/api/notifications" || !tc.hang(req.Header.Get("X-Country-Code")) {
					return false
				}
				// 応答のヘッダーを返さずに待たせる。
				<-req.Context().Done()
				return true
			})
			reason := r.load(t.Context())
			if tc.want == "" && reason != "" || !strings.Contains(reason, tc.want) {
				t.Fatalf("reason=%q, want %q", reason, tc.want)
			}
			if tc.want == "" && len(r.openedCounts()) == 0 {
				t.Fatal("the load did not run")
			}
		})
	}
}

// TestAdminLogoutAfterLoad は、負荷走行の時間が過ぎたあとも管理者としてログアウトし、全体の
// 期限（CLI の -timeout や SIGTERM）が来たら応答を待たずに打ち切ることを確かめる（#145）。
func TestAdminLogoutAfterLoad(t *testing.T) {
	t.Run("after the load", func(t *testing.T) {
		r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		var logouts atomic.Int32
		serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
			if req.URL.Path == "/api/admin/auth/logout" {
				logouts.Add(1)
			}
			return false
		})
		r.load(t.Context())
		if logouts.Load() != 1 || r.board.ErrorCount() != 0 {
			t.Fatalf("logouts=%d errors=%v", logouts.Load(), r.board.Errors())
		}
	})
	for _, tc := range []struct {
		name     string
		deadline time.Duration
		logout   bool
	}{
		// 負荷走行の時間（1 秒）のあと、ログアウトの応答を待つ間に全体の期限が来る。
		{"deadline during logout", 1300 * time.Millisecond, true},
		// 負荷走行の途中で全体の期限が来る。ログアウトは送らなくてよい。
		{"deadline during load", 200 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
			r.cfg.Growth.FastInvites, r.cfg.Growth.QuickInvites = 0, 0
			var logouts atomic.Int32
			serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
				if req.URL.Path != "/api/admin/auth/logout" {
					return false
				}
				// 応答しない。
				_, _ = io.Copy(io.Discard, req.Body)
				logouts.Add(1)
				<-req.Context().Done()
				return true
			})
			ctx, cancel := context.WithTimeout(t.Context(), tc.deadline)
			defer cancel()
			start := time.Now()
			r.load(ctx)
			if took := time.Since(start); took > tc.deadline+500*time.Millisecond {
				t.Fatalf("load took %v after the overall deadline of %v", took, tc.deadline)
			}
			if tc.logout && logouts.Load() != 1 {
				t.Fatalf("logouts=%d", logouts.Load())
			}
		})
	}
}
