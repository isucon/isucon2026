package scenario

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// searchPagesFrom は検索の要求に、cursor（beforeUsername）ごとに決めたページを返すハンドラー。
func searchPagesFrom(pages map[string]client.UserPage) func(w http.ResponseWriter, req *http.Request) bool {
	return func(w http.ResponseWriter, req *http.Request) bool {
		if req.URL.Path != "/api/users" {
			return false
		}
		page := pages[req.URL.Query().Get("beforeUsername")]
		if page.Items == nil {
			page.Items = []client.UserSummary{}
		}
		_ = json.NewEncoder(w).Encode(page)
		return true
	}
}

// 検索で探した相手が見つからず続きがあれば、続きのページを読んで確かめる。続きがあるのに空の
// ページや、最後まで読んでも見えるはずの相手がいない結果には加点しない。
func TestSearchReadsFollowingPages(t *testing.T) {
	// first は検索語を含む別の人、found は探している人。
	first := client.UserSummary{ID: "u-asia1", Username: "asia2x"}
	found := client.UserSummary{ID: "u-asia2", Username: "asia2"}
	for _, tc := range []struct {
		name     string
		pages    map[string]client.UserPage
		points   int64
		failures int64
		requests int64
	}{
		{"second-page", map[string]client.UserPage{"": {Items: []client.UserSummary{first}, HasMore: true}, first.Username: {Items: []client.UserSummary{found}}}, 1, 0, 2},
		{"empty-has-more", map[string]client.UserPage{"": {HasMore: true}}, 0, 1, 1},
		{"missing", map[string]client.UserPage{"": {Items: []client.UserSummary{first}, HasMore: true}}, 0, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, app := newTestRunner(t, 3, metrics.DefaultSLO)
			u, target := r.users["asia"][0], r.users["asia"][2]
			if target.ID != found.ID || target.Username != found.Username {
				t.Fatalf("target=%s %s", target.ID, target.Username)
			}
			time.Sleep(10 * time.Millisecond)
			rec := startRecording(r)
			routeThrough(t, r, u, app, searchPagesFrom(tc.pages))
			before := r.board.Count(score.TagSearch)
			r.searchUser(t.Context(), u, target)
			points := r.board.Count(score.TagSearch) - before
			stats := rec.Total(0)
			if points != tc.points || stats.Failures != tc.failures || stats.Ops != tc.requests {
				t.Fatalf("points=%d stats=%+v errors=%v", points, stats, r.board.Errors())
			}
		})
	}
}

// 検索結果に、確実にブロックの関係にある人がいれば加点せず失敗に数える。
func TestSearchExcludesBlockedUsers(t *testing.T) {
	for _, leak := range []bool{false, true} {
		r, app := newTestRunner(t, 2, metrics.DefaultSLO)
		a, b := r.users["asia"][0], r.users["asia"][1]
		w := r.model.BeginBlock(a.ID, b.ID)
		if err := a.API.Block(t.Context(), a.Token, b.ID); err != nil {
			t.Fatal(err)
		}
		w.Applied()
		time.Sleep(10 * time.Millisecond)
		app.mu.Lock()
		app.ignoreBlocks = leak
		app.mu.Unlock()
		rec := startRecording(r)
		routeThrough(t, r, a, app, func(http.ResponseWriter, *http.Request) bool { return false })
		r.searchUser(t.Context(), a, b)
		points, failures := r.board.Count(score.TagSearch), rec.Total(0).Failures
		if leak && (points != 0 || failures != 1) || !leak && (points != 1 || failures != 0) {
			t.Fatalf("leak=%v points=%d failures=%d errors=%v", leak, points, failures, r.board.Errors())
		}
	}
}
