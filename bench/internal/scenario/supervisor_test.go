package scenario

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCancelledRunCannotPass は、中断された走行が pass にならず、途中経過は報告されることを
// 確かめる。
func TestCancelledRunCannotPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var updates int
	r, err := NewRunner(Config{Target: "http://127.0.0.1:1", Users: 1, Dataset: tinyDataset, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnProgress: func(Result) { updates++ }})
	if err != nil {
		t.Fatal(err)
	}
	result := r.Run(ctx)
	if result.Pass || result.Score != 0 || updates == 0 {
		t.Fatalf("result=%+v updates=%d", result, updates)
	}
}

// TestBrokenAppCannotPass は、アプリが 500 しか返さなければ fail になることを確かめる。
func TestBrokenAppCannotPass(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "broken", 500) }))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.Start()
	defer server.Close()
	r, err := NewRunner(Config{Target: server.URL, Users: 1, Dataset: tinyDataset, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if result := r.Run(context.Background()); result.Pass || result.Reason == "" {
		t.Fatalf("result=%+v", result)
	}
	// 負荷ユーザーを作る前に失敗しても、初期化に使った管理者の接続を残さない。
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("initialize connection was not closed after the run")
	}
}
