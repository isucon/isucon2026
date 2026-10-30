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
	if problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{item("b", "rm-z"), item("c", "rm-a")}}, r); len(problems) != 0 {
		t.Fatalf("ID order: %v", problems)
	}
	problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{item("c", "rm-a"), item("b", "rm-z")}}, r)
	if len(problems) != 1 || !strings.Contains(problems[0], "新しい順") {
		t.Fatalf("reversed ID order: %v", problems)
	}
	early := item("b", "rm-z")
	early.CreatedAt = at.Add(-time.Microsecond)
	if problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{item("c", "rm-a"), early}}, r); len(problems) != 1 || !strings.Contains(problems[0], "内容") {
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
	problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{item("c"), item("b")}}, w.later())
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "重複") {
		t.Fatalf("shared ID accepted: %v", problems)
	}
	// 空の ID も記録しない（RealMoji がないことと区別できない）。
	empty := m.BeginReplaceRealMoji("p-b", "a")
	if m.ReplacedRealMoji(empty, client.RealMoji{}) {
		t.Fatal("empty ID recorded")
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
	problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: items, HasMore: true}, w.later())
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
