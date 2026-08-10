package clock

import (
	"sync/atomic"
	"time"
)

// Virtual is a Clock whose Now() adds an adjustable offset to the system clock.
// The offset is updated atomically, so concurrent reads and writes are safe
// without external synchronization.
type Virtual struct {
	offset atomic.Int64
}

func NewVirtual() *Virtual { return &Virtual{} }

func (v *Virtual) Now() time.Time {
	return time.Now().UTC().Add(time.Duration(v.offset.Load()))
}

// SetBaseTime adjusts the offset so that the next Now() call returns
// approximately base.
func (v *Virtual) SetBaseTime(base time.Time) {
	v.offset.Store(int64(base.UTC().Sub(time.Now().UTC())))
}

// Advance shifts the virtual clock by d (positive = forward).
func (v *Virtual) Advance(d time.Duration) {
	v.offset.Add(int64(d))
}

// Offset returns the current offset applied to the system clock.
func (v *Virtual) Offset() time.Duration {
	return time.Duration(v.offset.Load())
}
