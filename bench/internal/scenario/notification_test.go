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
