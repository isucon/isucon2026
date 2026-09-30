package score_test

import (
	"sync"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/score"
)

func TestBoardTotal(t *testing.T) {
	board := score.NewBoard(nil)
	board.Add(score.TagGatePass) // +100
	board.Add(score.TagRealMoji) // +5
	board.Add(score.TagComment)  // +3
	board.Add(score.TagFeed)     // +1

	if got := board.Total(); got != 109 {
		t.Fatalf("Total() = %d, want 109", got)
	}
}

func TestBoardCustomWeights(t *testing.T) {
	weights := score.Weights{score.TagGatePass: 7}
	board := score.NewBoard(weights)
	weights[score.TagGatePass] = 1000 // ボードは重みのコピーを持つ
	board.Add(score.TagGatePass)
	board.Add(score.TagFeed) // 重みなし
	if got := board.Total(); got != 7 {
		t.Fatalf("Total() = %d, want 7", got)
	}
}

func TestErrorsDoNotDeduct(t *testing.T) {
	board := score.NewBoard(nil)
	board.Add(score.TagGatePass)
	board.Errorf("GET /x: expected 200, got 500 (internal_error)")
	board.Criticalf("feed returned a hidden post")

	if got := board.Total(); got != 100 {
		t.Fatalf("Total() = %d, want 100", got)
	}
	if board.ErrorCount() != 1 || board.CriticalCount() != 1 || len(board.Critical()) != 1 {
		t.Fatalf("errors=%d critical=%d", board.ErrorCount(), board.CriticalCount())
	}
}

func TestBoardMessagesCapped(t *testing.T) {
	board := score.NewBoard(nil)
	const overflow = 100
	for range overflow {
		board.Errorf("repeated error")
		board.Criticalf("repeated violation")
	}
	if len(board.Errors()) > score.MaxMessages || len(board.Critical()) > score.MaxMessages {
		t.Fatalf("stored errors=%d critical=%d, want <= %d", len(board.Errors()), len(board.Critical()), score.MaxMessages)
	}
	if board.ErrorCount() != overflow || board.CriticalCount() != overflow {
		t.Fatalf("counts=%d/%d, want %d", board.ErrorCount(), board.CriticalCount(), overflow)
	}
}

func TestBoardConcurrentAccess(t *testing.T) {
	board := score.NewBoard(nil)
	var wg sync.WaitGroup
	const goroutines = 50
	for range goroutines {
		wg.Go(func() {
			board.Add(score.TagFeed)
			board.Errorf("concurrent error")
			board.Total()
		})
	}
	wg.Wait()
	if got := board.Count(score.TagFeed); got != goroutines {
		t.Fatalf("Count(TagFeed) = %d, want %d", got, goroutines)
	}
}
