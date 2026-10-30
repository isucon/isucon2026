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

// TestUnknownOwnPostsAreBounded は、走行後の整合性チェックで bench の知らない投稿を、結果の
// わからない作成の回数と、作成が入りえた日付で数えることを確かめる。作成 1 回でできる投稿は
// 1 件まで、1 人が同じ日付に持てる投稿も 1 件まで。
func TestUnknownOwnPostsAreBounded(t *testing.T) {
	ghost := func(id, window string) client.Post {
		return client.Post{ID: id, Author: client.UserSummary{ID: "b", Username: "b"}, PostingWindowID: window}
	}
	for name, tc := range map[string]struct {
		// creations は b の結果のわからない作成。"day1" は 1 日目、"day2" は 2 日目に送る。
		// "-5xx" が付けば 5xx（応答までに処理済み）、付かなければタイムアウト。
		creations []string
		keepPost  bool // b の 1 日目の投稿 p-b を消さずに残す
		got       []client.Post
		violation string
	}{
		"one creation, one post":            {[]string{"day1"}, false, []client.Post{ghost("g1", "w1")}, ""},
		"one creation, three posts":         {[]string{"day1"}, false, []client.Post{ghost("g1", "w1"), ghost("g2", "w2"), ghost("g3", "w2")}, "作成は 1 回"},
		"two creations, two days":           {[]string{"day1", "day2"}, false, []client.Post{ghost("g1", "w1"), ghost("g2", "w2")}, ""},
		"two creations, same day":           {[]string{"day1", "day2"}, false, []client.Post{ghost("g1", "w2"), ghost("g2", "w2")}, "同じ日付"},
		"same day as a live post":           {[]string{"day1"}, true, []client.Post{ghost("g1", "w1")}, "同じ日付"},
		"timed out, landed on the next day": {[]string{"day1"}, false, []client.Post{ghost("g1", "w2")}, ""},
		"5xx, on the next day":              {[]string{"day1-5xx"}, false, []client.Post{ghost("g1", "w2")}, "入りえた"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			m := w.m
			if !tc.keepPost {
				m.BeginDeletePost("p-b").Applied()
			}
			create := func(kind string) {
				w.c.tick()
				creation := m.BeginCreatePost("b")
				w.c.tick()
				if strings.HasSuffix(kind, "-5xx") {
					creation.Failed()
				} else {
					creation.Unknown()
				}
			}
			for _, kind := range tc.creations {
				if strings.HasPrefix(kind, "day1") {
					create(kind)
				}
			}
			w.nextDay()
			for _, kind := range tc.creations {
				if strings.HasPrefix(kind, "day2") {
					create(kind)
				}
			}
			v := m.CheckOwnPosts("b", tc.got, w.later())
			switch {
			case tc.violation == "" && len(v) != 0:
				t.Fatalf("violations=%v", v)
			case tc.violation != "" && !strings.Contains(strings.Join(v, "\n"), tc.violation):
				t.Fatalf("violations=%v, want %q", v, tc.violation)
			}
		})
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
