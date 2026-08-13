package vt_test

import (
	"testing"
	"time"

	"github.com/isucon/isucon2026/bench/internal/vt"
)

const tolerance = 100 * time.Millisecond

func TestClockSetBase(t *testing.T) {
	c := vt.NewClock()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	c.SetBase(base)

	if diff := c.Now().Sub(base).Abs(); diff > tolerance {
		t.Fatalf("Now() = %v, want within %v of %v", c.Now(), tolerance, base)
	}
}

func TestClockAdvance(t *testing.T) {
	c := vt.NewClock()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	c.SetBase(base)
	c.Advance(24 * time.Hour)

	want := base.Add(24 * time.Hour)
	if diff := c.Now().Sub(want).Abs(); diff > tolerance {
		t.Fatalf("Now() = %v, want within %v of %v", c.Now(), tolerance, want)
	}
}

func TestClockLocalDate(t *testing.T) {
	c := vt.NewClock()
	// 2026-07-18 23:00 UTC = 2026-07-19 08:00 Asia (UTC+9).
	c.SetBase(time.Date(2026, 7, 18, 23, 0, 0, 0, time.UTC))

	if got := c.LocalDate(9 * time.Hour); got != "2026-07-19" {
		t.Fatalf("LocalDate(+9h) = %q, want %q", got, "2026-07-19")
	}
	if got := c.LocalDate(-5 * time.Hour); got != "2026-07-18" {
		t.Fatalf("LocalDate(-5h) = %q, want %q", got, "2026-07-18")
	}
}
