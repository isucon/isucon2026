package model

import (
	"strings"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestBadPageSizeStillChecksVisibility は、件数の誤ったページ（続きがあるのに 50 件に満たない、
// 50 件を超える）でも、返った投稿の閲覧可否を確かめて失格にすることを確かめる。
func TestBadPageSizeStillChecksVisibility(t *testing.T) {
	w := newWorld(t)
	m := w.m
	item := func(author string) client.FeedPost { return feedItem(w.posts[author], *m.User(author)) }
	stranger := client.FeedPost{ID: "p-unknown", Author: client.FeedAuthor{ID: "e"}, PostedAt: w.c.now()}
	m.PageSize = 2
	for name, tc := range map[string]struct {
		viewer, feed string
		items        []client.FeedPost
		hasMore      bool
	}{
		"short page with a blocked post":        {"a", "public", []client.FeedPost{item("d")}, true},
		"short page with a friends-only post":   {"d", "friends", []client.FeedPost{item("b")}, true},
		"short page with an unknown post":       {"b", "public", []client.FeedPost{stranger}, true},
		"long page with a blocked post":         {"a", "public", []client.FeedPost{item("d"), item("c"), item("b")}, false},
		"short ranked page with a blocked post": {"d", "friends-of-friends", []client.FeedPost{item("a")}, true},
	} {
		for _, thorough := range []bool{false, true} {
			result := m.CheckFeed(tc.viewer, tc.feed, nil, client.FeedPage{Items: tc.items, HasMore: tc.hasMore}, w.later(), thorough)
			if len(result.Violations) == 0 {
				t.Errorf("%s (thorough=%t): no violation: %+v", name, thorough, result)
			}
			if !strings.Contains(strings.Join(result.Errors, "\n"), "件です") {
				t.Errorf("%s (thorough=%t): no page size error: %+v", name, thorough, result)
			}
		}
	}
	// 続きがあるのに空のページも件数の誤りにする（末尾の投稿がなくても欠落の確認で落ちない）。
	for _, feed := range []string{"public", "friends-of-friends"} {
		result := m.CheckFeed("b", feed, nil, client.FeedPage{HasMore: true}, w.later(), true)
		if len(result.Errors) != 1 || len(result.Violations) != 0 {
			t.Errorf("%s: empty page with more: %+v", feed, result)
		}
	}
}

// TestCursorRepeatedOnNextPage は、ページ送りの位置の投稿や前のページの投稿が次のページに
// また返るのを重複のエラーにすることを確かめる。
func TestCursorRepeatedOnNextPage(t *testing.T) {
	w := newWorld(t)
	m := w.m
	item := func(author string) client.FeedPost { return feedItem(w.posts[author], *m.User(author)) }
	cursor := item("d")
	result := m.CheckFeed("b", "public", &cursor, client.FeedPage{Items: []client.FeedPost{cursor, item("a")}}, w.later(), true)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "前のページ") {
		t.Fatalf("cursor repeated: %+v", result)
	}

	// 2 件ずつのページで、1 ページ目の先頭の投稿が 2 ページ目にまた返る。
	m.PageSize = 2
	m.BeginFriendship("b", "d", true).Applied()
	w.c.tick()
	var s Scroll
	first := client.FeedPage{Items: []client.FeedPost{item("d"), item("c")}, HasMore: true}
	r := w.later()
	if result := m.CheckScroll("b", "friends", &s, first, r, true); len(result.Errors)+len(result.Thorough)+len(result.Violations) != 0 {
		t.Fatalf("first page: %+v", result)
	}
	s.Advance(first, r.Sent)
	result = m.CheckScroll("b", "friends", &s, client.FeedPage{Items: []client.FeedPost{item("d"), item("a")}}, w.later(), true)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "前のページ") {
		t.Fatalf("first page repeated: %+v", result)
	}
}

// TestSameTimeAtPageBoundary は、ページ送りの位置と同じ時刻の投稿の欠落を、同じ時刻の
// 並べ方によらずに確かめることを確かめる。前のページにも次のページにも無く、最初のページから
// ずっと見えるべきだった投稿だけを欠落とする。
func TestSameTimeAtPageBoundary(t *testing.T) {
	setup := func() (*world, func(string) client.FeedPost) {
		w := newWorld(t)
		m := w.m
		// b の public フィードには a と d の公開投稿が出る。2 つを同じ時刻にする。
		w.posts["a"].PostedAt = w.posts["d"].PostedAt
		m.PageSize = 1
		return w, func(author string) client.FeedPost { return feedItem(w.posts[author], *m.User(author)) }
	}
	clean := func(result FeedResult) bool {
		return len(result.Errors)+len(result.Thorough)+len(result.Violations) == 0
	}
	// 同じ時刻の投稿をどちらの順に並べても、2 ページで両方が返れば正しい。
	for _, order := range [][2]string{{"d", "a"}, {"a", "d"}} {
		w, item := setup()
		var s Scroll
		r := w.later()
		first := client.FeedPage{Items: []client.FeedPost{item(order[0])}, HasMore: true}
		if result := w.m.CheckScroll("b", "public", &s, first, r, true); !clean(result) {
			t.Fatalf("%v: first page: %+v", order, result)
		}
		s.Advance(first, r.Sent)
		if result := w.m.CheckScroll("b", "public", &s, client.FeedPage{Items: []client.FeedPost{item(order[1])}}, w.later(), true); !clean(result) {
			t.Fatalf("%v: second page: %+v", order, result)
		}
		// 2 ページ目で同じ時刻の投稿を落とすのは欠落。
		if result := w.m.CheckScroll("b", "public", &s, client.FeedPage{}, w.later(), true); len(result.Thorough) != 1 || !strings.Contains(result.Thorough[0], "見えるべき") {
			t.Fatalf("%v: same-time post dropped: %+v", order, result)
		}
	}

	// 1 ページ目を読んだあとで見えるようになった同じ時刻の投稿は、並べ方によっては 1 ページ目の
	// 範囲にあったので、2 ページ目に無くてもよい。
	w, item := setup()
	m := w.m
	m.BeginUpdateVisibility("p-a", "friends").Applied()
	var s Scroll
	r := w.later()
	first := client.FeedPage{Items: []client.FeedPost{item("d")}, HasMore: true}
	if result := m.CheckScroll("b", "public", &s, first, r, true); !clean(result) {
		t.Fatalf("first page before the change: %+v", result)
	}
	s.Advance(first, r.Sent)
	m.BeginUpdateVisibility("p-a", "public").Applied()
	if result := m.CheckScroll("b", "public", &s, client.FeedPage{}, w.later(), true); !clean(result) {
		t.Fatalf("post that became visible after the first page: %+v", result)
	}

	// 前のページを知らなければ（CheckFeed）、同じ時刻の投稿は確かめない。
	w, item = setup()
	cursor := item("d")
	if result := w.m.CheckFeed("b", "public", &cursor, client.FeedPage{}, w.later(), true); !clean(result) {
		t.Fatalf("without the previous pages: %+v", result)
	}
}
