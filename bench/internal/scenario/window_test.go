package scenario

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// newWindowRunner は asia の 1 日目の投稿期間を開いた Runner を返す。
func newWindowRunner(t *testing.T) (*Runner, *fakeApp, string) {
	t.Helper()
	r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	r.setDuration(1000 * time.Second)
	r.books.Store(newBook(r.tt, time.Now()))
	window, err := r.openDay(t.Context(), geo.Asia, 1)
	if err != nil || window == "" {
		t.Fatalf("window=%q err=%v", window, err)
	}
	return r, app, window
}

// rewriteJSON は偽のアプリの応答の JSON を edit で書き換えて返す。
func rewriteJSON(t *testing.T, app *fakeApp, w http.ResponseWriter, req *http.Request, edit func(map[string]any)) {
	t.Helper()
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Errorf("%s %s: %d %s", req.Method, req.URL.Path, recorder.Code, recorder.Body)
	}
	edit(body)
	w.WriteHeader(recorder.Code)
	_ = json.NewEncoder(w).Encode(body)
}

func isCurrentWindow(req *http.Request) bool {
	return req.Method == http.MethodGet && req.URL.Path == "/api/posting-window/current"
}

func isCreatePost(req *http.Request) bool {
	return req.Method == http.MethodPost && req.URL.Path == "/api/posts"
}

// TestPostResponseWindow は、投稿の応答の投稿期間が、送っている間に投稿者のリージョンで開いて
// いた投稿期間でなければエラーにする（ほかのリージョンの期間 #209、閉じた前の日の期間 #276）
// ことを確かめる。
func TestPostResponseWindow(t *testing.T) {
	for _, tc := range []struct {
		name string
		// prepare は投稿の前に呼び、投稿する期間と、応答に書く期間を返す。
		prepare func(t *testing.T, r *Runner, w1 string) (post, reply string)
		wantErr bool
	}{
		{"own window", func(t *testing.T, r *Runner, w1 string) (string, string) {
			return w1, w1
		}, false},
		{"foreign region", func(t *testing.T, r *Runner, w1 string) (string, string) {
			europe, err := r.openDay(t.Context(), geo.Europe, 1)
			if err != nil {
				t.Fatal(err)
			}
			return w1, europe
		}, true},
		{"closed window", func(t *testing.T, r *Runner, w1 string) (string, string) {
			w2, err := r.openDay(t.Context(), geo.Asia, 2)
			if err != nil {
				t.Fatal(err)
			}
			return w2, w1
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, app, w1 := newWindowRunner(t)
			r.model.StartSkew = 0
			target, reply := tc.prepare(t, r, w1)
			serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
				if !isCreatePost(req) {
					return false
				}
				rewriteJSON(t, app, w, req, func(body map[string]any) { body["postingWindowId"] = reply })
				return true
			})
			_, _, ok := r.post(t.Context(), r.users[geo.Asia][0], target)
			if got := r.board.ErrorCount() != 0; ok == tc.wantErr || got != tc.wantErr {
				t.Fatalf("accepted=%v errors=%v", ok, r.board.Errors())
			}
		})
	}
}

// TestPostDuringWindowSwitch は、投稿を送っている間に次の投稿期間が開いたとき、投稿が前の期間に
// 入っても次の期間に入っても受け付けることを確かめる。
func TestPostDuringWindowSwitch(t *testing.T) {
	for _, before := range []bool{true, false} {
		r, app, w1 := newWindowRunner(t)
		var w2 string
		serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
			if !isCreatePost(req) {
				return false
			}
			open := func() {
				var err error
				if w2, err = r.openDay(req.Context(), geo.Asia, 2); err != nil {
					t.Error(err)
				}
			}
			recorder := httptest.NewRecorder()
			if before {
				open()
				app.ServeHTTP(recorder, req)
			} else {
				app.ServeHTTP(recorder, req)
				open()
			}
			w.WriteHeader(recorder.Code)
			_, _ = io.Copy(w, recorder.Body)
			return true
		})
		post, _, ok := r.post(t.Context(), r.users[geo.Asia][0], w1)
		want := w1
		if before {
			want = w2
		}
		if !ok || post.PostingWindowID != want || r.board.ErrorCount() != 0 {
			t.Fatalf("before=%v: accepted=%v window=%s want %s errors=%v", before, ok, post.PostingWindowID, want, r.board.Errors())
		}
	}
}

// TestConflictAfterWindowSwitch は、今の投稿期間を読んでから投稿を送るまでに次の投稿期間が開いた
// とき、その日付にまだ投稿していないのに 409 ならエラーにし（#263）、その日付にもう投稿して
// いれば正しい拒否とすることを確かめる。
func TestConflictAfterWindowSwitch(t *testing.T) {
	for _, postedElsewhere := range []bool{false, true} {
		r, app, w1 := newWindowRunner(t)
		r.model.StartSkew = 0
		u := r.users[geo.Asia][0]
		if postedElsewhere {
			// リージョンを移った人のように、次の期間の日付にほかのリージョンで投稿している。
			europe, err := r.openDay(t.Context(), geo.Europe, 2)
			if err != nil {
				t.Fatal(err)
			}
			creation := r.model.BeginCreatePost(u.ID)
			r.model.Created(creation, model.Post{ID: "p-elsewhere", AuthorID: u.ID, WindowID: europe, PostedAt: time.Now()}, "friends")
		}
		serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
			if !isCurrentWindow(req) {
				return false
			}
			// 読んだあとで次の期間を開き、投稿は 409 にする。
			recorder := httptest.NewRecorder()
			app.ServeHTTP(recorder, req)
			if _, err := r.openDay(req.Context(), geo.Asia, 2); err != nil {
				t.Error(err)
			}
			app.mu.Lock()
			app.postStatus = http.StatusConflict
			app.mu.Unlock()
			w.WriteHeader(recorder.Code)
			_, _ = io.Copy(w, recorder.Body)
			return true
		})
		if _, _, ok := r.post(t.Context(), u, w1); ok {
			t.Fatal("a rejected post was accepted")
		}
		if got := r.board.ErrorCount() != 0; got == postedElsewhere {
			t.Fatalf("postedElsewhere=%v: errors=%v", postedElsewhere, r.board.Errors())
		}
	}
}

// TestCurrentWindowChecked は、投稿の前に読んだ今の投稿期間が開いた投稿期間と食い違うとき
// （ID がない #206、内容が違う #300、投稿していないのに canPost が偽 #206）にエラーにし、
// 正しい応答では投稿することを確かめる。
func TestCurrentWindowChecked(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(map[string]any)
		wantErr bool
	}{
		{"correct", func(map[string]any) {}, false},
		{"no window", func(body map[string]any) { body["window"] = nil }, true},
		{"unknown window", func(body map[string]any) { body["window"].(map[string]any)["id"] = "unknown" }, true},
		{"wrong region", func(body map[string]any) { body["window"].(map[string]any)["region"] = geo.America }, true},
		{"wrong date", func(body map[string]any) { body["window"].(map[string]any)["localDate"] = "1999-01-01" }, true},
		{"no start", func(body map[string]any) { delete(body["window"].(map[string]any), "startsAt") }, true},
		{"cannot post", func(body map[string]any) { body["canPost"] = false }, true},
		// hasPosted の意味は参加者向けの文書にないので、canPost が真なら投稿する。
		{"has posted", func(body map[string]any) { body["hasPosted"] = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, app, w1 := newWindowRunner(t)
			r.model.StartSkew = 0
			serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
				if !isCurrentWindow(req) {
					return false
				}
				rewriteJSON(t, app, w, req, tc.edit)
				return true
			})
			_, _, ok := r.post(t.Context(), r.users[geo.Asia][0], w1)
			if got := r.board.ErrorCount() != 0; ok == tc.wantErr || got != tc.wantErr {
				t.Fatalf("posted=%v errors=%v", ok, r.board.Errors())
			}
		})
	}
}

// TestCannotPostRightAfterOpening は、投稿期間を開いた直後（アプリのサーバーと DB の時計の
// ずれの分）の「投稿できない」と 409 を許し、投稿したあとの「投稿できない」も許すことを確かめる。
func TestCannotPostRightAfterOpening(t *testing.T) {
	t.Run("right after opening", func(t *testing.T) {
		r, app, w1 := newWindowRunner(t)
		serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
			if !isCurrentWindow(req) {
				return false
			}
			rewriteJSON(t, app, w, req, func(body map[string]any) { body["canPost"] = false })
			return true
		})
		if _, _, ok := r.post(t.Context(), r.users[geo.Asia][0], w1); ok || r.board.ErrorCount() != 0 {
			t.Fatalf("posted=%v errors=%v", ok, r.board.Errors())
		}
		app.mu.Lock()
		app.postStatus = http.StatusConflict
		app.mu.Unlock()
		serveThrough(t, r, app, func(http.ResponseWriter, *http.Request) bool { return false })
		if _, _, ok := r.post(t.Context(), r.users[geo.Asia][0], w1); ok || r.board.ErrorCount() != 0 {
			t.Fatalf("posted=%v errors=%v", ok, r.board.Errors())
		}
	})
	t.Run("after posting", func(t *testing.T) {
		r, _, w1 := newWindowRunner(t)
		r.model.StartSkew = 0
		u := r.users[geo.Asia][0]
		if _, _, ok := r.post(t.Context(), u, w1); !ok {
			t.Fatalf("first post failed: %v", r.board.Errors())
		}
		if _, _, ok := r.post(t.Context(), u, w1); ok || r.board.ErrorCount() != 0 {
			t.Fatalf("posted again=%v errors=%v", ok, r.board.Errors())
		}
	})
}

// TestSuppressedPostingIsAnError は、すべての今の投稿期間の読み取りで canPost を false にする
// アプリが、投稿せずにエラーなしで走り切れないことを確かめる（#206）。
func TestSuppressedPostingIsAnError(t *testing.T) {
	r, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
	var modified atomic.Int64
	serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
		if !isCurrentWindow(req) {
			return false
		}
		modified.Add(1)
		rewriteJSON(t, app, w, req, func(body map[string]any) { body["canPost"] = false })
		return true
	})
	r.model.StartSkew = 0
	r.load(t.Context())
	errors := strings.Join(r.board.Errors(), "\n")
	if modified.Load() == 0 || app.postCount() != 0 || !strings.Contains(errors, "投稿できないと返りました") {
		t.Fatalf("modified=%d posts=%d errors=%s", modified.Load(), app.postCount(), errors)
	}
}
