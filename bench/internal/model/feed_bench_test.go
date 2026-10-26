package model

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// feedBenchModel は負荷走行の終盤に近いモデル。asia の n 人が 1 列に友達でつながり、
// 開いている投稿期間に全員が投稿済みで、pending 件の投稿作成が送信中。
func feedBenchModel(n, pending int) (*Model, []client.FeedPost) {
	m := New()
	m.Staleness = time.Second
	now := time.Now().Add(-time.Minute)
	m.now = func() time.Time { return now }
	id := func(i int) string { return fmt.Sprintf("u%d", i) }
	for i := range n {
		m.AddUser(User{ID: id(i), Username: id(i), SecretKey: "k", Region: "asia"})
	}
	for i := 0; i+1 < n; i++ {
		m.BeginFriendship(id(i), id(i+1), true).Applied()
	}
	w := m.BeginOpenWindow("asia")
	m.OpenedWindow(w, Window{ID: "w1", Region: "asia", LocalDate: now})
	visibilities := []string{"public", "friends", "friends_of_friends"}
	var public []client.FeedPost
	for i := range n {
		c := m.BeginCreatePost(id(i))
		p := Post{ID: fmt.Sprintf("p%d", i), AuthorID: id(i), WindowID: "w1", ImageID: fmt.Sprintf("p%d", i), Caption: "c", PostedAt: now.Add(time.Duration(i) * time.Millisecond)}
		m.Created(c, p, visibilities[i%3])
		if i%3 == 0 {
			public = append(public, client.FeedPost{ID: p.ID, Author: client.FeedAuthor{ID: p.AuthorID, Username: p.AuthorID}, Caption: p.Caption, PostedAt: p.PostedAt, Visibility: "public", PostingWindowID: "w1"})
		}
	}
	for i := range pending {
		m.BeginCreatePost(id(i % n))
	}
	slices.Reverse(public)
	return m, public[:min(len(public), 20)]
}

// BenchmarkCheckFeed は、フィード 1 ページの検証にかかる時間がユーザー数でどう増えるかを測る。
// 署名の検証（imageurl のベンチマークで測る）は含めない。
func BenchmarkCheckFeed(b *testing.B) {
	for _, n := range []int{100, 1000, 1500} {
		m, public := feedBenchModel(n, 300)
		viewer := "u5"
		for _, tc := range []struct {
			feed string
			page client.FeedPage
		}{
			{"public", client.FeedPage{Items: public, HasMore: true}},
			{"friends", client.FeedPage{}},
			{"friends-of-friends", client.FeedPage{}},
			{"following", client.FeedPage{}},
		} {
			b.Run(fmt.Sprintf("users=%d/%s", n, tc.feed), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					now := time.Now()
					m.checkFeedLocked(viewer, tc.feed, nil, tc.page, Read{Sent: now, Received: now, Staleness: time.Second}, true)
				}
			})
		}
	}
}
