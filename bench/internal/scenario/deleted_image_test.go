package scenario

import (
	"fmt"
	"strings"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// TestLateDeletedImagesAreRechecked は、負荷走行の終わりの deletedImageGrace より前に削除した
// 投稿の画像を、走行後の整合性チェックでもう一度取得し、返し続けるアプリを失格にすることを
// 確かめる（1 秒の走行では、すべての削除がもう一度取得する前に走行の終わりを迎える）。
// 正しいアプリは通す。
func TestLateDeletedImagesAreRechecked(t *testing.T) {
	for _, keepImages := range []bool{false, true} {
		t.Run(fmt.Sprintf("keepImages=%t", keepImages), func(t *testing.T) {
			t.Parallel()
			r, app := newTestRunner(t, 2, metrics.SLO{FailRate: 1000, MinOps: 1})
			r.cfg.Growth.FastInvites, r.cfg.Growth.QuickInvites = 0, 0
			r.deleteRate = 1 // 全員が投稿を削除する
			app.mu.Lock()
			app.keepImages = keepImages
			app.mu.Unlock()
			if reason := r.load(t.Context()); reason != "" || r.board.CriticalCount() != 0 {
				t.Fatalf("reason=%q critical=%v", reason, r.board.Critical())
			}
			r.mu.Lock()
			late := len(r.lateRechecks)
			r.mu.Unlock()
			if late == 0 {
				t.Fatal("no deleted image left to recheck after the run")
			}
			if reason := r.finalCheck(t.Context()); reason != "" {
				t.Fatalf("final check: %s", reason)
			}
			critical := strings.Join(r.board.Critical(), "\n")
			if keepImages != strings.Contains(critical, "画像が取得できました") {
				t.Fatalf("critical=%v", r.board.Critical())
			}
		})
	}
}
