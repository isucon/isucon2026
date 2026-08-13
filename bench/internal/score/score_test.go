package score_test

import (
	"sync"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/score"
)

func TestBoardTotal(t *testing.T) {
	board := score.NewBoard()
	board.Add(score.TagPost)     // +10
	board.Add(score.TagRealMoji) // +3
	board.Add(score.TagComment)  // +3
	board.Add(score.TagFeed)     // +1

	if got := board.Total(); got != 17 {
		t.Fatalf("Total() = %d, want 17", got)
	}
}

func TestBoardErrorPenalty(t *testing.T) {
	board := score.NewBoard()
	board.Add(score.TagPost) // +10
	board.Errorf("GET /x: expected 200, got 500 (internal_error)")

	if got := board.Total(); got != 0 {
		t.Fatalf("Total() = %d, want 0 (10 - 10 penalty)", got)
	}
}

func TestBoardTotalNeverNegative(t *testing.T) {
	board := score.NewBoard()
	board.Errorf("error one")
	board.Errorf("error two")

	if got := board.Total(); got != 0 {
		t.Fatalf("Total() = %d, want 0", got)
	}
}

func TestBoardErrorMessagesCapped(t *testing.T) {
	board := score.NewBoard()
	const overflow = 100
	for range overflow {
		board.Errorf("repeated error")
	}
	if got := len(board.Errors()); got > score.MaxErrorMessages {
		t.Fatalf("stored errors = %d, want <= %d", got, score.MaxErrorMessages)
	}
	if got := board.ErrorCount(); got != overflow {
		t.Fatalf("ErrorCount() = %d, want %d", got, overflow)
	}
}

func TestBoardConcurrentAccess(t *testing.T) {
	board := score.NewBoard()
	var wg sync.WaitGroup
	const goroutines = 50
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			board.Add(score.TagFeed)
			board.Errorf("concurrent error")
			board.Total()
		}()
	}
	wg.Wait()
	if got := board.Count(score.TagFeed); got != goroutines {
		t.Fatalf("Count(TagFeed) = %d, want %d", got, goroutines)
	}
}
