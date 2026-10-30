package model

import (
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
	for _, x := range [][2]string{{"b", "rm-z"}, {"c", "rm-a"}} {
		m.ReplacedRealMoji(m.BeginReplaceRealMoji("p-a", x[0]), x[1])
	}
	item := func(reactor, id string) client.RealMoji {
		return client.RealMoji{ID: id, Reactor: client.UserSummary{ID: reactor}, CreatedAt: at}
	}
	r := w.later()
	if problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{item("b", "rm-z"), item("c", "rm-a")}}, r); len(problems) != 0 {
		t.Fatalf("ID order: %v", problems)
	}
	problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{item("c", "rm-a"), item("b", "rm-z")}}, r)
	if len(problems) != 1 || !strings.Contains(problems[0], "新しい順") {
		t.Fatalf("reversed ID order: %v", problems)
	}
	later := item("c", "rm-a")
	later.CreatedAt = at.Add(time.Microsecond)
	if problems := m.CheckRealMojis("a", "p-a", client.RealMojiPage{Items: []client.RealMoji{later, item("b", "rm-z")}}, r); len(problems) != 0 {
		t.Fatalf("time order: %v", problems)
	}
}
