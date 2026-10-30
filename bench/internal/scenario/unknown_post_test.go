package scenario

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
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
