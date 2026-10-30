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
