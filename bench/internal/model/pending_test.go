package model

import (
	"strings"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// nextDay は asia の次の日の投稿期間 w2 を開き、a がそこに公開で投稿する（p-a2）。
func (w *world) nextDay() {
	w.c.tick()
	open := w.m.BeginOpenWindow("asia")
	w.c.tick()
	w.m.OpenedWindow(open, Window{ID: "w2", Region: "asia", LocalDate: w.window.LocalDate.AddDate(0, 0, 1)})
	w.c.tick()
	creation := w.m.BeginCreatePost("a")
	w.c.tick()
	w.m.Created(creation, Post{ID: "p-a2", AuthorID: "a", WindowID: "w2", ImageID: "img-a2", Caption: "hi", PostedAt: w.c.now()}, "public")
	w.posts["a2"] = w.m.Post("p-a2")
	w.c.tick()
}

// TestOldUnknownCreationKeepsNextGateClosed は、前日の結果のわからない投稿作成が、翌日の
// ゲート（閲覧者が今日投稿したか）を不明にしないことを確かめる。5xx を返した作成は応答までに
// 処理されているので前日の投稿にしかならないが、タイムアウトした作成や、次の日が始まるのと
// 重なった作成は、翌日の投稿になったかもしれない。
func TestOldUnknownCreationKeepsNextGateClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		settle  func(*Write)
		overlap bool
		leak    bool
	}{
		"failed on the previous day":      {(*Write).Failed, false, true},
		"timed out on the previous day":   {(*Write).Unknown, false, false},
		"failed while the next day opens": {(*Write).Failed, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			m := w.m
			// b は前日の投稿を消して撮り直したが、撮り直しの結果がわからない。
			m.BeginDeletePost("p-b").Applied()
			w.c.tick()
			retake := m.BeginCreatePost("b")
			w.c.tick()
			if !tc.overlap {
				tc.settle(retake.Write)
			}
			w.nextDay()
			if tc.overlap {
				tc.settle(retake.Write)
			}
			r := w.later()
			result := m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.FeedPost{feedItem(w.posts["a2"], *m.User("a"))}}, r, false)
			if leak := len(result.Violations) > 0; leak != tc.leak {
				t.Fatalf("leak detected=%t, want %t: %+v", leak, tc.leak, result)
			}
			if tc.leak && !strings.Contains(result.Violations[0], "見えてはいけない") {
				t.Fatalf("violations=%v", result.Violations)
			}
		})
	}
}

// TestUnknownCreationOpensSameDayGate は、結果のわからない投稿作成のあった日のゲートは
// 不明のまま（投稿が見えても見えなくてもよい）であることを確かめる。
func TestUnknownCreationOpensSameDayGate(t *testing.T) {
	for _, settle := range []func(*Write){(*Write).Failed, (*Write).Unknown} {
		w := newWorld(t)
		m := w.m
		m.BeginDeletePost("p-b").Applied()
		w.c.tick()
		retake := m.BeginCreatePost("b")
		w.c.tick()
		settle(retake.Write)
		r := w.later()
		page := client.FeedPage{Items: []client.FeedPost{feedItem(w.posts["d"], *m.User("d")), feedItem(w.posts["a"], *m.User("a"))}}
		if result := m.CheckFeed("b", "public", nil, page, r, true); len(result.Violations)+len(result.Errors)+len(result.Thorough) != 0 {
			t.Fatalf("posts seen through an unknown creation: %+v", result)
		}
		if result := m.CheckFeed("b", "public", nil, client.FeedPage{}, r, true); len(result.Violations)+len(result.Errors)+len(result.Thorough) != 0 {
			t.Fatalf("posts hidden for an unknown creation: %+v", result)
		}
	}
}
