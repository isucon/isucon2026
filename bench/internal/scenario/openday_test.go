package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// TestOpenWindowFailures は、投稿期間を開く要求が拒否されたら（BAN を除く）負荷走行を打ち切り、
// 結果のわからない失敗が続いて 1 つも開けなかった走行も通さないことを確かめる（#120）。一部の
// リージョンが BAN で開けないだけなら、ほかのリージョンで続ける。
func TestOpenWindowFailures(t *testing.T) {
	all := []string{geo.Asia, geo.Europe, geo.America}
	for _, tc := range []struct {
		name    string
		status  int
		code    string
		regions []string
		want    string
	}{
		{"all rejected", http.StatusForbidden, "forbidden", all, "拒否されました"},
		{"one region rejected", http.StatusBadRequest, "invalid_posting_window", []string{geo.Europe}, "拒否されました"},
		{"all unknown", http.StatusInternalServerError, "internal", all, "1 つも開けません"},
		{"one region banned", http.StatusForbidden, "ip_banned", []string{geo.Europe}, ""},
		{"all banned", http.StatusForbidden, "ip_banned", all, "1 つも開けません"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
			serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
				if req.URL.Path != "/api/admin/posting-window" {
					return false
				}
				data, _ := io.ReadAll(req.Body)
				req.Body = io.NopCloser(bytes.NewReader(data))
				var input map[string]string
				_ = json.Unmarshal(data, &input)
				if !slices.Contains(tc.regions, input["region"]) {
					return false
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"` + tc.code + `"}`))
				return true
			})
			reason := r.load(t.Context())
			if tc.want == "" && reason != "" || !strings.Contains(reason, tc.want) {
				t.Fatalf("reason=%q, want %q (opened=%v errors=%v)", reason, tc.want, r.openedCounts(), r.board.Errors())
			}
		})
	}
}

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
