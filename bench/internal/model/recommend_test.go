package model

import (
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// newRankingWorld は v の友達の友達フィードに a1、a2、x の投稿が出る世界を作る。
//
//	v ─ f1 ─ a1 ─ x
//	│        │    │
//	└── f2 ──┴────┘     f1 ─ a2 ─ x
//
// 点数は a1 = 3×2 + (0 + 0 + 1) = 7、a2 = 3×1 + (0 + 1) = 4、x = 3×1 + (2 + 1 + 0) = 6。
func newRankingWorld(t *testing.T) *world {
	m, c := newTestModel()
	w := &world{m: m, c: c, posts: map[string]*Post{}}
	for _, id := range []string{"v", "f1", "f2", "a1", "a2", "x"} {
		m.AddUser(User{ID: id, Username: id, SecretKey: "secret-" + id, Region: "asia"})
	}
	c.tick()
	for _, f := range [][2]string{{"v", "f1"}, {"v", "f2"}, {"f1", "a1"}, {"f2", "a1"}, {"f1", "a2"}, {"a1", "x"}, {"a2", "x"}, {"f2", "x"}} {
		m.BeginFriendship(f[0], f[1], true).Applied()
	}
	c.tick()
	open := m.BeginOpenWindow("asia")
	c.tick()
	w.window = Window{ID: "w1", Region: "asia", LocalDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	m.OpenedWindow(open, w.window)
	for _, author := range []string{"v", "a1", "a2", "x"} {
		c.tick()
		creation := m.BeginCreatePost(author)
		c.tick()
		p := Post{ID: "p-" + author, AuthorID: author, WindowID: "w1", ImageID: "img-" + author, Caption: "hi", PostedAt: c.now()}
		m.Created(creation, p, "friends_of_friends")
		w.posts[author] = m.Post(p.ID)
	}
	c.tick()
	return w
}

func TestCheckRecommendedFeed(t *testing.T) {
	w := newRankingWorld(t)
	m := w.m
	item := func(author string, score int64) client.FeedPost {
		it := feedItem(w.posts[author], *m.User(author))
		it.Visibility = "friends_of_friends"
		it.Score = &score
		return it
	}
	check := func(page client.FeedPage, before *client.FeedPost, r Read) FeedResult {
		t.Helper()
		return m.CheckFeed("v", "friends-of-friends", before, page, r, true)
	}
	clean := func(result FeedResult) bool {
		return len(result.Violations)+len(result.Errors)+len(result.Thorough) == 0
	}

	r := w.later()
	if result := check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("x", 6), item("a2", 4)}}, nil, r); !clean(result) {
		t.Fatalf("valid page: %+v", result)
	}
	// 点数が違う。
	result := check(client.FeedPage{Items: []client.FeedPost{item("a1", 8), item("x", 6), item("a2", 4)}}, nil, r)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "点数") {
		t.Fatalf("wrong score: %+v", result)
	}
	// 新しい順のまま（点数の順でない）。
	result = check(client.FeedPage{Items: []client.FeedPost{item("x", 6), item("a2", 4), item("a1", 7)}}, nil, r)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "おすすめ順") {
		t.Fatalf("wrong order: %+v", result)
	}
	// 点数がない。
	missingScore := item("a1", 7)
	missingScore.Score = nil
	result = check(client.FeedPage{Items: []client.FeedPost{missingScore, item("x", 6), item("a2", 4)}}, nil, r)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "点数がありません") {
		t.Fatalf("missing score: %+v", result)
	}
	// 点数がなくても、見えてはいけない投稿（v の友達 f1 は友達の友達フィードに出ない）は違反。
	friendPost := m.BeginCreatePost("f1")
	m.Created(friendPost, Post{ID: "p-f1", AuthorID: "f1", WindowID: "w1", ImageID: "img-f1", Caption: "hi", PostedAt: w.c.tick()}, "friends")
	leaked := feedItem(m.Post("p-f1"), *m.User("f1"))
	result = check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("x", 6), item("a2", 4), leaked}}, nil, w.later())
	if len(result.Violations) != 1 || !strings.Contains(result.Violations[0], "見えてはいけない") || len(result.Errors) != 1 {
		t.Fatalf("leaked post without a score: %+v", result)
	}
	r = w.later()

	// 欠落。次のページがあるなら、末尾より確実に下の a2 は欠落ではない。
	result = check(client.FeedPage{Items: []client.FeedPost{item("a1", 7)}}, nil, r)
	if len(result.Thorough) != 2 {
		t.Fatalf("missing posts: %+v", result)
	}
	m.PageSize = 2
	if result := check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("x", 6)}, HasMore: true}, nil, r); !clean(result) {
		t.Fatalf("first of two pages: %+v", result)
	}
	before := item("x", 6)
	if result := check(client.FeedPage{Items: []client.FeedPost{item("a2", 4)}}, &before, r); !clean(result) {
		t.Fatalf("second page: %+v", result)
	}
	result = check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("a2", 4)}}, &before, r)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "ページ送り") {
		t.Fatalf("post above the cursor: %+v", result)
	}
	result = check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("x", 6), item("a2", 4)}}, nil, r)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "件です") {
		t.Fatalf("too many posts: %+v", result)
	}
	m.PageSize = FeedPageSize

	// f1 が a2 に RealMoji を付けると、a2 は 4 + 10 = 14 点になる。付けている最中はどちらでもよい。
	sent := w.c.tick()
	reaction := m.BeginReplaceRealMoji("p-a2", "f1")
	during := m.ReadBetween(sent, w.c.tick())
	for _, page := range [][]client.FeedPost{
		{item("a1", 7), item("x", 6), item("a2", 4)},
		{item("a2", 14), item("a1", 7), item("x", 6)},
	} {
		if result := check(client.FeedPage{Items: page}, nil, during); !clean(result) {
			t.Fatalf("during the reaction: %+v", result)
		}
	}
	// M と C は決まっているので、4 と 14 の間の点数は出ない。
	result = check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("x", 6), item("a2", 6)}}, nil, during)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "点数") {
		t.Fatalf("impossible score during the reaction: %+v", result)
	}
	m.ReplacedRealMoji(reaction, "rm-1")
	r = w.later()
	if result := check(client.FeedPage{Items: []client.FeedPost{item("a2", 14), item("a1", 7), item("x", 6)}}, nil, r); !clean(result) {
		t.Fatalf("after the reaction: %+v", result)
	}
	result = check(client.FeedPage{Items: []client.FeedPost{item("a1", 7), item("x", 6), item("a2", 4)}}, nil, r)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "点数") {
		t.Fatalf("stale score: %+v", result)
	}

	// 同じ人がコメントもしても 1 人。友達でない人（x）のコメントは数えない。
	comment := m.BeginCreateComment("p-a2", "f1")
	m.CreatedComment(comment, client.Comment{ID: "c-1", Body: "nice", CreatedAt: w.c.tick()}, "f1")
	stranger := m.BeginCreateComment("p-a2", "x")
	m.CreatedComment(stranger, client.Comment{ID: "c-2", Body: "nice", CreatedAt: w.c.tick()}, "x")
	if result := check(client.FeedPage{Items: []client.FeedPost{item("a2", 14), item("a1", 7), item("x", 6)}}, nil, w.later()); !clean(result) {
		t.Fatalf("after the comments: %+v", result)
	}

	// f2 が退会すると、f2 の反応は数えないが、f2 を通じた友達関係は数え続ける。
	pending := m.BeginCreateComment("p-a1", "f2")
	m.BeginDeleteAccount("f2").Applied()
	pending.Unknown()
	if result := check(client.FeedPage{Items: []client.FeedPost{item("a2", 14), item("a1", 7), item("x", 6)}}, nil, w.later()); !clean(result) {
		t.Fatalf("after f2 left: %+v", result)
	}
	if stats := m.RankingStats(); stats.Items == 0 || stats.Exact == 0 || stats.Mutual.Max != 2 || stats.Score.Max != 14 {
		t.Fatalf("stats: %+v", stats)
	}
}
