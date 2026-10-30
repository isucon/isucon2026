package scenario

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// 負荷ユーザーの準備で認定公開アカウントのフォローに 1 件でも失敗すれば、準備の失敗として
// 理由を返す（準備が欠けたまま負荷走行に進まない）。
func TestPrepareFailsWhenFollowFails(t *testing.T) {
	for _, failing := range []int32{0, 1, 1 << 20} {
		r, app := newTestRunner(t, 2, metrics.DefaultSLO)
		for _, region := range geo.Regions {
			for _, du := range r.initial.CohortUsers(region) {
				app.addUser(du.ID, du.Username, du.SecretKey, du.Region)
			}
		}
		var follows atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/api/follows/") && follows.Add(1) <= failing {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			app.ServeHTTP(w, req)
		}))
		t.Cleanup(server.Close)
		r.api = client.NewClient(client.Config{Base: server.URL, Retry: r.cfg.Retry})
		reason := r.prepareUsers(t.Context())
		if (reason != "") != (failing > 0) || failing > 0 && !strings.Contains(reason, "フォローできません") {
			t.Fatalf("failing=%d reason=%q", failing, reason)
		}
	}
}

// TestPrepareFollowRetriedOrBanned は、準備中のフォローが 503 のあとの送り直しで 409 になった
// （前の試行でフォローできた）場合と、BAN された場合は、準備の失敗にしないことを確かめる。
func TestPrepareFollowRetriedOrBanned(t *testing.T) {
	for _, mode := range []string{"applied-then-503", "banned"} {
		t.Run(mode, func(t *testing.T) {
			r, app := newTestRunner(t, 2, metrics.DefaultSLO)
			for _, region := range geo.Regions {
				for _, du := range r.initial.CohortUsers(region) {
					app.addUser(du.ID, du.Username, du.SecretKey, du.Region)
				}
			}
			var follows atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/api/follows/") && follows.Add(1) == 1 {
					if mode == "banned" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"error":"ip_banned"}`))
						return
					}
					app.ServeHTTP(httptest.NewRecorder(), req)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				app.ServeHTTP(w, req)
			}))
			t.Cleanup(server.Close)
			r.api = client.NewClient(client.Config{Base: server.URL, Retry: r.cfg.Retry})
			if reason := r.prepareUsers(t.Context()); reason != "" {
				t.Fatalf("reason=%q", reason)
			}
			if follows.Load() < 2 {
				t.Fatalf("follows=%d", follows.Load())
			}
		})
	}
}
