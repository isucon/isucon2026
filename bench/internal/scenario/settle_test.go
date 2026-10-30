package scenario

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

// TestAnsweredFailure は、アプリが処理を終えてから返した 5xx だけを、あとから反映されない
// 結果不明（Failed）とすることを確かめる。中継の 502、503、504、503 で送り直した要求、
// タイムアウトは、アプリがあとで処理することがあるので含めない。
func TestAnsweredFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		replies []int
		hang    bool
		want    bool
	}{
		"500":               {[]int{http.StatusInternalServerError}, false, true},
		"502":               {[]int{http.StatusBadGateway}, false, false},
		"504":               {[]int{http.StatusGatewayTimeout}, false, false},
		"503 then 500":      {[]int{http.StatusServiceUnavailable, http.StatusInternalServerError}, false, false},
		"503 until give up": {[]int{http.StatusServiceUnavailable, http.StatusServiceUnavailable}, false, false},
		"409":               {[]int{http.StatusConflict}, false, false},
		"timeout":           {nil, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.hang {
					<-r.Context().Done()
					return
				}
				i := int(calls.Add(1)) - 1
				w.WriteHeader(tc.replies[min(i, len(tc.replies)-1)])
			}))
			defer server.Close()
			api := client.NewClient(client.Config{Base: server.URL, Retry: client.RetryPolicy{MaxRetries: 1, Base: time.Millisecond, MaxWait: time.Millisecond}})
			ctx := t.Context()
			if tc.hang {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			err := api.DeletePost(ctx, "token", "p")
			if err == nil {
				t.Fatal("no error")
			}
			if got := answeredFailure(err); got != tc.want {
				t.Fatalf("answeredFailure(%v) = %t, want %t", err, got, tc.want)
			}
		})
	}
}
