package score_test

import (
	"sync"
	"testing"

	"github.com/isucon/isucon2026/bench/internal/score"
)

func TestBoardTotal(t *testing.T) {
	board := score.NewBoard(nil)
	board.Add(score.TagGatePass, "asia", 1)  // +10
	board.Add(score.TagGrandPrix, "asia", 1) // +20
	board.Add(score.TagRealMoji, "asia", 1)  // +5
	board.Add(score.TagComment, "asia", 1)   // +3
	board.Add(score.TagFeed, "asia", 1)      // +1

	if got := board.Total(); got != 39 {
		t.Fatalf("Total() = %d, want 39", got)
	}
}

func TestBoardCustomWeights(t *testing.T) {
	weights := score.Weights{score.TagGatePass: 7}
	board := score.NewBoard(weights)
	weights[score.TagGatePass] = 1000 // ボードは重みのコピーを持つ
	board.Add(score.TagGatePass, "asia", 1)
	board.Add(score.TagFeed, "asia", 1) // 重みなし
	if got := board.Total(); got != 7 {
		t.Fatalf("Total() = %d, want 7", got)
	}
}

func TestErrorsDoNotDeduct(t *testing.T) {
	board := score.NewBoard(nil)
	board.Add(score.TagGatePass, "asia", 1)
	board.Errorf("GET /x: expected 200, got 500 (internal_error)")
	board.Criticalf("feed returned a hidden post")

	if got := board.Total(); got != 10 {
		t.Fatalf("Total() = %d, want 10", got)
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
			board.Add(score.TagFeed, "asia", 1)
			board.Errorf("concurrent error")
			board.Total()
		})
	}
	wg.Wait()
	if got := board.Count(score.TagFeed); got != goroutines {
		t.Fatalf("Count(TagFeed) = %d, want %d", got, goroutines)
	}
}

func TestBoardCells(t *testing.T) {
	board := score.NewBoard(nil)
	board.Add(score.TagGatePass, "europe", 2)
	board.Add(score.TagFeed, "asia", 1)
	board.Add(score.TagFeed, "asia", 1)
	board.Add(score.TagRealMoji, "asia", 2)
	cells := board.Cells()
	want := []score.Cell{
		{Region: "asia", Day: 1, Tag: score.TagFeed, Count: 2, Points: 2},
		{Region: "asia", Day: 2, Tag: score.TagRealMoji, Count: 1, Points: 5},
		{Region: "europe", Day: 2, Tag: score.TagGatePass, Count: 1, Points: 15},
	}
	if len(cells) != len(want) {
		t.Fatalf("cells=%+v", cells)
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Fatalf("cells[%d]=%+v want %+v", i, cells[i], want[i])
		}
	}
	if got := board.DayPoints("asia", 2); got != 5 {
		t.Fatalf("DayPoints(asia, 2)=%d", got)
	}
	if got := board.DayPoints("america", 1); got != 0 {
		t.Fatalf("DayPoints(america, 1)=%d", got)
	}
	if got := board.DayPoints("europe", 2); got != 15 {
		t.Fatalf("DayPoints(europe, 2)=%d", got)
	}
	if board.Total() != 22 {
		t.Fatalf("Total=%d", board.Total())
	}
}

// 海外の得点は 1.5 倍にし、端数は合計で 1 回だけ切り捨てる。
func TestBoardRegionMultiplier(t *testing.T) {
	board := score.NewBoard(nil)
	board.Add(score.TagFeed, "asia", 1)    // 1
	board.Add(score.TagFeed, "europe", 1)  // 1.5
	board.Add(score.TagFeed, "america", 1) // 1.5
	if got := board.Total(); got != 4 {
		t.Fatalf("Total=%d, want 4", got)
	}
	board.Add(score.TagFeed, "europe", 1) // europe 3
	if got := board.Total(); got != 5 {
		t.Fatalf("Total=%d, want 5", got)
	}
	if got := board.DayPoints("europe", 1); got != 3 {
		t.Fatalf("DayPoints(europe, 1)=%d, want 3", got)
	}
	if got := board.DayPoints("america", 1); got != 1 {
		t.Fatalf("DayPoints(america, 1)=%d, want 1", got)
	}
}
