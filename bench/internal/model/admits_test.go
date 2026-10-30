package model

import (
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// おすすめの点数は 10E + 3M + min(C, 30)。各項の範囲のどの組み合わせからも出ない点数は、
// 下限と上限の間でも受け付けない。
func TestScoreBoundsAdmits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		b       scoreBounds
		admits  []int64
		rejects []int64
	}{
		{"exact", scoreBounds{mutual: [2]int64{2, 2}, connections: [2]int64{1, 1}}, []int64{7}, []int64{6, 8}},
		{"engagement", scoreBounds{mutual: [2]int64{1, 1}, engagement: [2]int64{0, 2}}, []int64{3, 13, 23}, []int64{4, 12, 22, 33}},
		{"mutual", scoreBounds{mutual: [2]int64{1, 2}}, []int64{3, 6}, []int64{4, 5}},
		{"connections", scoreBounds{mutual: [2]int64{1, 1}, connections: [2]int64{0, 2}}, []int64{3, 4, 5}, []int64{2, 6}},
		{"connections-capped", scoreBounds{connections: [2]int64{29, 40}}, []int64{29, 30}, []int64{31}},
		{"all", scoreBounds{mutual: [2]int64{0, 1}, connections: [2]int64{0, 1}, engagement: [2]int64{0, 1}}, []int64{0, 1, 3, 4, 10, 11, 13, 14}, []int64{2, 5, 9, 12, 15}},
	} {
		for _, score := range tc.admits {
			if !tc.b.admits(score) {
				t.Errorf("%s: %d must be admitted", tc.name, score)
			}
		}
		for _, score := range tc.rejects {
			if tc.b.admits(score) {
				t.Errorf("%s: %d must be rejected", tc.name, score)
			}
		}
	}
}

// 友達関係が 1 つだけ不確かなとき、共通の友達の数は 1 か 2 なので、点数は 3 か 6 だけで、
// その間の 4 と 5 は誤り。
func TestRecommendedScoreWithOneUncertainFriendship(t *testing.T) {
	w := newWorld(t)
	m := w.m
	// a と c は b を介して友達の友達。c-e は確実な友達で、a-e は結果がわからない。
	m.BeginFriendship("c", "e", true).Applied()
	m.BeginFriendship("a", "e", true).Unknown()
	r := w.later()
	for score, ok := range map[int64]bool{3: true, 4: false, 5: false, 6: true} {
		item := feedItem(w.posts["c"], *m.User("c"))
		item.Visibility = "friends_of_friends"
		item.Score = &score
		result := m.CheckFeed("a", "friends-of-friends", nil, client.FeedPage{Items: []client.FeedPost{item}}, r, false)
		if len(result.Violations) != 0 || (len(result.Errors) == 0) != ok {
			t.Errorf("score %d: violations=%v errors=%v", score, result.Violations, result.Errors)
		}
	}
}
