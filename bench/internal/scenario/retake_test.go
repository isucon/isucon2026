package scenario

import (
	"bytes"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/geo"
)

// TestRetakeUsesNewPhotoAndChecksIt は、投稿を削除して撮り直すとき、初めの投稿と違う画像を
// 送り（#259）、撮り直した投稿の画像も取得して確かめる（#271）ことを確かめる。
func TestRetakeUsesNewPhotoAndChecksIt(t *testing.T) {
	for _, broken := range []bool{false, true} {
		r, app, w1 := newWindowRunner(t)
		r.deleteRate = 1
		u := r.users[geo.Asia][0]
		u.persona.retake = 1
		var posts atomic.Int32
		serveThrough(t, r, app, func(w http.ResponseWriter, req *http.Request) bool {
			if !isCreatePost(req) || posts.Add(1) != 2 || !broken {
				return false
			}
			// 撮り直した投稿の画像だけを壊す。
			app.ServeHTTP(w, req)
			app.mu.Lock()
			last := app.posts[len(app.posts)-1]
			app.images["img-"+last.ID] = []byte("broken image")
			app.mu.Unlock()
			return true
		})
		s, _ := r.enterWindow(t.Context(), u, w1, nil, time.Time{}, make(chan notice, 1))
		app.mu.Lock()
		var images [][]byte
		for _, p := range app.posts {
			if p.Author.ID == u.ID {
				images = append(images, app.images["img-"+p.ID])
			}
		}
		app.mu.Unlock()
		if len(images) != 2 || (!broken && bytes.Equal(images[0], images[1])) {
			t.Fatalf("broken=%v: posts=%d, same image=%v", broken, len(images), len(images) == 2 && bytes.Equal(images[0], images[1]))
		}
		if got := r.board.ErrorCount() != 0; got != broken || s == nil {
			t.Fatalf("broken=%v: session=%v errors=%v", broken, s != nil, r.board.Errors())
		}
	}
}
