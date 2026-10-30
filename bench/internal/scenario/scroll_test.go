package scenario

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// TestScrollFindsRepeatedPage は、フィードを続けて読むとき、ページ送りの位置を無視して前の
// ページをまた返すアプリをエラーにし、正しいアプリは通すことを確かめる。
func TestScrollFindsRepeatedPage(t *testing.T) {
	for _, broken := range []bool{false, true} {
		r, app := newTestRunner(t, 3, metrics.SLO{FailRate: 1000, MinOps: 1})
		r.setDuration(1000 * time.Second)
		r.visibility = func() string { return "public" }
		r.books.Store(newBook(r.tt, time.Now()))
		app.pageSize, r.model.PageSize = 1, 1
		ctx := t.Context()
		window, err := r.openDay(ctx, "asia", 1)
		if err != nil {
			t.Fatal(err)
		}
		users := r.users["asia"]
		for _, u := range users {
			if _, _, ok := r.post(ctx, u, window); !ok {
				t.Fatal(r.board.Errors())
			}
		}
		viewer := users[0]
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if broken {
				req.URL.RawQuery = ""
			}
			app.ServeHTTP(w, req)
		}))
		defer server.Close()
		viewer.API = client.New(server.URL).WithCountry(viewer.Country())
		var scroll model.Scroll
		page, ok := r.readFeed(ctx, viewer, "public", &scroll, 1)
		if !ok || !page.HasMore {
			t.Fatalf("first page: %+v errors=%v", page, r.board.Errors())
		}
		page, ok = r.readFeed(ctx, viewer, "public", &scroll, 1)
		if ok == broken || len(page.Items) != 1 {
			t.Fatalf("broken=%t: second page ok=%t page=%+v errors=%v", broken, ok, page, r.board.Errors())
		}
		if broken && !strings.Contains(strings.Join(r.board.Errors(), "\n"), "前のページ") {
			t.Fatalf("errors=%v", r.board.Errors())
		}
	}
}
