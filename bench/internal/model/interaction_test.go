package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestRealMojiTieOrder は、作成時刻が同じ RealMoji が ID の降順に並んでいなければ、並び順の
// 誤りとすることを確かめる（アプリは created_at DESC, id DESC で並べ、ページもこの順で区切る）。
func TestRealMojiTieOrder(t *testing.T) {
	w := newWorld(t)
	m := w.m
	at := w.c.tick()
	item := func(reactor, id string) client.RealMoji {
		return client.RealMoji{ID: id, Reactor: client.UserSummary{ID: reactor}, ReactionType: "like", ImageURL: "/api/images/img-" + id, CreatedAt: at}
	}
	for _, x := range [][2]string{{"b", "rm-z"}, {"c", "rm-a"}} {
		m.ReplacedRealMoji(m.BeginReplaceRealMoji("p-a", x[0]), item(x[0], x[1]))
	}
	r := w.later()
	if problems := m.CheckRealMojis("p-a", client.RealMojiPage{Items: []client.RealMoji{item("b", "rm-z"), item("c", "rm-a")}}, r); len(problems) != 0 {
		t.Fatalf("ID order: %v", problems)
	}
	problems := m.CheckRealMojis("p-a", client.RealMojiPage{Items: []client.RealMoji{item("c", "rm-a"), item("b", "rm-z")}}, r)
	if len(problems) != 1 || !strings.Contains(problems[0], "新しい順") {
		t.Fatalf("reversed ID order: %v", problems)
	}
	early := item("b", "rm-z")
	early.CreatedAt = at.Add(-time.Microsecond)
	if problems := m.CheckRealMojis("p-a", client.RealMojiPage{Items: []client.RealMoji{item("c", "rm-a"), early}}, r); len(problems) != 1 || !strings.Contains(problems[0], "内容") {
		t.Fatalf("time order: %v", problems)
	}
}

// TestRealMojiIDReuse は、別の人の RealMoji と同じ ID を返した作成を記録せず、一覧で 2 人が
// 同じ ID を共有していれば誤りとすることを確かめる。
func TestRealMojiIDReuse(t *testing.T) {
	w := newWorld(t)
	m := w.m
	at := w.c.tick()
	item := func(reactor string) client.RealMoji {
		return client.RealMoji{ID: "same", Reactor: client.UserSummary{ID: reactor}, ReactionType: "like", ImageURL: "/api/images/img-" + reactor, CreatedAt: at}
	}
	if !m.ReplacedRealMoji(m.BeginReplaceRealMoji("p-a", "b"), item("b")) {
		t.Fatal("first RealMoji rejected")
	}
	second := m.BeginReplaceRealMoji("p-a", "c")
	if m.ReplacedRealMoji(second, item("c")) {
		t.Fatal("reused ID recorded")
	}
	second.Unknown()
	problems := m.CheckRealMojis("p-a", client.RealMojiPage{Items: []client.RealMoji{item("c"), item("b")}}, w.later())
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "重複") {
		t.Fatalf("shared ID accepted: %v", problems)
	}
	// 空の ID も記録しない（RealMoji がないことと区別できない）。
	empty := m.BeginReplaceRealMoji("p-b", "a")
	if m.ReplacedRealMoji(empty, client.RealMoji{}) {
		t.Fatal("empty ID recorded")
	}
	// 同じ人が同じ投稿の RealMoji を置き換えて ID が変わらない実装は許し、記録を新しい応答で
	// 書き換える。
	again := item("b")
	again.ReactionType, again.CreatedAt = "smile", w.c.tick()
	if !m.ReplacedRealMoji(m.BeginReplaceRealMoji("p-a", "b"), again) {
		t.Fatal("replacement keeping its own ID rejected")
	}
	if problems := m.CheckRealMojis("p-a", client.RealMojiPage{Items: []client.RealMoji{again}}, w.later()); len(problems) != 0 {
		t.Fatalf("replacement keeping its own ID: %v", problems)
	}
}

// TestRealMojiMissingBeforeLastItem は、続きのある（50 件の）ページでも、最後の項目より
// 新しい既知の RealMoji が無ければ欠落とし、古いものは次のページにあってよいとすることを
// 確かめる。
func TestRealMojiMissingBeforeLastItem(t *testing.T) {
	w := newWorld(t)
	m := w.m
	base := w.c.tick()
	known := func(reactor string, at time.Time) client.RealMoji {
		return client.RealMoji{ID: "rm-" + reactor, Reactor: client.UserSummary{ID: reactor}, ReactionType: "like", ImageURL: "/api/images/img-" + reactor, CreatedAt: at}
	}
	newer, older := known("b", base.Add(time.Hour)), known("c", base.Add(-time.Hour))
	for _, x := range []client.RealMoji{newer, older} {
		m.ReplacedRealMoji(m.BeginReplaceRealMoji("p-a", x.Reactor.ID), x)
	}
	// bench の知らない人の RealMoji で埋めたページは、それ自体が誤りなので、ここでは欠落の
	// 判定だけを見る。
	var items []client.RealMoji
	for i := range InteractionPageSize {
		items = append(items, client.RealMoji{ID: fmt.Sprintf("x%02d", i), Reactor: client.UserSummary{ID: fmt.Sprintf("x%02d", i)}, CreatedAt: base})
	}
	problems := m.CheckRealMojis("p-a", client.RealMojiPage{Items: items, HasMore: true}, w.later())
	missing := 0
	for _, problem := range problems {
		if strings.Contains(problem, "ありません") {
			missing++
			if !strings.Contains(problem, "b の") {
				t.Errorf("older RealMoji required on the first page: %s", problem)
			}
		}
	}
	if missing != 1 {
		t.Fatalf("missing=%d problems=%v", missing, problems)
	}
}

// TestFriendRequestPending は、bench が送った申請が、閉じる書き込み、2 人の友達関係の成立、
// ブロックで保留中でなくなり、拒否された書き込みは無視することを確かめる。
func TestFriendRequestPending(t *testing.T) {
	w := newWorld(t)
	m, c := w.m, w.c
	request := func(from, to, id string) {
		m.RequestedFriend(m.BeginFriendRequest(from, to), id)
	}
	request("c", "e", "rq1")
	request("a", "e", "rq2")
	if got := m.FriendRequestPending("rq1", w.later()); got != True {
		t.Fatalf("pending: %s", got)
	}
	if got := m.FriendRequestPending("unknown", w.later()); got != Unknown {
		t.Fatalf("unknown request: %s", got)
	}
	sent := c.tick()
	closing := m.BeginCloseFriendRequest("rq1")
	if got := m.FriendRequestPending("rq1", m.ReadBetween(sent, c.tick())); got != Unknown {
		t.Fatalf("while closing: %s", got)
	}
	closing.Rejected()
	if got := m.FriendRequestPending("rq1", w.later()); got != True {
		t.Fatalf("after a rejected close: %s", got)
	}
	// 相手からの申請で友達になると、2 人の間の申請は閉じる。
	m.BeginFriendship("e", "c", true).Applied()
	if got := m.FriendRequestPending("rq1", w.later()); got != False {
		t.Fatalf("after friendship: %s", got)
	}
	m.BeginBlock("e", "a").Applied()
	m.BeginUnblock("e", "a").Applied()
	if got := m.FriendRequestPending("rq2", w.later()); got != False {
		t.Fatalf("after block and unblock: %s", got)
	}
}
