package scenario

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/score"
)

func TestCancelledRunCannotPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var updates int
	r, err := NewRunner(Config{Target: "http://127.0.0.1:1", Users: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnProgress: func(Result) { updates++ }})
	if err != nil {
		t.Fatal(err)
	}
	result := r.Run(ctx)
	if result.Pass || result.Score != 0 || updates == 0 {
		t.Fatalf("result=%+v updates=%d", result, updates)
	}
}
func TestLoadSetupFailureCannotPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "broken", 500) }))
	defer server.Close()
	r, _ := NewRunner(Config{Target: server.URL, Users: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	r.load(context.Background())
	if r.abortReason == "" {
		t.Fatal("load setup failure has no failure verdict")
	}
}
func TestScoreMapping(t *testing.T) {
	r, _ := NewRunner(Config{})
	r.board.Add(score.TagPost)
	r.board.Errorf("first")
	r.board.Errorf("second")
	result := r.snapshot()
	if result.RawScore != 10 || result.Deduction != 20 || result.Score != 0 {
		t.Fatalf("%+v", result)
	}
}
