package scenario

import (
	"net/http"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// 友達の輪の残り（circleLeft）は、実際に送れた申請の分だけ減らす。申請する相手がいない日や
// 送れなかった申請の分は、あとの日に送る。
func TestReachOutCountsSentRequests(t *testing.T) {
	r, app := newTestRunner(t, 1, metrics.DefaultSLO)
	u := r.users["asia"][0]
	others := []*user{r.users["europe"][0], r.users["america"][0]}
	for _, c := range others {
		c.stopped.Store(true)
	}
	u.circleLeft = 3
	r.reachOut(t.Context(), u, 3)
	if u.circleLeft != 3 {
		t.Fatalf("no candidates: circleLeft=%d", u.circleLeft)
	}

	// 相手が来ても、申請が失敗すれば残りは減らない。
	others[0].stopped.Store(false)
	routeThrough(t, r, u, app, conflictOn(http.MethodPost, "/api/friend-requests", http.StatusInternalServerError))
	r.reachOut(t.Context(), u, 3)
	if u.circleLeft != 3 {
		t.Fatalf("failed request: circleLeft=%d", u.circleLeft)
	}

	// 結果のわからない申請の相手は候補から外れるので、別の相手に送る。
	others[1].stopped.Store(false)
	routeThrough(t, r, u, app, func(http.ResponseWriter, *http.Request) bool { return false })
	r.reachOut(t.Context(), u, 3)
	if u.circleLeft != 2 {
		t.Fatalf("one request: circleLeft=%d errors=%v", u.circleLeft, r.board.Errors())
	}
}
