// Package score accumulates per-tag success counts and error deductions.
// Weights are provisional until calibration (spec-bench.md §14).
package score

import (
	"fmt"
	"sync"
)

type Tag string

const (
	TagPost         Tag = "post"
	TagRealMoji     Tag = "realmoji"
	TagComment      Tag = "comment"
	TagFeed         Tag = "feed"
	TagNotification Tag = "notification"
	TagMemory       Tag = "memory"
)

var weights = map[Tag]int64{
	TagPost:         10,
	TagRealMoji:     3,
	TagComment:      3,
	TagFeed:         1,
	TagNotification: 1,
	TagMemory:       1,
}

const (
	errorPenalty = 10
	// MaxErrorMessages caps stored error strings; the count keeps growing.
	MaxErrorMessages = 20
)

type Board struct {
	mu         sync.Mutex
	counts     map[Tag]int64
	errors     []string
	errorCount int64
}

func NewBoard() *Board {
	return &Board{counts: make(map[Tag]int64, len(weights))}
}

func (b *Board) Add(tag Tag) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts[tag]++
}

func (b *Board) Errorf(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.errorCount++
	if len(b.errors) < MaxErrorMessages {
		b.errors = append(b.errors, fmt.Sprintf(format, args...))
	}
}

func (b *Board) Count(tag Tag) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.counts[tag]
}

func (b *Board) ErrorCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errorCount
}

func (b *Board) Errors() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.errors))
	copy(out, b.errors)
	return out
}

// Total is the weighted sum of successes minus the error penalty, floored at
// zero.
func (b *Board) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total int64
	for tag, count := range b.counts {
		total += weights[tag] * count
	}
	total -= errorPenalty * b.errorCount
	return max(total, 0)
}

// Breakdown returns raw success counts per tag (operator-facing, D-005).
func (b *Board) Breakdown() map[string]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int64, len(b.counts))
	for tag, count := range b.counts {
		out[string(tag)] = count
	}
	return out
}

// Totals returns weighted successes and the full error penalty, before flooring.
func (b *Board) Totals() (raw, deduction int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for tag, count := range b.counts {
		raw += weights[tag] * count
	}
	return raw, errorPenalty * b.errorCount
}
