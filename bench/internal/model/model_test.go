package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/imageurl"
)

// clock はテスト用の時計。tick で 1 秒進める。
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) tick() time.Time {
	c.t = c.t.Add(time.Second)
	return c.t
}

func newTestModel() (*Model, *clock) {
	c := &clock{t: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	m := New()
	m.now = c.now
	return m, c
}

func TestTimeline(t *testing.T) {
	m, c := newTestModel()
	tl := newTimeline(false)
	read := func(sent, received time.Time) []bool { return tl.possible(Read{Sent: sent, Received: received}) }

	t0 := c.tick()
	c.tick()
	w := m.newWrite()
	begin(w, tl, true) // 送信 t1
	t1 := c.now()
	c.tick()
	if got := read(t0, c.now()); len(got) != 2 {
		t.Fatalf("in-flight write must be uncertain, got %v", got)
	}
	t2 := c.tick()
	w.Applied() // 応答 t2
	if got := read(c.tick(), c.tick()); len(got) != 1 || !got[0] {
		t.Fatalf("applied write read later must be certain, got %v", got)
	}
	if got := read(t0.Add(-time.Hour), t0); len(got) != 1 || got[0] {
		t.Fatalf("read before the write must see the initial value, got %v", got)
	}
	if got := read(t1, t2); len(got) != 2 {
		t.Fatalf("overlapping read must be uncertain, got %v", got)
	}
	stale := tl.possible(Read{Sent: t2.Add(time.Second), Received: t2.Add(2 * time.Second), Staleness: 5 * time.Second})
	if len(stale) != 2 {
		t.Fatalf("staleness must keep a recent write uncertain, got %v", stale)
	}

	w2 := m.newWrite()
	begin(w2, tl, false)
	c.tick()
	w2.Rejected()
	if got := read(c.tick(), c.tick()); len(got) != 1 || !got[0] {
		t.Fatalf("rejected write must be ignored, got %v", got)
	}
	w3 := m.newWrite()
	begin(w3, tl, false)
	c.tick()
	w3.Unknown()
	if got := read(c.tick().Add(time.Hour), c.tick().Add(time.Hour)); len(got) != 2 {
		t.Fatalf("write with unknown outcome must stay uncertain, got %v", got)
	}
}

// world は asia の a、b、c、d と europe の e。a-b、b-c が友達（a と c は友達の友達）、d は
// a をブロックしている。a、b、c、d は今日投稿済みで、e は投稿していない。
type world struct {
	m      *Model
	c      *clock
	window Window
	posts  map[string]*Post
}

func newWorld(t *testing.T) *world {
	m, c := newTestModel()
	w := &world{m: m, c: c, posts: map[string]*Post{}}
	for _, u := range []User{{ID: "a", Region: "asia"}, {ID: "b", Region: "asia"}, {ID: "c", Region: "asia"}, {ID: "d", Region: "asia"}, {ID: "e", Region: "europe"}} {
		u.Username, u.SecretKey = u.ID, "secret-"+u.ID
		m.AddUser(u)
	}
	c.tick()
	for _, f := range [][2]string{{"a", "b"}, {"b", "c"}} {
		m.BeginFriendship(f[0], f[1], true).Applied()
	}
	m.BeginBlock("d", "a").Applied()
	c.tick()
	open := m.BeginOpenWindow("asia")
	c.tick()
	w.window = Window{ID: "w1", Region: "asia", LocalDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	m.OpenedWindow(open, w.window)
	for _, spec := range [][2]string{{"a", "public"}, {"b", "friends"}, {"c", "friends_of_friends"}, {"d", "public"}} {
		c.tick()
		creation := m.BeginCreatePost(spec[0])
		c.tick()
		p := Post{ID: "p-" + spec[0], AuthorID: spec[0], WindowID: "w1", ImageID: "img-" + spec[0], Caption: "hi", PostedAt: c.now()}
		m.Created(creation, p, spec[1])
		w.posts[spec[0]] = m.Post(p.ID)
	}
	c.tick()
	return w
}

// later は、ここまでの書き込みがすべて確実に反映されている読み取り。
func (w *world) later() Read {
	return w.m.ReadBetween(w.c.tick(), w.c.tick())
}

func (w *world) inFeed(viewer, author, feed string, r Read) Tri {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	v := w.m.newView(w.m.users[viewer], r)
	return v.inFeedTri(w.posts[author], feed)
}

func TestOracle(t *testing.T) {
	w := newWorld(t)
	r := w.later()
	for _, tc := range []struct {
		viewer, author, feed string
		want                 Tri
	}{
		{"b", "a", "public", True},
		{"b", "a", "friends", True},
		{"a", "b", "friends", True},             // friends の投稿を友達が見る
		{"c", "b", "friends", True},             // b と c も友達
		{"a", "c", "friends-of-friends", True},  // a と c は b を通じた友達の友達
		{"a", "c", "friends", False},            // 友達ではない
		{"d", "b", "friends", False},            // friends の投稿は友達以外に見えない
		{"a", "d", "public", False},             // d は a をブロックしている
		{"d", "a", "public", False},             // ブロックは両方向に効く
		{"e", "a", "public", False},             // e は投稿していない（そもそも別リージョン）
		{"a", "a", "public", False},             // 自分の投稿はフィードに出ない
		{"b", "c", "friends-of-friends", False}, // 友達は友達の友達フィードに出ない
	} {
		if got := w.inFeed(tc.viewer, tc.author, tc.feed, r); got != tc.want {
			t.Errorf("%s sees %s's post in %s: got %s, want %s", tc.viewer, tc.author, tc.feed, got, tc.want)
		}
	}
}

func TestOracleConcurrentWrites(t *testing.T) {
	w := newWorld(t)
	m, c := w.m, w.c

	// a と c の友達関係の作成中に読んだ場合は、friends フィードに c の投稿が出るかは不明。
	sent := c.tick()
	friendship := m.BeginFriendship("a", "c", true)
	if got := w.inFeed("a", "c", "friends", m.ReadBetween(sent, c.tick())); got != Unknown {
		t.Fatalf("in-flight friendship: got %s", got)
	}
	friendship.Applied()
	if got := w.inFeed("a", "c", "friends", w.later()); got != True {
		t.Fatalf("after friendship: got %s", got)
	}

	// 公開範囲の変更中は、public フィードに出るかは不明。変更後は出ない。
	sent = c.tick()
	update := m.BeginUpdateVisibility("p-a", "friends")
	if got := w.inFeed("b", "a", "public", m.ReadBetween(sent, c.tick())); got != Unknown {
		t.Fatalf("in-flight visibility change: got %s", got)
	}
	update.Applied()
	if got := w.inFeed("b", "a", "public", w.later()); got != False {
		t.Fatalf("after visibility change: got %s", got)
	}

	// b が自分の投稿を削除すると、b には他人の投稿が見えなくなる（ゲートが閉じる）。
	m.BeginDeletePost("p-b").Applied()
	if got := w.inFeed("b", "a", "friends", w.later()); got != False {
		t.Fatalf("viewer without a live post: got %s", got)
	}

	// 次の投稿期間が開くと、前の期間の投稿は見えなくなる。開いている最中は不明。
	sent = c.tick()
	open := m.BeginOpenWindow("asia")
	if got := w.inFeed("c", "d", "public", m.ReadBetween(sent, c.tick())); got != Unknown {
		t.Fatalf("while the next window opens: got %s", got)
	}
	m.OpenedWindow(open, Window{ID: "w2", Region: "asia", LocalDate: w.window.LocalDate.AddDate(0, 0, 1)})
	if got := w.inFeed("c", "d", "public", w.later()); got != False {
		t.Fatalf("after the window ended: got %s", got)
	}
}

func feedItem(p *Post, author User) client.Post {
	exp := time.Now().Add(time.Hour).Unix()
	return client.Post{
		ID: p.ID, Author: client.UserSummary{ID: author.ID, Username: author.Username}, Caption: p.Caption, PostedAt: p.PostedAt,
		Visibility: p.visibility.initial, ImageURL: fmt.Sprintf("/api/images/%s?exp=%d&sig=%s", p.ImageID, exp, imageurl.Sign(author.SecretKey, p.ImageID, exp)),
	}
}

func TestCheckFeed(t *testing.T) {
	w := newWorld(t)
	m := w.m
	item := func(author string) client.Post { return feedItem(w.posts[author], *m.User(author)) }

	// b の public フィードには a と d の公開投稿が出る。新しい順は d、a。
	r := w.later()
	if result := m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{item("d"), item("a")}}, r, true); len(result.Violations)+len(result.Errors)+len(result.Thorough) != 0 {
		t.Fatalf("valid page: %+v", result)
	}
	// a に d（a をブロックしている）の投稿を返すのは違反。
	result := m.CheckFeed("a", "public", nil, client.FeedPage{Items: []client.Post{item("d")}}, r, true)
	if len(result.Violations) != 1 || !strings.Contains(result.Violations[0], "見えてはいけない") {
		t.Fatalf("blocked post: %+v", result)
	}
	// 見えるべき a の投稿が無いのは欠落（エラー）。
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{item("d")}}, r, true)
	if len(result.Violations) != 0 || len(result.Thorough) != 1 || !strings.Contains(result.Thorough[0], "見えるべき") {
		t.Fatalf("missing post: %+v", result)
	}
	// thorough でなければ欠落は確かめない。
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{item("d")}}, r, false)
	if len(result.Violations)+len(result.Errors)+len(result.Thorough) != 0 {
		t.Fatalf("missing post without thorough check: %+v", result)
	}
	// 続きがあるページでは、最後の投稿より古い投稿は次のページにあるので欠落ではない。
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{item("d")}, HasMore: true}, r, true)
	if len(result.Thorough) != 0 {
		t.Fatalf("next page: %+v", result)
	}
	// 古い順に並んでいるのはエラー。
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{item("a"), item("d")}}, r, true)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "新しい順") {
		t.Fatalf("order: %+v", result)
	}
	// 画像 URL の署名が投稿者のものでなければエラー。
	forged := item("d")
	forged.ImageURL = feedItem(w.posts["d"], *m.User("a")).ImageURL
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{forged, item("a")}}, r, true)
	if len(result.Thorough) != 1 || !strings.Contains(result.Thorough[0], "画像 URL") {
		t.Fatalf("forged image URL: %+v", result)
	}
	// 返った投稿は thorough でなくても確かめる。
	result = m.CheckFeed("a", "public", nil, client.FeedPage{Items: []client.Post{item("d")}}, r, false)
	if len(result.Violations) != 1 {
		t.Fatalf("blocked post without thorough check: %+v", result)
	}

	// bench の知らない投稿は違反。ただし結果のわからない投稿作成があればその投稿かもしれない。
	stranger := client.Post{ID: "p-unknown", Author: client.UserSummary{ID: "e"}, PostedAt: w.c.now()}
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{stranger, item("d"), item("a")}}, r, true)
	if len(result.Violations) != 1 || !strings.Contains(result.Violations[0], "存在しない") {
		t.Fatalf("unknown post: %+v", result)
	}
	m.BeginCreatePost("e").Unknown()
	result = m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.Post{stranger, item("d"), item("a")}}, w.later(), true)
	if len(result.Violations) != 0 {
		t.Fatalf("post from an unknown creation: %+v", result)
	}
}

func TestPostLiveWithGrace(t *testing.T) {
	w := newWorld(t)
	m, c := w.m, w.c
	m.BeginDeletePost("p-a").Applied()
	deleted := c.now()
	grace := func(after time.Duration) Tri {
		at := deleted.Add(after)
		return m.PostLive("p-a", Read{Sent: at, Received: at, Staleness: 5 * time.Second})
	}
	if got := grace(3 * time.Second); got != Unknown {
		t.Fatalf("3s after deletion: got %s", got)
	}
	if got := grace(6 * time.Second); got != False {
		t.Fatalf("6s after deletion: got %s", got)
	}
}

func TestFinalChecks(t *testing.T) {
	w := newWorld(t)
	m := w.m
	r := w.later()
	if v := m.CheckOwnPosts("a", []string{"p-a"}, r); len(v) != 0 {
		t.Fatalf("consistent posts: %v", v)
	}
	if v := m.CheckOwnPosts("a", nil, r); len(v) != 1 || !strings.Contains(v[0], "ありません") {
		t.Fatalf("lost post: %v", v)
	}
	if v := m.CheckOwnPosts("a", []string{"p-a", "p-b", "stranger"}, r); len(v) != 2 {
		t.Fatalf("other user's and unknown posts: %v", v)
	}
	m.BeginDeletePost("p-a").Applied()
	if v := m.CheckOwnPosts("a", []string{"p-a"}, w.later()); len(v) != 1 || !strings.Contains(v[0], "削除した投稿") {
		t.Fatalf("deleted post: %v", v)
	}

	// b の友達は a と c。
	if v := m.CheckFriends("b", []string{"a", "c"}, w.later()); len(v) != 0 {
		t.Fatalf("consistent friends: %v", v)
	}
	if v := m.CheckFriends("b", []string{"a", "d"}, w.later()); len(v) != 2 {
		t.Fatalf("missing and extra friends: %v", v)
	}
	if got := m.LoadMonths(); len(got) != 1 || got[0].Month() != time.October {
		t.Fatalf("months=%v", got)
	}
}
