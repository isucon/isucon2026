package clock

import "time"

// Timer is the subset of time.Timer used by asynchronous portal workers.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock makes time-dependent diagnostics and workers testable.
type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

// Real uses the process wall clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) NewTimer(d time.Duration) Timer {
	return realTimer{Timer: time.NewTimer(d)}
}

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }
