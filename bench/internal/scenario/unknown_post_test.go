package scenario

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// lostCreation は POST /api/posts の応答を失わせる中継。mode が "5xx" ならアプリで投稿を
// 作ったうえで 500 を返す。"late" なら応答せずに要求を控え、replay でアプリに渡す（タイム
// アウトした作成を、アプリがあとで処理する）。
type lostCreation struct {
	t    *testing.T
	app  *fakeApp
	mode string

	mu      sync.Mutex
	pending []*http.Request
	body    [][]byte
}

func (l *lostCreation) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/posts" {
		l.app.ServeHTTP(w, r)
		return
	}
	switch l.mode {
	case "5xx":
		recorder := httptest.NewRecorder()
		l.app.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusCreated {
			l.t.Errorf("create status=%d", recorder.Code)
		}
		w.WriteHeader(http.StatusInternalServerError)
	case "late":
		body, _ := io.ReadAll(r.Body)
		l.mu.Lock()
		l.pending = append(l.pending, r.Clone(context.Background()))
		l.body = append(l.body, body)
		l.mu.Unlock()
		<-r.Context().Done()
	}
}

// replay は控えた作成をアプリに処理させる。
func (l *lostCreation) replay() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, r := range l.pending {
		r.Body = io.NopCloser(bytes.NewReader(l.body[i]))
		recorder := httptest.NewRecorder()
		l.app.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusCreated {
			l.t.Errorf("late create status=%d", recorder.Code)
		}
	}
	l.pending, l.body = nil, nil
}

// TestCorrectAppWithLostCreations は、投稿を作ったうえで 500 を返すことがある正しいアプリが、
// 削除、撮り直し、公開範囲の変更、ページ送りを混ぜて何日も走らせても、bench の知らない投稿の
// せいで失格や検証の失敗にならず、走行後の整合性チェックも通ることを確かめる。
func TestCorrectAppWithLostCreations(t *testing.T) {
	r, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
	app.pageSize, r.model.PageSize = 2, 2
	app.loseCreation = 3
	r.setDuration(2 * time.Second)
	r.deleteRate, r.changeRate = 0.2, 0.3
	if reason := r.load(t.Context()); reason != "" || r.board.CriticalCount() != 0 {
		t.Fatalf("reason=%q critical=%v", reason, r.board.Critical())
	}
	lost := 0
	for _, message := range r.board.Errors() {
		if !strings.Contains(message, "投稿できません") {
			t.Fatalf("errors=%v", r.board.Errors())
		}
		lost++
	}
	if lost == 0 {
		t.Fatal("no creation was lost")
	}
	if reason := r.finalCheck(t.Context()); reason != "" {
		t.Fatalf("final check: %s", reason)
	}
}

// TestUnknownPostPrivacyOverHTTP は、投稿作成が 5xx で終わって bench が ID を知らない投稿でも、
// ブロックした相手や今日まだ投稿していない人のフィードに返すアプリを失格にし、正しいアプリは
// 通すことを確かめる。
func TestUnknownPostPrivacyOverHTTP(t *testing.T) {
	for _, kind := range []string{"blocked", "viewer-not-posted"} {
		for _, broken := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/broken=%t", kind, broken), func(t *testing.T) {
				count := 2
				if kind == "blocked" {
					count = 4 // 0 番目の人が 3 番目の人をブロックしている
				}
				r, app := newTestRunner(t, count, metrics.DefaultSLO)
				r.setDuration(1000 * time.Second)
				r.visibility = func() string { return "public" }
				r.books.Store(newBook(r.tt, time.Now()))
				ctx := t.Context()
				window, err := r.openDay(ctx, "asia", 1)
				if err != nil {
					t.Fatal(err)
				}
				viewer, author := r.users["asia"][0], r.users["asia"][count-1]
				if kind == "blocked" {
					if _, _, ok := r.post(ctx, viewer, window); !ok {
						t.Fatal(r.board.Errors())
					}
					if r.model.Blocked(viewer.ID, author.ID, r.model.Now()) != model.True {
						t.Fatal("block not settled")
					}
				}
				server := httptest.NewServer(&lostCreation{t: t, app: app, mode: "5xx"})
				defer server.Close()
				author.API = client.New(server.URL).WithCountry(author.Country())
				if _, _, ok := r.post(ctx, author, window); ok {
					t.Fatal("the creation must have an unknown outcome")
				}
				app.mu.Lock()
				app.ignoreBlocks = broken && kind == "blocked"
				app.ignoreGate = broken && kind == "viewer-not-posted"
				app.mu.Unlock()
				feed := "public"
				if kind == "viewer-not-posted" {
					feed = "friends"
				}
				page, ok := r.readFeed(ctx, viewer, feed, nil, 1)
				if broken && (len(page.Items) != 1 || r.model.Post(page.Items[0].ID) != nil) {
					t.Fatalf("not an unknown returned post: %+v", page)
				}
				if critical := r.board.CriticalCount() > 0; critical != broken || ok == broken {
					t.Fatalf("critical=%v ok=%t, want critical=%t (page=%+v)", r.board.Critical(), ok, broken, page)
				}
			})
		}
	}
}

// TestUnknownCreationAndNextDayGate は、投稿作成の応答が失われた人の翌日のゲートを確かめる。
// 5xx を返した作成は前日の投稿にしかならないので、翌日まだ投稿していない人に他人の投稿を
// 見せるアプリは失格にする。タイムアウトした作成はアプリがあとで処理して翌日の投稿になる
// ことがあるので、その投稿でゲートを通った人に他人の投稿を見せる正しいアプリは通す。
func TestUnknownCreationAndNextDayGate(t *testing.T) {
	for name, tc := range map[string]struct {
		mode       string
		ignoreGate bool
		critical   bool
	}{
		"5xx and a correct app":            {"5xx", false, false},
		"5xx and an app ignoring the gate": {"5xx", true, true},
		"late creation and a correct app":  {"late", false, false},
	} {
		t.Run(name, func(t *testing.T) {
			r, app := newTestRunner(t, 2, metrics.DefaultSLO)
			r.setDuration(1000 * time.Second)
			r.visibility = func() string { return "public" }
			r.books.Store(newBook(r.tt, time.Now()))
			ctx := t.Context()
			window, err := r.openDay(ctx, "asia", 1)
			if err != nil {
				t.Fatal(err)
			}
			viewer, author := r.users["asia"][0], r.users["asia"][1]
			relay := &lostCreation{t: t, app: app, mode: tc.mode}
			server := httptest.NewServer(relay)
			defer server.Close()
			viewer.API = client.New(server.URL).WithCountry(viewer.Country())
			postCtx := ctx
			if tc.mode == "late" {
				var cancel context.CancelFunc
				postCtx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			if _, _, ok := r.post(postCtx, viewer, window); ok {
				t.Fatal("the creation must have an unknown outcome")
			}
			next, err := r.openDay(ctx, "asia", 2)
			if err != nil {
				t.Fatal(err)
			}
			relay.replay()
			if _, _, ok := r.post(ctx, author, next); !ok {
				t.Fatalf("author could not post: %v", r.board.Errors())
			}
			app.mu.Lock()
			app.ignoreGate = tc.ignoreGate
			app.mu.Unlock()
			page, ok := r.readFeed(ctx, viewer, "public", nil, 1)
			if critical := r.board.CriticalCount() > 0; critical != tc.critical {
				t.Fatalf("critical=%v, want %t (page=%+v ok=%t errors=%v)", r.board.Critical(), tc.critical, page, ok, r.board.Errors())
			}
			// 作成の 5xx は失敗に数えるが、フィードの検証は通る。あとで処理された作成は翌日の
			// 投稿になり、正しいアプリでも他人の投稿が見える。
			if !tc.critical && (!ok || (len(page.Items) == 1) != (tc.mode == "late")) {
				t.Fatalf("page=%+v ok=%t errors=%v", page, ok, r.board.Errors())
			}
		})
	}
}
