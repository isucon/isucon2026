package scenario

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
	"github.com/isucon/isucon2026/bench/internal/metrics"
)

// 登録に失敗した新しいユーザーは r.users に入らないので、自分の接続をその場で閉じる。
func TestFailedNewcomerClosesConnections(t *testing.T) {
	r, _ := newTestRunner(t, 1, metrics.DefaultSLO)
	var mu sync.Mutex
	states := map[net.Conn]http.ConnState{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	server.Config.ConnState = func(c net.Conn, state http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		if state == http.StateClosed {
			delete(states, c)
		} else {
			states[c] = state
		}
	}
	server.Start()
	defer server.Close()
	open := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(states)
	}
	r.api = client.New(server.URL)
	r.arriving["asia"] = 16
	for range 16 {
		r.runNewcomer(t.Context(), "asia")
	}
	deadline := time.Now().Add(time.Second)
	for open() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := open(); n != 0 {
		t.Fatalf("%d connections of failed newcomers are left open", n)
	}
}
