package scenario

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// notificationServer は、購読のたびに handle の返す通知を 1 件送って閉じるサーバーにつなぐ。
// 返り値は購読の回数。
func notificationServer(t *testing.T, u *user, handle func(n int64) string) *atomic.Int64 {
	t.Helper()
	var subscriptions atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if payload := handle(subscriptions.Add(1)); payload != "" {
			fmt.Fprintf(w, "data: %s\n\n", payload)
		}
	}))
	t.Cleanup(server.Close)
	u.API = client.New(server.URL).WithCountry(u.Country()).WithClientIP(u.API.ClientIP())
	return &subscriptions
}

// TestNotificationRequeueStopsOnCancel は、投稿を待つ間に届いた次の通知を戻すのと、listen が
// さらに新しい通知を入れるのが重なっても、走行が終われば投稿を待つ処理が止まることを確かめる（#128）。
func TestNotificationRequeueStopsOnCancel(t *testing.T) {
	r, _, window := newWindowRunner(t)
	r.peekRate = 0
	u := r.users[geo.Asia][0]
	u.persona.attendance = 1
	notificationServer(t, u, func(n int64) string {
		return fmt.Sprintf(`{"id":"n%d","postingWindowId":"next-%d","region":"asia"}`, n, n)
	})
	for trial := range 30 {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		notifications := make(chan notice, 1)
		listener, consumer := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(listener)
			r.listen(ctx, u, notifications, nil)
		}()
		go func() {
			defer close(consumer)
			for ctx.Err() == nil {
				r.enterWindow(ctx, u, window, nil, time.Now(), notifications)
			}
		}()
		<-ctx.Done()
		cancel()
		for name, done := range map[string]chan struct{}{"listen": listener, "enterWindow": consumer} {
			select {
			case <-done:
			case <-time.After(time.Second):
				// 止まった送信をほどいてから失敗にする。
				go func() {
					for {
						select {
						case <-notifications:
						case <-listener:
							return
						}
					}
				}()
				t.Fatalf("trial %d: %s did not stop after the run ended", trial, name)
			}
		}
	}
}

// TestNotificationReconnectBackoff は、新しい通知を受けずにすぐ切れる購読（空の応答、重複した
// 通知、不正な通知）を 1 秒に 1 回までにし（#177）、新しい通知のあとはすぐに購読し直すことを
// 確かめる。
func TestNotificationReconnectBackoff(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		min, max int64
	}{
		{"empty", 1, 2},
		{"duplicate", 1, 2},
		{"invalid", 1, 2},
		{"fresh", 5, 1 << 30},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			r, _, window := newWindowRunner(t)
			u := r.users[geo.Asia][0]
			subscriptions := notificationServer(t, u, func(n int64) string {
				switch tc.mode {
				case "duplicate":
					return fmt.Sprintf(`{"id":"n1","postingWindowId":%q,"region":"asia"}`, window)
				case "invalid":
					return fmt.Sprintf(`{"id":"n1","postingWindowId":%q,"region":"europe"}`, window)
				case "fresh":
					return fmt.Sprintf(`{"id":"n%d","postingWindowId":"next-%d","region":"asia"}`, n, n)
				}
				return ""
			})
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			r.listen(ctx, u, make(chan notice, 1), nil)
			if got := subscriptions.Load(); got < tc.min || got > tc.max {
				t.Fatalf("subscriptions in 300ms = %d, want %d..%d", got, tc.min, tc.max)
			}
		})
	}
}

// TestNotificationPointsOnlyForOpenedWindows は、通知の点を、bench が開いたことを確かめた投稿期間の
// 通知にだけ与え（#255）、開く要求への応答より先に届いた通知にも、記録されてから与えることを確かめる。
func TestNotificationPointsOnlyForOpenedWindows(t *testing.T) {
	t.Run("never opened", func(t *testing.T) {
		r, _, window := newWindowRunner(t)
		u := r.users[geo.Asia][0]
		notificationServer(t, u, func(n int64) string {
			switch n {
			case 1:
				return `{"id":"n1","postingWindowId":"never-opened","region":"asia"}`
			case 2:
				return fmt.Sprintf(`{"id":"n2","postingWindowId":%q,"region":"asia"}`, window)
			}
			return ""
		})
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		r.listen(ctx, u, make(chan notice, 1), nil)
		if got := r.board.Count(score.TagNotification); got != 1 {
			t.Fatalf("notification points = %d, want 1", got)
		}
	})
	t.Run("before the open response", func(t *testing.T) {
		r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
		r.setDuration(1000 * time.Second)
		r.books.Store(newBook(r.tt, time.Now()))
		app.mu.Lock()
		app.openDelay = 300 * time.Millisecond
		app.mu.Unlock()
		u := r.users[geo.Asia][0]
		notifications := make(chan notice, 1)
		subscribed := make(chan struct{}, 1)
		stop := r.startListening(t.Context(), u, notifications, subscribed)
		defer stop()
		<-subscribed
		opened := make(chan string, 1)
		go func() {
			id, _ := r.openDay(t.Context(), geo.Asia, 1)
			opened <- id
		}()
		n := <-notifications
		points := r.board.Count(score.TagNotification)
		id := <-opened
		for deadline := time.Now().Add(time.Second); r.board.Count(score.TagNotification) == 0 && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		if n.PostingWindowID != id || points != 0 || r.board.Count(score.TagNotification) != 1 {
			t.Fatalf("notified %s (opened %s), points before the response=%d, after=%d", n.PostingWindowID, id, points, r.board.Count(score.TagNotification))
		}
	})
}
