package scenario

import (
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// TestPostDetailMustMatchRequestedPost は、投稿の詳細の応答が要求した投稿と違う（ID、投稿
// 期間、画像、位置情報、時間内かどうか、遅延時間）とき、検証の失敗に数えて加点しないことを
// 確かめる。書き換えなければ加点する。
func TestPostDetailMustMatchRequestedPost(t *testing.T) {
	for name, edit := range map[string]func(f *interactionFixture, p *client.Post){
		"correct":       func(*interactionFixture, *client.Post) {},
		"unknown id":    func(_ *interactionFixture, p *client.Post) { p.ID = "does-not-exist" },
		"other post id": func(f *interactionFixture, p *client.Post) { p.ID = f.guestPost.ID },
		"window":        func(_ *interactionFixture, p *client.Post) { p.PostingWindowID = "unrelated-window" },
		"image":         func(_ *interactionFixture, p *client.Post) { p.ImageURL = "/api/images/does-not-exist" },
		"location":      func(_ *interactionFixture, p *client.Post) { p.Location.Kind = "forged" },
		"on time":       func(_ *interactionFixture, p *client.Post) { p.OnTime = !p.OnTime },
		"late seconds":  func(_ *interactionFixture, p *client.Post) { p.LateSeconds += 999 },
	} {
		t.Run(name, func(t *testing.T) {
			f := newInteractionFixture(t)
			rec := f.route(t, f.guest, rewrite(t, f.app, is(http.MethodGet, "/api/posts/"+f.post.ID), func(p *client.Post) { edit(f, p) }))
			f.r.openPost(t.Context(), f.guest, &session{day: 1}, f.post.FeedPost())
			stats, details := rec.Total(0), f.r.board.Count(score.TagPostDetail)
			if name == "correct" {
				if stats.Failures != 0 || details != 1 || f.r.board.ErrorCount() != 0 {
					t.Fatalf("stats=%+v details=%d errors=%v", stats, details, f.r.board.Errors())
				}
				return
			}
			if stats.Failures != 1 || details != 0 || f.r.board.ErrorCount() != 1 || f.r.board.CriticalCount() != 0 {
				t.Fatalf("stats=%+v details=%d errors=%v critical=%v", stats, details, f.r.board.Errors(), f.r.board.Critical())
			}
		})
	}
}

// TestMyCurrentPostMustMatchPost は、自分の今日の投稿が ID だけの応答や、内容の違う応答なら
// 詳細の確認として加点せず、失敗に数えることを確かめる。
func TestMyCurrentPostMustMatchPost(t *testing.T) {
	for name, edit := range map[string]func(f *interactionFixture, p *map[string]any){
		"correct": func(*interactionFixture, *map[string]any) {},
		"id only": func(f *interactionFixture, p *map[string]any) { *p = map[string]any{"id": f.post.ID} },
		"caption": func(_ *interactionFixture, p *map[string]any) { (*p)["caption"] = "forged" },
		"author":  func(f *interactionFixture, p *map[string]any) { (*p)["author"] = map[string]any{"id": f.guest.ID} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newInteractionFixture(t)
			rec := f.route(t, f.owner, rewrite(t, f.app, is(http.MethodGet, "/api/posts/me/current"), func(p *map[string]any) { edit(f, p) }))
			f.r.checkMyCurrentPost(t.Context(), f.owner, f.post)
			stats, details := rec.Total(0), f.r.board.Count(score.TagPostDetail)
			if name == "correct" {
				if stats.Failures != 0 || details != 1 || f.r.board.ErrorCount() != 0 {
					t.Fatalf("stats=%+v details=%d errors=%v", stats, details, f.r.board.Errors())
				}
				return
			}
			if stats.Failures != 1 || details != 0 || f.r.board.ErrorCount() != 1 {
				t.Fatalf("stats=%+v details=%d errors=%v", stats, details, f.r.board.Errors())
			}
		})
	}
}
