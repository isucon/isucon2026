package scenario

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// listBoth は u が post のコメントと RealMoji の一覧を読み、増えたエラーの数を返す。
func (f *interactionFixture) listBoth(t *testing.T, u *user, postID string) int64 {
	t.Helper()
	before := f.r.board.ErrorCount()
	s := &session{day: 1}
	f.r.listComments(t.Context(), u, s, postID)
	f.r.listRealMojis(t.Context(), u, s, postID)
	return f.r.board.ErrorCount() - before
}

// TestBlockedInteractionsMayBeHidden は、閲覧者とブロックの関係にある人のコメントと RealMoji
// を、一覧が返しても（参照実装）隠しても（仕様）よいとし、ブロックを解除したあとに隠せば
// 欠落とすることを確かめる。
func TestBlockedInteractionsMayBeHidden(t *testing.T) {
	f := newInteractionFixture(t)
	s := &session{day: 1}
	if !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.react(t.Context(), f.guest, s, f.post.FeedPost()) {
		t.Fatal(f.r.board.Errors())
	}
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 {
		t.Fatalf("before block: %v", f.r.board.Errors())
	}
	block := f.r.model.BeginBlock(f.owner.ID, f.guest.ID)
	if err := f.owner.API.Block(t.Context(), f.owner.Token, f.guest.ID); err != nil {
		t.Fatal(err)
	}
	block.Applied()
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 {
		t.Fatalf("shown after block: %v", f.r.board.Errors())
	}
	var hidden atomic.Bool
	f.route(t, f.owner, func(w http.ResponseWriter, req *http.Request) {
		if hidden.Load() && req.Method == http.MethodGet && (strings.HasSuffix(req.URL.Path, "/comments") || strings.HasSuffix(req.URL.Path, "/realmojis")) {
			writeJSON(t, w, http.StatusOK, map[string]any{"items": []any{}, "hasMore": false})
			return
		}
		f.app.ServeHTTP(w, req)
	})
	hidden.Store(true)
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 {
		t.Fatalf("hidden after block: %v", f.r.board.Errors())
	}
	hidden.Store(false)
	unblock := f.r.model.BeginUnblock(f.owner.ID, f.guest.ID)
	if err := f.owner.API.Unblock(t.Context(), f.owner.Token, f.guest.ID); err != nil {
		t.Fatal(err)
	}
	unblock.Applied()
	hidden.Store(true)
	if n := f.listBoth(t, f.owner, f.post.ID); n != 2 {
		t.Fatalf("hidden after unblock: %v", f.r.board.Errors())
	}
}

// TestShortInteractionPageWithMoreIsInvalid は、続きがあるとする 50 件に満たないページ（空の
// ページを含む）で、既知の交流の欠落を隠せないことを確かめる。
func TestShortInteractionPageWithMoreIsInvalid(t *testing.T) {
	f := newInteractionFixture(t)
	s := &session{day: 1}
	if !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.react(t.Context(), f.guest, s, f.post.FeedPost()) {
		t.Fatal(f.r.board.Errors())
	}
	var short atomic.Bool
	comments := rewrite(t, f.app, is(http.MethodGet, "/api/posts/"+f.post.ID+"/comments"), func(page *client.CommentPage) {
		page.Items, page.HasMore = page.Items[:1], true
	})
	rec := f.route(t, f.owner, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case short.Load() && strings.HasSuffix(req.URL.Path, "/comments"):
			comments(w, req)
		case short.Load() && strings.HasSuffix(req.URL.Path, "/realmojis"):
			writeJSON(t, w, http.StatusOK, client.RealMojiPage{Items: []client.RealMoji{}, HasMore: true})
		default:
			f.app.ServeHTTP(w, req)
		}
	})
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 || f.r.board.Count(score.TagReactionList) != 2 {
		t.Fatalf("full lists: %v", f.r.board.Errors())
	}
	short.Store(true)
	if n := f.listBoth(t, f.owner, f.post.ID); n != 2 || rec.Total(0).Failures != 2 || f.r.board.Count(score.TagReactionList) != 2 {
		t.Fatalf("short pages with more: stats=%+v lists=%d errors=%v", rec.Total(0), f.r.board.Count(score.TagReactionList), f.r.board.Errors())
	}
}

// TestRealMojiResponseMustMatchRequest は、RealMoji を送った応答が空のオブジェクトや、送った人、
// 種類、画像 URL の違うものなら、送れたとして加点せず失敗に数え、その RealMoji を「ない」とも
// 決めないことを確かめる。
func TestRealMojiResponseMustMatchRequest(t *testing.T) {
	for name, edit := range map[string]func(f *interactionFixture, x *client.RealMoji){
		"correct": func(*interactionFixture, *client.RealMoji) {},
		"empty":   func(_ *interactionFixture, x *client.RealMoji) { *x = client.RealMoji{} },
		"reactor": func(f *interactionFixture, x *client.RealMoji) { x.Reactor.ID = f.owner.ID },
		"type":    func(_ *interactionFixture, x *client.RealMoji) { x.ReactionType = "not-a-reaction" },
		"image":   func(_ *interactionFixture, x *client.RealMoji) { x.ImageURL = "invalid:" + x.ImageURL },
		"time":    func(_ *interactionFixture, x *client.RealMoji) { x.CreatedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newInteractionFixture(t)
			rec := f.route(t, f.guest, rewrite(t, f.app, is(http.MethodPost, "/api/posts/"+f.post.ID+"/realmojis"), func(x *client.RealMoji) { edit(f, x) }))
			sent := f.r.react(t.Context(), f.guest, &session{day: 1}, f.post.FeedPost())
			stats, points := rec.Total(0), f.r.board.Count(score.TagRealMoji)
			if name == "correct" {
				if !sent || stats.Failures != 0 || points != 1 {
					t.Fatalf("sent=%v stats=%+v points=%d errors=%v", sent, stats, points, f.r.board.Errors())
				}
				return
			}
			if sent || stats.Failures != 1 || points != 0 || f.r.board.ErrorCount() != 1 {
				t.Fatalf("sent=%v stats=%+v points=%d errors=%v", sent, stats, points, f.r.board.Errors())
			}
			// 応答の誤った RealMoji は、一覧にあってもなくてもよい。
			before := f.r.board.ErrorCount()
			f.r.listRealMojis(t.Context(), f.owner, &session{day: 1}, f.post.ID)
			if f.r.board.ErrorCount() != before {
				t.Fatalf("list after a broken response: %v", f.r.board.Errors())
			}
		})
	}
}

// TestRealMojiListMustMatchSent は、送った RealMoji の種類、画像、作成時刻を一覧で書き換えると、
// 一覧の閲覧に加点せず失敗に数えることを確かめる。
func TestRealMojiListMustMatchSent(t *testing.T) {
	for name, edit := range map[string]func(f *interactionFixture, x *client.RealMoji){
		"correct": func(*interactionFixture, *client.RealMoji) {},
		"type":    func(_ *interactionFixture, x *client.RealMoji) { x.ReactionType = "not-a-reaction" },
		"image":   func(f *interactionFixture, x *client.RealMoji) { x.ImageURL = f.post.ImageURL },
		"time":    func(_ *interactionFixture, x *client.RealMoji) { x.CreatedAt = x.CreatedAt.Add(24 * time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newInteractionFixture(t)
			if !f.r.react(t.Context(), f.guest, &session{day: 1}, f.post.FeedPost()) {
				t.Fatal(f.r.board.Errors())
			}
			rec := f.route(t, f.owner, rewrite(t, f.app, is(http.MethodGet, "/api/posts/"+f.post.ID+"/realmojis"), func(page *client.RealMojiPage) {
				for i := range page.Items {
					edit(f, &page.Items[i])
				}
			}))
			f.r.listRealMojis(t.Context(), f.owner, &session{day: 1}, f.post.ID)
			stats, lists := rec.Total(0), f.r.board.Count(score.TagReactionList)
			if name == "correct" {
				if stats.Failures != 0 || lists != 1 {
					t.Fatalf("stats=%+v lists=%d errors=%v", stats, lists, f.r.board.Errors())
				}
				return
			}
			if stats.Failures != 1 || lists != 0 || f.r.board.ErrorCount() != 1 {
				t.Fatalf("stats=%+v lists=%d errors=%v", stats, lists, f.r.board.Errors())
			}
		})
	}
}
