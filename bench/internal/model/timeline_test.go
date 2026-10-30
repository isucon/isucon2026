package model

import (
	"slices"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestFailedWriteSupersededByLaterWrite は、5xx を返して処理を終えた結果のわからない書き込みが、
// 応答のあとに送って受け付けられた書き込みで上書きされ、候補に残らないことを確かめる。
// タイムアウトした書き込みはアプリがあとで処理することがあるので、候補に残す。
func TestFailedWriteSupersededByLaterWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		settle  func(*Write)
		overlap bool
		want    []string
	}{
		"failed before the later write": {(*Write).Failed, false, []string{"new"}},
		"timed out":                     {(*Write).Unknown, false, []string{"old", "new"}},
		"failed during the later write": {(*Write).Failed, true, []string{"old", "new"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			m, c := w.m, w.c
			first := m.BeginUpdateCaption("p-a", "old")
			c.tick()
			var second *Write
			if tc.overlap {
				second = m.BeginUpdateCaption("p-a", "new")
				c.tick()
				tc.settle(first)
			} else {
				tc.settle(first)
				c.tick()
				second = m.BeginUpdateCaption("p-a", "new")
			}
			c.tick()
			second.Applied()
			r := w.later()
			got := m.posts["p-a"].caption.possible(r)
			slices.Sort(got)
			slices.Sort(tc.want)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("possible captions = %v, want %v", got, tc.want)
			}
			// フィードが古いキャプションを返したら、候補にない限りエラーにする。
			stale := feedItem(w.posts["a"], *m.User("a"))
			stale.Caption = "old"
			result := m.CheckFeed("b", "public", nil, client.FeedPage{Items: []client.FeedPost{feedItem(w.posts["d"], *m.User("d")), stale}}, r, false)
			if stale := len(result.Errors) > 0; stale == slices.Contains(tc.want, "old") {
				t.Fatalf("stale caption: %+v", result)
			}
		})
	}
}
