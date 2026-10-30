package model

import (
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestOwnPostsContent は、走行後の整合性チェックで Memories の投稿の内容（投稿者、投稿期間、
// キャプション、公開範囲、投稿時刻、画像）を記録と比べることを確かめる。
func TestOwnPostsContent(t *testing.T) {
	w := newWorld(t)
	m := w.m
	r := w.later()
	for name, tamper := range map[string]func(*client.Post){
		"投稿者":    func(p *client.Post) { p.Author.Username = "someone" },
		"投稿期間":   func(p *client.Post) { p.PostingWindowID = "w-other" },
		"キャプション": func(p *client.Post) { p.Caption = "tampered" },
		"公開範囲":   func(p *client.Post) { p.Visibility = "friends" },
		"投稿時刻":   func(p *client.Post) { p.PostedAt = p.PostedAt.Add(time.Microsecond) },
		"画像":     func(p *client.Post) { p.ImageURL = "invalid:" },
	} {
		item := memoryItem(w.posts["a"], *m.User("a"))
		tamper(&item)
		v := m.CheckOwnPosts("a", []client.Post{item}, r)
		if len(v) != 1 || !strings.Contains(v[0], name) {
			t.Errorf("%s: %v", name, v)
		}
		if problem := m.CheckOwnPost(item, r); !strings.Contains(problem, name) {
			t.Errorf("%s: CheckOwnPost=%q", name, problem)
		}
	}

	// 変えている最中のキャプションは、前と後のどちらでもよい。
	sent := w.c.tick()
	update := m.BeginUpdateCaption("p-a", "edited")
	during := m.ReadBetween(sent, w.c.tick())
	for _, caption := range []string{"hi", "edited"} {
		item := memoryItem(w.posts["a"], *m.User("a"))
		item.Caption = caption
		if v := m.CheckOwnPosts("a", []client.Post{item}, during); len(v) != 0 {
			t.Errorf("caption %q during the update: %v", caption, v)
		}
	}
	update.Applied()
	item := memoryItem(w.posts["a"], *m.User("a"))
	if v := m.CheckOwnPosts("a", []client.Post{item}, w.later()); len(v) != 1 {
		t.Errorf("old caption after the update: %v", v)
	}
}

// TestOwnPostsDuplicate は、Memories のページをまたいで同じ投稿が 2 度返るのを違反にすることを
// 確かめる。
func TestOwnPostsDuplicate(t *testing.T) {
	w := newWorld(t)
	item := memoryItem(w.posts["a"], *w.m.User("a"))
	v := w.m.CheckOwnPosts("a", []client.Post{item, item}, w.later())
	if len(v) != 1 || !strings.Contains(v[0], "重複") {
		t.Fatalf("duplicate: %v", v)
	}
}
