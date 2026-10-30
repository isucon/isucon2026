package scenario

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// TestReusedPostIDIsInvalid は、新しい投稿に前の投稿と同じ ID を返して前の投稿を失うアプリを、
// 投稿の検証で失敗にし、前回の投稿の Memories に加点しないことを確かめる。
func TestReusedPostIDIsInvalid(t *testing.T) {
	r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	r.setDuration(1000 * time.Second)
	r.cfg.Growth.FastInvites, r.cfg.Growth.QuickInvites = 0, 0
	r.books.Store(newBook(r.tt, time.Now()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/posts" {
			app.ServeHTTP(w, req)
			return
		}
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusCreated {
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
			return
		}
		var created client.Post
		if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
			t.Error(err)
			return
		}
		// 投稿の ID をユーザーごとに固定し、同じユーザーの前の投稿を捨てる。
		app.mu.Lock()
		newest := app.post(created.ID)
		newest.ID = "single-post-" + created.Author.ID
		kept := []*fakePost{}
		for _, p := range app.posts {
			if p.Author.ID != created.Author.ID || p == newest {
				kept = append(kept, p)
			}
		}
		app.posts = kept
		created = newest.Post
		app.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)
	}))
	defer server.Close()
	u := r.users["asia"][0]
	u.API = client.New(server.URL).WithCountry("JP")
	var previous *client.Post
	var windows []string
	for day := 1; day <= 2; day++ {
		window, err := r.openDay(t.Context(), "asia", day)
		if err != nil {
			t.Fatal(err)
		}
		windows = append(windows, window)
		_, previous = r.enterWindow(t.Context(), u, window, previous, time.Time{}, make(chan notice, 1))
	}
	if !strings.Contains(strings.Join(r.board.Errors(), "\n"), "ほかの投稿と同じ") {
		t.Fatalf("errors=%v", r.board.Errors())
	}
	if got := r.board.Count(score.TagMemory); got != 0 {
		t.Fatalf("memory scores=%d", got)
	}
	// モデルは 1 日目の投稿を覚えたまま。
	if previous == nil || r.model.Post(previous.ID).WindowID != windows[0] {
		t.Fatalf("previous=%+v", previous)
	}
}
