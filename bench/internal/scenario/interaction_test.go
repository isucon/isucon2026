package scenario

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// interactionFixture は asia の友達 2 人（owner と guest）が、開いた投稿期間にそれぞれ
// 投稿した状態。交流と閲覧の検証のテストに使う。
type interactionFixture struct {
	r            *Runner
	app          *fakeApp
	owner, guest *user
	post         client.Post // owner の投稿
	guestPost    client.Post
}

func newInteractionFixture(t *testing.T) *interactionFixture {
	t.Helper()
	r, app := newTestRunner(t, 2, metrics.DefaultSLO)
	r.setDuration(1000 * time.Second)
	r.books.Store(newBook(r.tt, time.Now()))
	window, err := r.openDay(t.Context(), geo.Asia, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &interactionFixture{r: r, app: app, owner: r.users[geo.Asia][0], guest: r.users[geo.Asia][1]}
	var ok bool
	if f.post, _, ok = r.post(t.Context(), f.owner, window); !ok {
		t.Fatal(r.board.Errors())
	}
	if f.guestPost, _, ok = r.post(t.Context(), f.guest, window); !ok {
		t.Fatal(r.board.Errors())
	}
	return f
}

// route は u の要求を handler に送り、失敗を集計する。handler が nil なら偽のアプリに送る。
// 集計した操作と失敗は返した Recorder で読む。
func (f *interactionFixture) route(t *testing.T, u *user, handler http.HandlerFunc) *metrics.Recorder {
	t.Helper()
	if handler == nil {
		handler = f.app.ServeHTTP
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rec := metrics.New(time.Now(), time.Minute)
	f.r.recorder.Store(rec)
	u.API = client.New(server.URL).WithCountry(geo.Country(u.Region, 0)).WithClientIP(u.API.ClientIP()).WithObserver(&accounting{r: f.r, slo: true, growth: true})
	return rec
}

// writeJSON は body を JSON にして status で返す。
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Error(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// rewrite は偽のアプリへ送り、match に当たる要求の成功した応答だけ、本体を T として読んで
// edit で書き換えてから返すハンドラー。
func rewrite[T any](t *testing.T, app *fakeApp, match func(*http.Request) bool, edit func(*T)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !match(req) {
			app.ServeHTTP(w, req)
			return
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code >= 300 {
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		var body T
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Error(err)
		}
		edit(&body)
		writeJSON(t, w, rec.Code, body)
	}
}

// is は method と path が一致する要求か。
func is(method, path string) func(*http.Request) bool {
	return func(req *http.Request) bool { return req.Method == method && req.URL.Path == path }
}

// TestImageURLWithSchemeIsInvalid は、パスと署名が正しくてもスキームの付いた画像 URL を、
// 署名の検証で拒否し、取得できないことを応答の誤りとして失敗に数えることを確かめる。
func TestImageURLWithSchemeIsInvalid(t *testing.T) {
	f := newInteractionFixture(t)
	item := f.guestPost.FeedPost()
	item.ImageURL = "invalid:" + item.ImageURL
	rec := f.route(t, f.owner, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/feeds/friends" {
			writeJSON(t, w, http.StatusOK, client.FeedPage{Items: []client.FeedPost{item}})
			return
		}
		f.app.ServeHTTP(w, req)
	})
	if _, ok := f.r.readFeed(t.Context(), f.owner, "friends", nil, 1); ok {
		t.Fatalf("feed with a schemed image URL accepted: %v", f.r.board.Errors())
	}
	if f.r.checkImage(t.Context(), f.owner, item) {
		t.Fatal("image with a scheme accepted")
	}
	if got := rec.Total(0); got.Failures != 2 || f.r.board.ErrorCount() != 2 {
		t.Fatalf("stats=%+v errors=%v", got, f.r.board.Errors())
	}
}

// TestBanDuringInteractionsIsReputation は、走行中に BAN されたユーザーが画像、RealMoji、
// コメント、友達申請の操作で初めて 403 ip_banned を受けたとき、閲覧権限の拒否としてエラーに
// せず、ユーザーを止めて悪評に記録することを確かめる。
func TestBanDuringInteractionsIsReputation(t *testing.T) {
	for name, run := range map[string]func(t *testing.T, f *interactionFixture, s *session){
		"checkImage": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.checkImage(t.Context(), f.guest, f.post.FeedPost())
		},
		"react": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.react(t.Context(), f.guest, s, f.post.FeedPost())
		},
		"comment": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.comment(t.Context(), f.guest, s, f.post.FeedPost())
		},
		"listRealMojis": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.listRealMojis(t.Context(), f.guest, s, f.post.ID)
		},
		"listComments": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.listComments(t.Context(), f.guest, s, f.post.ID)
		},
		"deleteRealMoji": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.deleteRealMoji(t.Context(), f.guest, s)
		},
		"deleteComment": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.deleteComment(t.Context(), f.guest, s)
		},
		"answerRequest": func(t *testing.T, f *interactionFixture, s *session) {
			f.r.answerRequest(t.Context(), f.guest, f.pendingRequest(t, f.r.users[geo.Europe][0], f.guest), "accept")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newInteractionFixture(t)
			s := &session{day: 1}
			if !f.r.react(t.Context(), f.guest, s, f.post.FeedPost()) || !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) {
				t.Fatal(f.r.board.Errors())
			}
			rec := f.route(t, f.guest, nil)
			f.app.mu.Lock()
			f.app.banned[f.guest.API.ClientIP()] = true
			f.app.mu.Unlock()
			run(t, f, s)
			if got := rec.Total(0); got.Failures != 0 || !f.guest.stopped.Load() || !f.guest.banned.Load() || f.r.reputations() != 1 || f.r.board.ErrorCount() != 0 {
				t.Fatalf("stats=%+v stopped=%v banned=%v reputation=%d errors=%v", got, f.guest.stopped.Load(), f.guest.banned.Load(), f.r.reputations(), f.r.board.Errors())
			}
		})
	}
}

// pendingRequest は from から to への友達申請を送り、to に届いた申請として返す。
func (f *interactionFixture) pendingRequest(t *testing.T, from, to *user) client.Relationship {
	t.Helper()
	request, err := from.API.CreateFriendRequest(t.Context(), from.Token, to.ID)
	if err != nil || request.Status != "pending" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	return client.Relationship{RelationID: request.RelationID, User: client.UserSummary{ID: from.ID, Username: from.Username}, Status: "pending"}
}

// TestBlockedProfileIsInvalid は、確実にブロックの関係にある相手のプロフィールが 200 で
// 返ったら失敗に数えて加点せず、404 なら正しい応答とすることを確かめる。ブロックした側と
// された側のどちらから見ても同じ。
func TestBlockedProfileIsInvalid(t *testing.T) {
	for _, leak := range []bool{false, true} {
		for _, blocker := range []bool{false, true} {
			t.Run(fmt.Sprintf("leak=%v/blocker=%v", leak, blocker), func(t *testing.T) {
				f := newInteractionFixture(t)
				w := f.r.model.BeginBlock(f.owner.ID, f.guest.ID)
				if err := f.owner.API.Block(t.Context(), f.owner.Token, f.guest.ID); err != nil {
					t.Fatal(err)
				}
				w.Applied()
				f.app.mu.Lock()
				f.app.ignoreBlocks = leak
				f.app.mu.Unlock()
				viewer, target := f.guest, f.owner
				if blocker {
					viewer, target = f.owner, f.guest
				}
				rec := f.route(t, viewer, nil)
				f.r.viewProfile(t.Context(), viewer, &session{day: 1}, target.ID)
				stats, profiles := rec.Total(0), f.r.board.Count(score.TagProfileView)
				if leak && (stats.Failures != 1 || f.r.board.ErrorCount() != 1 || profiles != 0) {
					t.Fatalf("leaked profile: stats=%+v profiles=%d errors=%v", stats, profiles, f.r.board.Errors())
				}
				if !leak && (stats.Failures != 0 || f.r.board.ErrorCount() != 0 || profiles != 0) {
					t.Fatalf("hidden profile: stats=%+v profiles=%d errors=%v", stats, profiles, f.r.board.Errors())
				}
			})
		}
	}
}

// TestRejectedOwnInteractionDeletion は、確実に残っている自分のコメントや RealMoji の削除を 403
// か 404 で拒否されたら失敗に数え、投稿が削除されたあとの拒否は正しい拒否とすることを確かめる。
func TestRejectedOwnInteractionDeletion(t *testing.T) {
	for _, kind := range []string{"comment", "realmoji"} {
		for _, status := range []int{http.StatusNoContent, http.StatusForbidden, http.StatusNotFound} {
			for _, postDeleted := range []bool{false, true} {
				if status == http.StatusNoContent && postDeleted {
					continue
				}
				t.Run(fmt.Sprintf("%s/%d/postDeleted=%v", kind, status, postDeleted), func(t *testing.T) {
					f := newInteractionFixture(t)
					s := &session{day: 1}
					if kind == "comment" && !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) || kind == "realmoji" && !f.r.react(t.Context(), f.guest, s, f.post.FeedPost()) {
						t.Fatal(f.r.board.Errors())
					}
					if postDeleted {
						w := f.r.model.BeginDeletePost(f.post.ID)
						if err := f.owner.API.DeletePost(t.Context(), f.owner.Token, f.post.ID); err != nil {
							t.Fatal(err)
						}
						w.Applied()
					}
					rec := f.route(t, f.guest, func(w http.ResponseWriter, req *http.Request) {
						if req.Method == http.MethodDelete && status != http.StatusNoContent {
							writeJSON(t, w, status, map[string]string{"error": "interaction_forbidden"})
							return
						}
						f.app.ServeHTTP(w, req)
					})
					if kind == "comment" {
						f.r.deleteComment(t.Context(), f.guest, s)
					} else {
						f.r.deleteRealMoji(t.Context(), f.guest, s)
					}
					want := int64(0)
					if status != http.StatusNoContent && !postDeleted {
						want = 1
					}
					if got := rec.Total(0); got.Ops != 1 || got.Failures != want || f.r.board.ErrorCount() != want {
						t.Fatalf("stats=%+v errors=%v", got, f.r.board.Errors())
					}
				})
			}
		}
	}
	// 503 のあとに送り直した削除の 404 は、前の試行で消えたかもしれないので正しい拒否とする。
	f := newInteractionFixture(t)
	s := &session{day: 1}
	if !f.r.comment(t.Context(), f.guest, s, f.post.FeedPost()) {
		t.Fatal(f.r.board.Errors())
	}
	var attempts atomic.Int64
	f.route(t, f.guest, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete {
			if attempts.Add(1) == 1 {
				f.app.ServeHTTP(httptest.NewRecorder(), req)
				writeJSON(t, w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
				return
			}
		}
		f.app.ServeHTTP(w, req)
	})
	f.r.deleteComment(t.Context(), f.guest, s)
	// 消えたかもしれないコメントは、一覧になくてもよい。
	f.r.listComments(t.Context(), f.guest, s, f.post.ID)
	if attempts.Load() != 2 || f.r.board.ErrorCount() != 0 {
		t.Fatalf("attempts=%d errors=%v", attempts.Load(), f.r.board.Errors())
	}
}
