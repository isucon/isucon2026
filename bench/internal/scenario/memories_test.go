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

// TestTamperedMemoriesAreInvalid は、負荷ユーザーの投稿の内容（キャプション、画像）を変えて
// Memories に返すアプリに、前回の投稿の Memories で加点せず、走行後の整合性チェックも fail に
// することを確かめる。
func TestTamperedMemoriesAreInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		tamper func(*client.MemoryPost)
		want   string
	}{
		"intact":  {nil, ""},
		"caption": {func(p *client.MemoryPost) { p.Caption = "tampered-memory-caption" }, "キャプション"},
		"image":   {func(p *client.MemoryPost) { p.ImageURL = "invalid:" }, "画像"},
	} {
		t.Run(name, func(t *testing.T) {
			r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
			r.setDuration(1000 * time.Second)
			r.books.Store(newBook(r.tt, time.Now()))
			ctx := t.Context()
			window, err := r.openDay(ctx, "asia", 1)
			if err != nil {
				t.Fatal(err)
			}
			u := r.users["asia"][0]
			post, _, ok := r.post(ctx, u, window)
			if !ok {
				t.Fatal(r.board.Errors())
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if tc.tamper == nil || req.URL.Path != "/api/memories" {
					app.ServeHTTP(w, req)
					return
				}
				recorder := httptest.NewRecorder()
				app.ServeHTTP(recorder, req)
				var page client.MemoryPage
				if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
					t.Error(err)
				}
				for i := range page.Items {
					tc.tamper(&page.Items[i])
				}
				w.WriteHeader(recorder.Code)
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			u.API = client.New(server.URL).WithCountry(u.Country())
			r.checkMemory(ctx, u, &post)
			final := r.finalCheck(ctx)
			if tc.want == "" {
				if r.board.Count(score.TagMemory) != 1 || r.board.ErrorCount() != 0 || final != "" {
					t.Fatalf("memory=%d errors=%v final=%q", r.board.Count(score.TagMemory), r.board.Errors(), final)
				}
				return
			}
			if r.board.Count(score.TagMemory) != 0 || !strings.Contains(strings.Join(r.board.Errors(), "\n"), tc.want) {
				t.Fatalf("memory=%d errors=%v", r.board.Count(score.TagMemory), r.board.Errors())
			}
			if !strings.Contains(final, tc.want) {
				t.Fatalf("final=%q", final)
			}
		})
	}
}

// TestFinalCheckFindsRepeatedMemoriesPage は、Memories のページ送りで前のページの投稿を
// また返すアプリを、走行後の整合性チェックで fail にすることを確かめる。
func TestFinalCheckFindsRepeatedMemoriesPage(t *testing.T) {
	r, app := newTestRunner(t, 1, metrics.SLO{FailRate: 1000, MinOps: 1})
	r.setDuration(1000 * time.Second)
	r.books.Store(newBook(r.tt, time.Now()))
	ctx := t.Context()
	window, err := r.openDay(ctx, "asia", 1)
	if err != nil {
		t.Fatal(err)
	}
	u := r.users["asia"][0]
	if _, _, ok := r.post(ctx, u, window); !ok {
		t.Fatal(r.board.Errors())
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/memories" {
			app.ServeHTTP(w, req)
			return
		}
		// 1 ページ目は続きがあると答え、2 ページ目にも同じ投稿を返す。
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, req)
		var page client.MemoryPage
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Error(err)
		}
		page.HasMore = req.URL.Query().Get("beforeId") == "" && len(page.Items) > 0
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	u.API = client.New(server.URL).WithCountry(u.Country())
	if final := r.finalCheck(ctx); !strings.Contains(final, "重複") {
		t.Fatalf("final=%q", final)
	}
}
