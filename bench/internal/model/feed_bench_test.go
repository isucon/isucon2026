package model

import (
	"fmt"
	"math/rand/v2"
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

// rankingBenchModel は最上位の実装に近い規模のモデル。初期データの 5,000 人が優先的選択で
// 1 人 5 本ずつ友達になり、走行中に 2,000 人が来て、走行中の利用者から友達の多い人ほど選んで
// 3〜11 人（1 割は 15〜31 人）と友達になる。走行中の利用者の 9 割が投稿し、投稿 1 件に
// 1 件ほど、投稿者の友達が RealMoji を付ける。
func rankingBenchModel() (*Model, []string) {
	r := rand.New(rand.NewPCG(1, 2))
	m := New()
	now := time.Now().Add(-time.Minute)
	m.now = func() time.Time { return now }
	id := func(i int) string { return fmt.Sprintf("u%d", i) }
	const initial, newcomers = 5000, 2000
	friends := make([][]int, initial+newcomers)
	befriend := func(a, b int) {
		if a == b || slices.Contains(friends[a], b) {
			return
		}
		friends[a] = append(friends[a], b)
		friends[b] = append(friends[b], a)
		m.BeginFriendship(id(a), id(b), true).Applied()
	}
	for i := range initial + newcomers {
		m.AddUser(User{ID: id(i), Username: id(i), SecretKey: "k", Region: "asia"})
	}
	var ends []int
	for i := range initial {
		for range 5 {
			if len(ends) > 0 {
				befriend(i, ends[r.IntN(len(ends))])
			}
		}
		ends = append(ends, i)
		for range friends[i] {
			ends = append(ends, i)
		}
	}
	var active, runEnds []int
	for i := range initial {
		if r.Float64() < 0.1 {
			active = append(active, i)
			runEnds = append(runEnds, i)
		}
	}
	for i := initial; i < initial+newcomers; i++ {
		circle := 3 + r.IntN(9)
		if r.Float64() < 0.1 {
			circle = 15 + r.IntN(17)
		}
		for range circle*6/10 + 1 {
			t := runEnds[r.IntN(len(runEnds))]
			befriend(i, t)
			runEnds = append(runEnds, t)
		}
		active = append(active, i)
		runEnds = append(runEnds, i)
	}
	w := m.BeginOpenWindow("asia")
	m.OpenedWindow(w, Window{ID: "w1", Region: "asia", LocalDate: now})
	for k, a := range active {
		if r.Float64() >= 0.9 {
			continue
		}
		c := m.BeginCreatePost(id(a))
		p := Post{ID: fmt.Sprintf("p%d", a), AuthorID: id(a), WindowID: "w1", ImageID: "i", Caption: "c", PostedAt: now.Add(time.Duration(k) * time.Millisecond)}
		m.Created(c, p, []string{"public", "friends", "friends_of_friends"}[r.IntN(3)])
		if len(friends[a]) > 0 && r.Float64() < 0.7 {
			reactor := friends[a][r.IntN(len(friends[a]))]
			m.ReplacedRealMoji(m.BeginReplaceRealMoji(p.ID, id(reactor)), client.RealMoji{ID: fmt.Sprintf("rm%d", k)})
		}
	}
	slices.SortFunc(active, func(x, y int) int { return len(friends[y]) - len(friends[x]) })
	var viewers []string
	for _, a := range []int{active[0], active[len(active)/10], active[len(active)/2]} {
		viewers = append(viewers, id(a))
	}
	return m, viewers
}

// BenchmarkCheckRecommendedFeed は、友達の友達フィード 1 ページ（おすすめ順）の検証にかかる
// 時間を、友達の最も多い人、上位 1 割の人、中央の人について測る。
func BenchmarkCheckRecommendedFeed(b *testing.B) {
	m, viewers := rankingBenchModel()
	for i, viewer := range viewers {
		now := time.Now()
		read := Read{Sent: now, Received: now}
		var page client.FeedPage
		m.feedCandidates(viewer, "friends-of-friends", read, func(p *Post) {
			if len(page.Items) < FeedPageSize {
				score := int64(0)
				page.Items = append(page.Items, client.FeedPost{ID: p.ID, Author: client.FeedAuthor{ID: p.AuthorID, Username: p.AuthorID}, Caption: p.Caption, PostedAt: p.PostedAt, Score: &score})
			}
		})
		candidates := 0
		m.feedCandidates(viewer, "friends-of-friends", read, func(*Post) { candidates++ })
		for _, thorough := range []bool{false, true} {
			b.Run(fmt.Sprintf("viewer=%d/friends=%d/candidates=%d/thorough=%t", i, len(m.friendCandidates[viewer]), candidates, thorough), func(b *testing.B) {
				for b.Loop() {
					m.checkFeedLocked(viewer, "friends-of-friends", nil, page, read, thorough)
				}
			})
		}
	}
}
