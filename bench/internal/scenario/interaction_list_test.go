package scenario

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestBlockedInteractionsAreListed は、閲覧者とブロックの関係にある人のコメントと RealMoji も、
// 参照実装と同じく一覧に含めることを確かめる（隠せば欠落とする。#130 は参照実装が正しい）。
func TestBlockedInteractionsAreListed(t *testing.T) {
	f := newInteractionFixture(t)
	s := &session{day: 1}
	if !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.react(t.Context(), f.guest, s, f.post.FeedPost()) {
		t.Fatal(f.r.board.Errors())
	}
	block := f.r.model.BeginBlock(f.owner.ID, f.guest.ID)
	if err := f.owner.API.Block(t.Context(), f.owner.Token, f.guest.ID); err != nil {
		t.Fatal(err)
	}
	block.Applied()
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 {
		t.Fatalf("shown after block: %v", f.r.board.Errors())
	}
	f.route(t, f.owner, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && (strings.HasSuffix(req.URL.Path, "/comments") || strings.HasSuffix(req.URL.Path, "/realmojis")) {
			writeJSON(t, w, http.StatusOK, map[string]any{"items": []any{}, "hasMore": false})
			return
		}
		f.app.ServeHTTP(w, req)
	})
	if n := f.listBoth(t, f.owner, f.post.ID); n != 2 {
		t.Fatalf("hidden after block: %v", f.r.board.Errors())
	}
}

// TestShortInteractionPageWithMoreIsInvalid は、コメントと RealMoji の一覧で、続きがあるのに空の
// ページや、最後の項目より前にあるはずの交流を欠いたページはエラーにし、加点しないことを
// 確かめる。仕様は 1 ページ「最大」50 件なので、続きがあって 50 件に満たないだけのページは
// エラーにしない（#133）。
func TestShortInteractionPageWithMoreIsInvalid(t *testing.T) {
	f := newInteractionFixture(t)
	s := &session{day: 1}
	if !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.react(t.Context(), f.guest, s, f.post.FeedPost()) {
		t.Fatal(f.r.board.Errors())
	}
	// mode 1: 続きがあるとして最初のコメントだけを返す（正しい）。
	// mode 2: 続きがあるとして 2 つ目のコメントだけを返し（1 つ目を隠す）、RealMoji は空で続きがあるとする。
	var mode atomic.Int32
	comments := rewrite(t, f.app, is(http.MethodGet, "/api/posts/"+f.post.ID+"/comments"), func(page *client.CommentPage) {
		if mode.Load() == 1 {
			page.Items, page.HasMore = page.Items[:1], true
		} else {
			page.Items, page.HasMore = page.Items[1:2], true
		}
	})
	rec := f.route(t, f.owner, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case mode.Load() != 0 && strings.HasSuffix(req.URL.Path, "/comments"):
			comments(w, req)
		case mode.Load() == 2 && strings.HasSuffix(req.URL.Path, "/realmojis"):
			writeJSON(t, w, http.StatusOK, client.RealMojiPage{Items: []client.RealMoji{}, HasMore: true})
		default:
			f.app.ServeHTTP(w, req)
		}
	})
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 || f.r.board.Count(score.TagReactionList) != 2 {
		t.Fatalf("full lists: %v", f.r.board.Errors())
	}
	mode.Store(1)
	if n := f.listBoth(t, f.owner, f.post.ID); n != 0 || rec.Total(0).Failures != 0 || f.r.board.Count(score.TagReactionList) != 4 {
		t.Fatalf("short page with more: stats=%+v lists=%d errors=%v", rec.Total(0), f.r.board.Count(score.TagReactionList), f.r.board.Errors())
	}
	mode.Store(2)
	if n := f.listBoth(t, f.owner, f.post.ID); n != 2 || rec.Total(0).Failures != 2 || f.r.board.Count(score.TagReactionList) != 4 {
		t.Fatalf("pages hiding interactions: stats=%+v lists=%d errors=%v", rec.Total(0), f.r.board.Count(score.TagReactionList), f.r.board.Errors())
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

// TestCommentListMustMatchCreated は、作成したコメントの作成時刻、書いた人を一覧で書き換えると
// 失敗に数えることを確かめる。作成の結果がわからなくても、その作成を送った人でない人の
// コメントにはできない。
func TestCommentListMustMatchCreated(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for name, edit := range map[string]func(f *interactionFixture, c *client.Comment){
			"correct": func(*interactionFixture, *client.Comment) {},
			"time":    func(_ *interactionFixture, c *client.Comment) { c.CreatedAt = c.CreatedAt.Add(24 * time.Hour) },
			"author":  func(f *interactionFixture, c *client.Comment) { c.Author.ID = f.owner.ID },
		} {
			if unknown && name == "time" {
				continue // 結果のわからない作成は作成時刻を知らない
			}
			t.Run(fmt.Sprintf("unknown=%v/%s", unknown, name), func(t *testing.T) {
				f := newInteractionFixture(t)
				f.route(t, f.guest, func(w http.ResponseWriter, req *http.Request) {
					if unknown && is(http.MethodPost, "/api/posts/"+f.post.ID+"/comments")(req) {
						f.app.ServeHTTP(httptest.NewRecorder(), req)
						writeJSON(t, w, http.StatusInternalServerError, map[string]string{"error": "response_lost"})
						return
					}
					rewrite(t, f.app, is(http.MethodGet, "/api/posts/"+f.post.ID+"/comments"), func(page *client.CommentPage) {
						for i := range page.Items {
							edit(f, &page.Items[i])
						}
					})(w, req)
				})
				if sent := f.r.comment(t.Context(), f.guest, &session{day: 1}, f.post.FeedPost()); sent == unknown {
					t.Fatalf("sent=%v errors=%v", sent, f.r.board.Errors())
				}
				before := f.r.board.ErrorCount()
				f.r.listComments(t.Context(), f.guest, &session{day: 1}, f.post.ID)
				added, lists := f.r.board.ErrorCount()-before, f.r.board.Count(score.TagReactionList)
				if name == "correct" && (added != 0 || lists != 1) {
					t.Fatalf("added=%d lists=%d errors=%v", added, lists, f.r.board.Errors())
				}
				if name != "correct" && (added != 1 || lists != 0) {
					t.Fatalf("added=%d lists=%d errors=%v", added, lists, f.r.board.Errors())
				}
			})
		}
	}
}

// TestDuplicateCommentIDs は、2 つのコメントの作成に同じ ID を返されたら 2 件目を送れたとせず、
// 一覧の 1 件で 2 件を見たことにしないことを確かめる。
func TestDuplicateCommentIDs(t *testing.T) {
	f := newInteractionFixture(t)
	var last atomic.Pointer[client.Comment]
	f.route(t, f.guest, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case is(http.MethodPost, "/api/posts/"+f.post.ID+"/comments")(req):
			rewrite(t, f.app, func(*http.Request) bool { return true }, func(c *client.Comment) {
				c.ID = "duplicate-comment"
				last.Store(c)
			})(w, req)
		case is(http.MethodGet, "/api/posts/"+f.post.ID+"/comments")(req):
			writeJSON(t, w, http.StatusOK, client.CommentPage{Items: []client.Comment{*last.Load()}})
		default:
			f.app.ServeHTTP(w, req)
		}
	})
	s := &session{day: 1}
	first, second := f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()), f.r.comment(t.Context(), f.guest, s, f.post.FeedPost())
	f.r.listComments(t.Context(), f.guest, s, f.post.ID)
	if !first || second || f.r.board.Count(score.TagComment) != 1 || f.r.board.Count(score.TagReactionList) != 0 || f.r.board.ErrorCount() != 2 {
		t.Fatalf("first=%v second=%v comments=%d lists=%d errors=%v", first, second, f.r.board.Count(score.TagComment), f.r.board.Count(score.TagReactionList), f.r.board.Errors())
	}
}
