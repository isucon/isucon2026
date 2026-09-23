package clock

import (
	"sync"
	"time"
)

// Fake is a manually advanced clock. Timers fire synchronously during Advance.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

func NewFake(now time.Time) *Fake {
	return &Fake{now: now, timers: make(map[*fakeTimer]struct{})}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	t := &fakeTimer{
		clock:    f,
		deadline: f.now.Add(max(d, 0)),
		ch:       make(chan time.Time, 1),
		active:   true,
	}
	f.timers[t] = struct{}{}
	f.mu.Unlock()
	return t
}

// PendingTimers returns the number of timers registered but not fired or stopped.
// Tests can use it to synchronize with workers before advancing the clock.
func (f *Fake) PendingTimers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	now := f.now
	var due []*fakeTimer
	for timer := range f.timers {
		if timer.active && !timer.deadline.After(now) {
			timer.active = false
			delete(f.timers, timer)
			due = append(due, timer)
		}
	}
	f.mu.Unlock()
	for _, timer := range due {
		timer.ch <- now
	}
}

type fakeTimer struct {
	clock    *Fake
	deadline time.Time
	ch       chan time.Time
	active   bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.active
	t.active = false
	delete(t.clock.timers, t)
	return wasActive
}
