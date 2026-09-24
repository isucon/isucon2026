package scenario

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/client"
)

func TestCycleNotifications(t *testing.T) {
	for _, tc := range []struct {
		name         string
		openStatus   int
		notification string
		wantFatal    bool
		wantErrors   int64
	}{
		{name: "missing notification skips posting", openStatus: 201},
		{name: "wrong window is an error", openStatus: 201, notification: `{"id":"n1","postingWindowId":"wrong","region":"asia"}`, wantErrors: 1},
		{name: "open failure is fatal", openStatus: 500, wantFatal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var subscribed atomic.Bool
			var posts atomic.Int64
			opened := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/api/notifications":
					subscribed.Store(true)
					w.Header().Set("Content-Type", "text/event-stream")
					w.(http.Flusher).Flush()
					select {
					case <-opened:
					case <-req.Context().Done():
						return
					}
					if tc.notification != "" {
						fmt.Fprintf(w, "data: %s\n\n", tc.notification)
					}
				case "/api/admin/posting-window":
					if !subscribed.Load() {
						t.Error("window opened before subscribing")
					}
					w.WriteHeader(tc.openStatus)
					fmt.Fprint(w, `{"id":"w1","region":"asia","localDate":"2026-09-30"}`)
					close(opened)
				case "/api/posts":
					posts.Add(1)
					w.WriteHeader(500)
				default:
					t.Errorf("unexpected path %s", req.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			runner, err := NewRunner(Config{Target: server.URL, Users: 1, MaxCycles: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			runner.firstDate = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
			runner.users = []client.User{{ID: "u1", Token: "token"}}
			reason := runner.runCycle(t.Context(), 0)
			if (reason != "") != tc.wantFatal || runner.board.ErrorCount() != tc.wantErrors || posts.Load() != 0 {
				t.Fatalf("reason=%q errors=%d posts=%d", reason, runner.board.ErrorCount(), posts.Load())
			}
		})
	}
}

func TestCanceledRunFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner, err := NewRunner(Config{Target: "http://127.0.0.1:1", Users: 1, MaxCycles: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(ctx)
	if result.Pass || !strings.Contains(result.Reason, "canceled") {
		t.Fatalf("result=%+v", result)
	}
}

func TestConfiguration(t *testing.T) {
	for _, cfg := range []Config{{Users: 0}, {Users: 1, Region: "unknown"}, {Users: 1, MaxCycles: -1}} {
		if _, err := NewRunner(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
}
