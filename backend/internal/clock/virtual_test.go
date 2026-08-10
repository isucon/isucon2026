package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/isucon/isucon2026/backend/internal/clock"
)

func TestVirtualNow_DefaultOffset(t *testing.T) {
	v := clock.NewVirtual()
	before := time.Now().UTC()
	got := v.Now()
	after := time.Now().UTC()

	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestVirtualSetBaseTime(t *testing.T) {
	v := clock.NewVirtual()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	v.SetBaseTime(base)

	got := v.Now()
	diff := got.Sub(base).Abs()
	const tolerance = 100 * time.Millisecond
	if diff > tolerance {
		t.Fatalf("Now() = %v, want within %v of %v (diff=%v)", got, tolerance, base, diff)
	}
}

func TestVirtualAdvance(t *testing.T) {
	v := clock.NewVirtual()
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	v.SetBaseTime(base)
	v.Advance(24 * time.Hour)

	got := v.Now()
	want := base.Add(24 * time.Hour)
	diff := got.Sub(want).Abs()
	const tolerance = 100 * time.Millisecond
	if diff > tolerance {
		t.Fatalf("Now() = %v, want within %v of %v (diff=%v)", got, tolerance, want, diff)
	}
}

func TestVirtualOffset(t *testing.T) {
	v := clock.NewVirtual()
	if got := v.Offset(); got != 0 {
		t.Fatalf("initial Offset() = %v, want 0", got)
	}

	v.Advance(5 * time.Second)
	if got := v.Offset(); got != 5*time.Second {
		t.Fatalf("Offset() = %v, want %v", got, 5*time.Second)
	}
}

func TestVirtualConcurrentAccess(t *testing.T) {
	v := clock.NewVirtual()
	v.SetBaseTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	var wg sync.WaitGroup
	const goroutines = 100
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			v.Now()
			v.Advance(time.Millisecond)
			v.Now()
		}()
	}
	wg.Wait()
}
