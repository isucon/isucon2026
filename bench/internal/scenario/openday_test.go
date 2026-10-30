package scenario

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// TestNewcomersFollowTodaysCertified は、1 日が始まるときに前日の人気のユーザーを認定してから
// 新しいユーザーを迎え、認定が遅くても新しいユーザーがその人をフォローできることを確かめる（#243）。
func TestNewcomersFollowTodaysCertified(t *testing.T) {
	r, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	r.setDuration(6 * time.Second)
	r.cfg.Growth.FriendRatio = 0 // 新しいユーザーはみな認定公開アカウントをフォローする
	var wg sync.WaitGroup
	r.wg = &wg
	b := newBook(r.tt, time.Now())
	r.books.Store(b)
	// 認定公開アカウントをフォローした人（あとでフォローを解除する人もいるので要求で数える）。
	var mu sync.Mutex
	followed := map[string]map[string]bool{}
	serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
		switch {
		case strings.HasSuffix(req.URL.Path, "/certification"):
			time.Sleep(100 * time.Millisecond)
		case strings.HasPrefix(req.URL.Path, "/api/follows/") && req.Method != http.MethodDelete:
			mu.Lock()
			followee := strings.TrimPrefix(req.URL.Path, "/api/follows/")
			if followed[followee] == nil {
				followed[followee] = map[string]bool{}
			}
			followed[followee][req.Header.Get("Authorization")] = true
			mu.Unlock()
		}
		return false
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer func() {
		cancel()
		wg.Wait()
	}()
	w1, err := r.openDay(ctx, geo.Asia, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 前日の投稿期間に、ほかの 2 人から RealMoji を受けたことにする。
	star := r.users[geo.Asia][0]
	r.model.Created(r.model.BeginCreatePost(star.ID), model.Post{ID: "p-star", AuthorID: star.ID, WindowID: w1, PostedAt: time.Now()}, "public")
	for i, reactor := range r.users[geo.Asia][1:3] {
		r.model.ReplacedRealMoji(r.model.BeginReplaceRealMoji("p-star", reactor.ID), client.RealMoji{ID: "rm-star-" + itoa(i)})
	}
	time.Sleep(10 * time.Millisecond)
	// 前日に 2 人がすぐ通過したことにして、新しいユーザーが来るようにする。
	b.pass(geo.Asia, 1, "passer-1", 0)
	b.pass(geo.Asia, 1, "passer-2", 0)
	if _, err := r.openDay(ctx, geo.Asia, 2); err != nil {
		t.Fatal(err)
	}
	newcomers := b.stats(geo.Asia, 2).newcomers
	if newcomers == 0 || !star.certified.Load() {
		t.Fatalf("newcomers=%d certified=%v", newcomers, star.certified.Load())
	}
	followers := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(followed[star.ID])
	}
	for deadline := time.Now().Add(2 * time.Second); followers() < newcomers && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if got := followers(); got != newcomers {
		t.Fatalf("newcomers following the account certified today = %d, want %d", got, newcomers)
	}
}
