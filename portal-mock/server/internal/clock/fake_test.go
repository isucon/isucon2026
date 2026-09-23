package clock

import (
	"testing"
	"time"
)

func TestPendingTimersTracksRegistrationStopAndFire(t *testing.T) {
	clk := NewFake(time.Unix(0, 0))
	if got := clk.PendingTimers(); got != 0 {
		t.Fatalf("initial pending timers = %d", got)
	}
	first := clk.NewTimer(time.Second)
	clk.NewTimer(2 * time.Second)
	if got := clk.PendingTimers(); got != 2 {
		t.Fatalf("registered pending timers = %d", got)
	}
	first.Stop()
	if got := clk.PendingTimers(); got != 1 {
		t.Fatalf("pending timers after stop = %d", got)
	}
	clk.Advance(2 * time.Second)
	if got := clk.PendingTimers(); got != 0 {
		t.Fatalf("pending timers after fire = %d", got)
	}
}
