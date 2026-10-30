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

// TestReusedPostIDKeepsFirstPost は、受け付けられたほかの投稿と同じ ID の投稿を記録せず、
// 前の投稿の記録を残すことを確かめる。作成は結果のわからないものとして残す。
func TestReusedPostIDKeepsFirstPost(t *testing.T) {
	w := newWorld(t)
	m := w.m
	w.nextDay()
	for _, tc := range []struct{ author, id string }{{"b", "p-b"}, {"c", "p-a2"}} {
		creation := m.BeginCreatePost(tc.author)
		w.c.tick()
		before := *m.Post(tc.id)
		if m.Created(creation, Post{ID: tc.id, AuthorID: tc.author, WindowID: "w2", ImageID: "img-new", Caption: "new", PostedAt: w.c.now()}, "public") {
			t.Fatalf("%s: the reused ID %s was recorded", tc.author, tc.id)
		}
		if after := m.Post(tc.id); after.AuthorID != before.AuthorID || after.WindowID != before.WindowID || after.ImageID != before.ImageID {
			t.Fatalf("%s: the first post was overwritten: %+v", tc.author, after)
		}
		if !m.mayHaveUnknownPost(tc.author, w.later()) {
			t.Fatalf("%s: the creation must stay unknown", tc.author)
		}
	}
}

// TestUnknownPostPrivacy は、bench の知らない投稿（結果のわからない作成でできたかもしれない
// 投稿）でも、投稿を知らなくても決まる閲覧の条件（ブロック、閲覧者のゲート、自分の投稿、
// フィードの対象、作成が入りえた投稿期間）で見えてはいけないものを失格にすることを確かめる。
func TestUnknownPostPrivacy(t *testing.T) {
	for name, tc := range map[string]struct {
		viewer, feed, author string
		setup                func(*world)
		violation            bool
	}{
		"blocked author":          {"a", "public", "d", nil, true},
		"viewer without a post":   {"e", "public", "a", (*world).openEurope, true},
		"own post":                {"b", "public", "b", nil, true},
		"not a friend":            {"a", "friends", "c", nil, true},
		"not followed":            {"a", "following", "b", nil, true},
		"friend in fof":           {"a", "friends-of-friends", "b", nil, true},
		"author without a window": {"b", "public", "e", nil, true},
		"public":                  {"b", "public", "a", nil, false},
		"friend":                  {"a", "friends", "b", nil, false},
		"friend of friend":        {"a", "friends-of-friends", "c", nil, false},
		"europe author":           {"b", "public", "e", (*world).openEurope, false},
	} {
		for _, thorough := range []bool{false, true} {
			w := newWorld(t)
			if tc.setup != nil {
				tc.setup(w)
			}
			w.m.BeginCreatePost(tc.author).Unknown()
			score := int64(0)
			item := client.FeedPost{ID: "p-unknown", Author: client.FeedAuthor{ID: tc.author, Username: tc.author}, Caption: "hi", PostedAt: w.c.now(), Visibility: "public", Score: &score}
			result := w.m.CheckFeed(tc.viewer, tc.feed, nil, client.FeedPage{Items: []client.FeedPost{item}}, w.later(), thorough)
			if got := len(result.Violations) > 0; got != tc.violation {
				t.Errorf("%s (thorough=%t): violation=%t, want %t: %+v", name, thorough, got, tc.violation, result)
			}
		}
	}
}

// openEurope は europe の投稿期間を開く（e はまだ投稿していない）。
func (w *world) openEurope() {
	w.c.tick()
	open := w.m.BeginOpenWindow("europe")
	w.c.tick()
	w.m.OpenedWindow(open, Window{ID: "w-eu", Region: "europe", LocalDate: w.window.LocalDate})
	w.c.tick()
}

// TestUnknownPostInClosedWindow は、5xx を返した前日の作成でできたかもしれない投稿は、前日の
// 投稿期間にしか入らないので、翌日のフィードに出れば失格にすることを確かめる。タイムアウト
// した作成はアプリがあとで処理して翌日の投稿になることがあるので許す。
func TestUnknownPostInClosedWindow(t *testing.T) {
	for _, tc := range []struct {
		settle    func(*Write)
		violation bool
	}{{(*Write).Failed, true}, {(*Write).Unknown, false}} {
		w := newWorld(t)
		m := w.m
		creation := m.BeginCreatePost("b")
		w.c.tick()
		tc.settle(creation.Write)
		w.nextDay()
		// c は翌日にも投稿してゲートを通る。
		next := m.BeginCreatePost("c")
		w.c.tick()
		m.Created(next, Post{ID: "p-c2", AuthorID: "c", WindowID: "w2", ImageID: "img-c2", Caption: "hi", PostedAt: w.c.now()}, "public")
		item := client.FeedPost{ID: "p-unknown", Author: client.FeedAuthor{ID: "b", Username: "b"}, Caption: "hi", PostedAt: w.c.now(), Visibility: "public"}
		page := client.FeedPage{Items: []client.FeedPost{item, feedItem(w.posts["a2"], *m.User("a"))}}
		result := m.CheckFeed("c", "public", nil, page, w.later(), false)
		if got := len(result.Violations) > 0; got != tc.violation {
			t.Errorf("violation=%t, want %t: %+v", got, tc.violation, result)
		}
	}
}
