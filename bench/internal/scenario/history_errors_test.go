package scenario

import (
	"errors"
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
	"github.com/isucon/isucon2026/bench/internal/score"
)

// TestHistoryBodyErrorIsNotCritical は、履歴の読み取りで本体が上限を超えるか JSON として読めない
// （client.BodyError）ときは、通信の失敗と同じくログだけにして失格にせず、中身が初期データと
// 違うときだけ失格にすることを確かめる。
func TestHistoryBodyErrorIsNotCritical(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		critical int64
	}{
		{"body", &client.BodyError{Method: http.MethodGet, Path: "/api/memories", Err: errors.New("too large")}, 0},
		{"mismatch", errors.New("caption differs"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
			r.recordHistory(t.Context(), r.users["asia"][0], tc.err, score.TagHistoryMonth)
			if got := r.board.CriticalCount(); got != tc.critical || r.board.ErrorCount()+got != 1 {
				t.Fatalf("critical=%d errors=%v", got, r.board.Errors())
			}
		})
	}
}
