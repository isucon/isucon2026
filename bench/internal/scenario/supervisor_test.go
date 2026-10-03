package scenario

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "broken", 500) }))
	defer server.Close()
	r, err := NewRunner(Config{Target: server.URL, Users: 1, Dataset: tinyDataset, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if result := r.Run(context.Background()); result.Pass || result.Reason == "" {
		t.Fatalf("result=%+v", result)
	}
}
