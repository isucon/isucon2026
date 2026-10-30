package scenario

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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
