package scenario

import (
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/model"
)

// 認定する人は、前日の投稿期間で有効な RealMoji を 2 件以上受けた人から選ぶ。コメントは
// 数えず、同じ人が置き換えた RealMoji は 1 件、取り消した RealMoji は数えない。
func TestCertifyTopCountsLiveRealMojis(t *testing.T) {
	for _, tc := range []struct {
		name string
		// reactions は RealMoji を送る人の添字（同じ人が 2 回なら置き換え）、comments は
		// コメントする人の添字、deleted は取り消す人の添字。
		reactions, comments, deleted []int
		certified                    bool
	}{
		{name: "two-reactors", reactions: []int{1, 2}, certified: true},
		{name: "comments-only", comments: []int{1, 2}},
		{name: "replaced", reactions: []int{1, 1}},
		{name: "deleted", reactions: []int{1, 2}, deleted: []int{2}},
		{name: "one-reaction-and-comment", reactions: []int{1}, comments: []int{2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRunner(t, 3, metrics.DefaultSLO)
			users := r.users["asia"]
			author := users[0]
			window := model.Window{ID: "w-1", Region: "asia", LocalDate: r.firstDate}
			r.model.OpenedWindow(r.model.BeginOpenWindow("asia"), window)
			creation := r.model.BeginCreatePost(author.ID)
			r.model.Created(creation, model.Post{ID: "p-1", AuthorID: author.ID, WindowID: window.ID, PostedAt: time.Now()}, "public")
			for i, reactor := range tc.reactions {
				w := r.model.BeginReplaceRealMoji("p-1", users[reactor].ID)
				r.model.ReplacedRealMoji(w, client.RealMoji{ID: "rm-" + itoa(i)})
			}
			for i, commenter := range tc.comments {
				c := r.model.BeginCreateComment("p-1", users[commenter].ID)
				r.model.CreatedComment(c, client.Comment{ID: "c-" + itoa(i), Body: "hi"}, users[commenter].ID)
			}
			for _, reactor := range tc.deleted {
				r.model.BeginDeleteRealMoji("p-1", users[reactor].ID).Applied()
			}
			time.Sleep(10 * time.Millisecond)
			r.certifyTop(t.Context(), "asia", window.ID)
			if got := author.certified.Load(); got != tc.certified || r.board.ErrorCount() != 0 {
				t.Fatalf("certified=%v errors=%v", got, r.board.Errors())
			}
		})
	}
}
