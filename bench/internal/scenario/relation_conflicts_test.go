package scenario

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// conflictOn は method で path の接頭辞に来た要求に、関係を変えずに status を返すハンドラー。
func conflictOn(method, prefix string, status int) func(w http.ResponseWriter, req *http.Request) bool {
	return func(w http.ResponseWriter, req *http.Request) bool {
		if req.Method != method || !strings.HasPrefix(req.URL.Path, prefix) {
			return false
		}
		w.WriteHeader(status)
		fmt.Fprint(w, `{"error":"relationship_conflict"}`)
		return true
	}
}

// certifiedPair は asia の 2 人目を認定公開アカウントにし、1 人目と組にして返す。
func certifiedPair(r *Runner, app *fakeApp) (*user, *user) {
	u, influencer := r.users["asia"][0], r.users["asia"][1]
	app.mu.Lock()
	app.users[influencer.ID].certified = true
	app.mu.Unlock()
	r.certify(influencer)
	return u, influencer
}

// モデルで確実にフォローしていない認定公開アカウントへのフォローが 409 なら誤りとし、解除の
// 候補にも入れない。もうフォローしている相手と、503 で送り直したあとの 409 は正しい。
func TestFollowConflict(t *testing.T) {
	t.Run("not-following", func(t *testing.T) {
		r, app := newTestRunner(t, 2, metrics.DefaultSLO)
		rec := startRecording(r)
		u, influencer := certifiedPair(r, app)
		routeThrough(t, r, u, app, conflictOn(http.MethodPut, "/api/follows/", http.StatusConflict))
		r.followCertified(t.Context(), u, 1)
		if failures := rec.Total(0).Failures; failures != 1 || u.follows[influencer.ID] {
			t.Fatalf("failures=%d follows=%v errors=%v", failures, u.follows, r.board.Errors())
		}
	})
	t.Run("already-following", func(t *testing.T) {
		r, app := newTestRunner(t, 2, metrics.DefaultSLO)
		rec := startRecording(r)
		u, influencer := certifiedPair(r, app)
		// 初期データのフォローのように、u.follows にはないがフォローしている。
		app.mu.Lock()
		app.follows[[2]string{u.ID, influencer.ID}] = true
		app.mu.Unlock()
		r.model.BeginFollow(u.ID, influencer.ID, true).Applied()
		time.Sleep(10 * time.Millisecond)
		r.followCertified(t.Context(), u, 1)
		if failures := rec.Total(0).Failures; failures != 0 || !u.follows[influencer.ID] || r.board.ErrorCount() != 0 {
			t.Fatalf("failures=%d follows=%v errors=%v", failures, u.follows, r.board.Errors())
		}
	})
	t.Run("retried-after-503", func(t *testing.T) {
		r, app := newTestRunner(t, 2, metrics.DefaultSLO)
		startRecording(r)
		u, influencer := certifiedPair(r, app)
		var attempts atomic.Int32
		routeThrough(t, r, u, app, func(w http.ResponseWriter, req *http.Request) bool {
			if req.Method != http.MethodPut || !strings.HasPrefix(req.URL.Path, "/api/follows/") || attempts.Add(1) > 1 {
				return false
			}
			// フォローしたうえで 503 を返す。送り直すと 409 になる。
			app.ServeHTTP(httptest.NewRecorder(), req)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		})
		r.followCertified(t.Context(), u, 1)
		if attempts.Load() != 2 || r.board.ErrorCount() != 0 || !u.follows[influencer.ID] {
			t.Fatalf("attempts=%d follows=%v errors=%v", attempts.Load(), u.follows, r.board.Errors())
		}
		if got := r.model.Following(u.ID, influencer.ID, r.model.Now()); got != model.Unknown {
			t.Fatalf("follow after a retried conflict is %v, want unknown", got)
		}
	})
}

// 確実にフォローしている相手の解除、確実に友達の相手の解除、確実にブロックしている相手の
// 解除が拒否されたら誤りとする。
func TestUndoConflictWhileRelated(t *testing.T) {
	t.Run("unfollow", func(t *testing.T) {
		r, app := newTestRunner(t, 2, metrics.DefaultSLO)
		u, influencer := certifiedPair(r, app)
		r.followCertified(t.Context(), u, 1)
		time.Sleep(10 * time.Millisecond)
		rec := startRecording(r)
		routeThrough(t, r, u, app, conflictOn(http.MethodDelete, "/api/follows/", http.StatusConflict))
		// toggleFollow は 3 回に 1 回解除する。解除を送るまで繰り返す。
		for i := 0; i < 100 && rec.Total(0).Ops == 0; i++ {
			r.toggleFollow(t.Context(), u)
		}
		if failures := rec.Total(0).Failures; failures != 1 || !u.follows[influencer.ID] {
			t.Fatalf("failures=%d follows=%v errors=%v", failures, u.follows, r.board.Errors())
		}
	})
	t.Run("unfriend", func(t *testing.T) {
		// asia の 1 人目の友達は 2 人目と 3 人目（unfriend は友達が 2 人以上いるときだけ解除する）。
		r, app := newTestRunner(t, 4, metrics.DefaultSLO)
		u := r.users["asia"][1]
		time.Sleep(10 * time.Millisecond)
		rec := startRecording(r)
		routeThrough(t, r, u, app, conflictOn(http.MethodDelete, "/api/friendships/", http.StatusConflict))
		r.unfriend(t.Context(), u)
		if stats := rec.Total(0); stats.Ops != 1 || stats.Failures != 1 {
			t.Fatalf("stats=%+v errors=%v", stats, r.board.Errors())
		}
	})
	for _, status := range []int{http.StatusForbidden, http.StatusConflict} {
		t.Run(fmt.Sprintf("unblock-%d", status), func(t *testing.T) {
			// asia の 1 人目は 3 人目（友達でない）をブロックする。
			r, app := newTestRunner(t, 3, metrics.DefaultSLO)
			u := r.users["asia"][0]
			r.toggleBlock(t.Context(), u)
			if len(u.blocking) != 1 {
				t.Fatalf("blocking=%v errors=%v", u.blocking, r.board.Errors())
			}
			time.Sleep(10 * time.Millisecond)
			rec := startRecording(r)
			routeThrough(t, r, u, app, conflictOn(http.MethodDelete, "/api/blocks/", status))
			r.toggleBlock(t.Context(), u)
			if stats := rec.Total(0); stats.Ops != 1 || stats.Failures != 1 || len(u.blocking) != 1 {
				t.Fatalf("stats=%+v blocking=%v errors=%v", stats, u.blocking, r.board.Errors())
			}
		})
	}
}

// 解除の結果がわからなかったブロックをもう一度解除して 403 なら、正しい拒否とする。
func TestUnblockAfterUnknownUnblock(t *testing.T) {
	r, app := newTestRunner(t, 3, metrics.DefaultSLO)
	u := r.users["asia"][0]
	r.toggleBlock(t.Context(), u)
	app.mu.Lock()
	clear(app.blocks)
	app.mu.Unlock()
	r.model.BeginUnblock(u.ID, u.blocking[0]).Unknown()
	rec := startRecording(r)
	routeThrough(t, r, u, app, func(http.ResponseWriter, *http.Request) bool { return false })
	r.toggleBlock(t.Context(), u)
	if stats := rec.Total(0); stats.Ops != 1 || stats.Failures != 0 || len(u.blocking) != 0 || r.board.ErrorCount() != 0 {
		t.Fatalf("stats=%+v blocking=%v errors=%v", stats, u.blocking, r.board.Errors())
	}
}

// ブロックの解除への BAN（403 ip_banned）は解除の拒否ではない。失敗に数えず、ユーザーを止める。
func TestUnblockBanned(t *testing.T) {
	r, app := newTestRunner(t, 3, metrics.DefaultSLO)
	u := r.users["asia"][0]
	r.toggleBlock(t.Context(), u)
	time.Sleep(10 * time.Millisecond)
	rec := startRecording(r)
	routeThrough(t, r, u, app, func(w http.ResponseWriter, req *http.Request) bool {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"ip_banned"}`)
		return true
	})
	r.toggleBlock(t.Context(), u)
	if stats := rec.Total(0); stats.Failures != 0 || r.board.ErrorCount() != 0 || !u.banned.Load() {
		t.Fatalf("stats=%+v banned=%v errors=%v", stats, u.banned.Load(), r.board.Errors())
	}
}
