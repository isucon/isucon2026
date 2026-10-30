package scenario

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// serveThrough は偽のアプリの前に handler を置いたサーバーへ、管理者と負荷ユーザーをつなぎ直す。
// handler が false を返した要求は偽のアプリがそのまま処理する。
func serveThrough(t *testing.T, r *Runner, app *fakeApp, handler func(w http.ResponseWriter, req *http.Request) bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !handler(w, req) {
			app.ServeHTTP(w, req)
		}
	}))
	t.Cleanup(server.Close)
	r.api = client.NewClient(client.Config{Base: server.URL, Retry: r.cfg.Retry})
	r.admin = r.api.WithCountry("JP")
	for _, u := range r.usersSnapshot() {
		u.API = r.api.WithCountry(u.Country()).WithClientIP(u.API.ClientIP())
	}
}

// appliedThen503 は、n 回目の要求だけを偽のアプリに処理させたうえで、応答を 503 に差し替える。
// クライアントは 503 を送り直すので、反映された書き込みの応答が届かず、送り直しの応答だけが返る。
func appliedThen503(app *fakeApp, method, path string, n int32) func(w http.ResponseWriter, req *http.Request) bool {
	var count atomic.Int32
	return func(w http.ResponseWriter, req *http.Request) bool {
		if req.Method != method || req.URL.Path != path || count.Add(1) != n {
			return false
		}
		app.ServeHTTP(httptest.NewRecorder(), req)
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
}

func TestIsRejectionExcludesRetried4xx(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&client.APIError{Status: http.StatusConflict, Attempts: 1}, true},
		{&client.APIError{Status: http.StatusNotFound}, true},
		{&client.APIError{Status: http.StatusConflict, Attempts: 2}, false},
		{&client.APIError{Status: http.StatusServiceUnavailable, Attempts: 5}, false},
		{&client.APIError{Status: http.StatusInternalServerError, Attempts: 1}, false},
	} {
		if got := isRejection(tc.err); got != tc.want {
			t.Errorf("isRejection(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// TestSettleRetried4xxIsUnknown は、503 を受けて送り直した要求の 4xx を、反映されていない
// 拒否ではなく結果不明として記録することを確かめる。
func TestSettleRetried4xxIsUnknown(t *testing.T) {
	r, _ := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	a, b, c := r.users[geo.Asia][0], r.users[geo.Asia][2], r.users[geo.Europe][0]
	settle(r.model.BeginFriendship(a.ID, b.ID, true), &client.APIError{Status: http.StatusConflict, Attempts: 2})
	settle(r.model.BeginFriendship(a.ID, c.ID, true), &client.APIError{Status: http.StatusConflict, Attempts: 1})
	now := r.model.Now()
	if got := r.model.Friend(a.ID, b.ID, now); got != model.Unknown {
		t.Errorf("friendship after a retried 409 = %v, want unknown", got)
	}
	if got := r.model.Friend(a.ID, c.ID, now); got != model.False {
		t.Errorf("friendship after a single 409 = %v, want false", got)
	}
}

// TestMoveAppliedBefore503 は、所属リージョンの変更が反映されて 503 になり、送り直しが 409 に
// なっても、変わっていないと決めつけずに確かめ直し、アプリと同じリージョンから使い続けることを
// 確かめる（#288）。
func TestMoveAppliedBefore503(t *testing.T) {
	r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	u := r.users[geo.Asia][0]
	app.mu.Lock()
	app.moveReplies = []int{http.StatusServiceUnavailable, http.StatusConflict}
	app.mu.Unlock()
	notifications := make(chan notice, 1)
	stop := r.startListening(t.Context(), u, notifications, nil)
	stop = r.move(t.Context(), u, stop, notifications)
	stop()
	app.mu.Lock()
	appRegion := app.users[u.ID].region
	app.mu.Unlock()
	if u.Region == geo.Asia || u.Region != appRegion || u.stranded.Load() || u.stopped.Load() {
		t.Fatalf("bench region=%s app region=%s stranded=%v stopped=%v errors=%v", u.Region, appRegion, u.stranded.Load(), u.stopped.Load(), r.board.Errors())
	}
}

// TestPostAppliedBefore503 は、投稿が保存されて 503 になり、送り直しが 409 になっても、投稿を
// 拒否されたものとせず（エラーにせず）、走行後の整合性チェックでその投稿を許すことを確かめる（#289）。
func TestPostAppliedBefore503(t *testing.T) {
	r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	r.setDuration(1000 * time.Second)
	r.books.Store(newBook(r.tt, time.Now()))
	window, err := r.openDay(t.Context(), geo.Asia, 1)
	if err != nil {
		t.Fatal(err)
	}
	serveThrough(t, r, app, appliedThen503(app, http.MethodPost, "/api/posts", 1))
	if _, _, ok := r.post(t.Context(), r.users[geo.Asia][0], window); ok {
		t.Fatal("a post whose creation was answered with 409 was accepted")
	}
	if app.postCount() != 1 || r.board.ErrorCount() != 0 {
		t.Fatalf("posts=%d errors=%v", app.postCount(), r.board.Errors())
	}
	if reason := r.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
}

// TestOpenWindowAppliedBefore503 は、投稿期間が開いて 503 になり、送り直しが 409 になっても、
// 開いたかどうかを今の投稿期間で確かめてその日を続けることを確かめる（#298）。
func TestOpenWindowAppliedBefore503(t *testing.T) {
	r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	r.setDuration(18 * time.Second)
	r.books.Store(newBook(r.tt, time.Now()))
	var opens atomic.Int32
	serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
		if req.URL.Path != "/api/admin/posting-window" {
			return false
		}
		if opens.Add(1) == 1 {
			app.ServeHTTP(httptest.NewRecorder(), req)
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"posting_window_conflict"}`))
		}
		return true
	})
	id, err := r.openDay(t.Context(), geo.Asia, 1)
	if err != nil || id == "" || !r.model.HasWindow(id) || opens.Load() != 2 {
		t.Fatalf("id=%q err=%v opens=%d errors=%v", id, err, opens.Load(), r.board.Errors())
	}
	app.mu.Lock()
	opened := app.windows[geo.Asia].id
	app.mu.Unlock()
	if id != opened || r.board.ErrorCount() != 0 {
		t.Fatalf("id=%q app=%q errors=%v", id, opened, r.board.Errors())
	}
}

// TestWindowRecoverySkipsBannedUser は、投稿期間を開いた結果がわからず今の投稿期間を読んで
// 確かめるとき、読んだユーザーが BAN されていれば止めて悪評に記録し、次のユーザーで読み直す
// ことを確かめる（#197）。セッションが切れていたときも次のユーザーで読む。
func TestWindowRecoverySkipsBannedUser(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		r, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
		r.setDuration(4 * time.Second)
		victim := r.users[geo.Asia][0]
		var victimReads atomic.Int32
		serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
			switch {
			case req.URL.Path == "/api/admin/posting-window":
				// 開いたうえで応答を失う。
				app.ServeHTTP(httptest.NewRecorder(), req)
				w.WriteHeader(http.StatusInternalServerError)
				return true
			case req.URL.Path == "/api/posting-window/current" && req.Header.Get("X-Client-IP") == victim.API.ClientIP():
				victimReads.Add(1)
				w.WriteHeader(status)
				code := map[int]string{http.StatusUnauthorized: "invalid_session", http.StatusForbidden: "ip_banned"}[status]
				_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
				return true
			}
			return false
		})
		r.books.Store(newBook(r.tt, time.Now()))
		start := time.Now()
		id, err := r.openDay(t.Context(), geo.Asia, 1)
		if err != nil || id == "" || victimReads.Load() != 1 || time.Since(start) > 300*time.Millisecond {
			t.Fatalf("status %d: id=%q err=%v victim reads=%d took=%v errors=%v", status, id, err, victimReads.Load(), time.Since(start), r.board.Errors())
		}
		if banned := status == http.StatusForbidden; victim.stopped.Load() != banned || (r.reputations() == 1) != banned {
			t.Fatalf("status %d: stopped=%v reputation=%d", status, victim.stopped.Load(), r.reputations())
		}
	}
}

// TestFriendRequestAppliedBefore503 は、友達申請で友達になって 503 になり、送り直しが 409 に
// なっても、友達になっていないと決めつけず、走行後の整合性チェックで友達を許すことを確かめる。
func TestFriendRequestAppliedBefore503(t *testing.T) {
	r, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	// 隣どうしだけが友達なので、0 番目と 2 番目は友達ではない。2 番目からの申請を保留にしておくと、
	// 0 番目の申請で友達になる。
	u, target := r.users[geo.Asia][0], r.users[geo.Asia][2]
	app.mu.Lock()
	app.requests["pending-request"] = &fakeRequest{from: target.ID, to: u.ID, status: "pending", at: time.Now()}
	app.mu.Unlock()
	serveThrough(t, r, app, appliedThen503(app, http.MethodPost, "/api/friend-requests", 1))
	r.requestFriend(t.Context(), u, target, false)
	app.mu.Lock()
	friends := app.friends[key(u.ID, target.ID)]
	app.mu.Unlock()
	if !friends || r.board.ErrorCount() != 0 {
		t.Fatalf("friends=%v errors=%v", friends, r.board.Errors())
	}
	if reason := r.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
}
