// Package vt tracks the benchmarker's view of the application's virtual time.
// It mirrors the offset-based Virtual clock in the application: the offset is
// set once from the base_time returned by POST /api/v1/initialize and moved
// forward by POST /api/v1/virtual-time/advance calls.
package vt

import (
	"sync/atomic"
	"time"
)

const localDateFormat = "2006-01-02"

type Clock struct {
	offset atomic.Int64
}

func NewClock() *Clock { return &Clock{} }

func (c *Clock) Now() time.Time {
	return time.Now().UTC().Add(time.Duration(c.offset.Load()))
}

// SetBase adjusts the offset so that Now() returns approximately base.
func (c *Clock) SetBase(base time.Time) {
	c.offset.Store(int64(base.UTC().Sub(time.Now().UTC())))
}

// Advance shifts the virtual clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.offset.Add(int64(d))
}

// LocalDate returns the calendar date (YYYY-MM-DD) of the current virtual
// time in a region with the given fixed UTC offset.
func (c *Clock) LocalDate(utcOffset time.Duration) string {
	return c.Now().Add(utcOffset).Format(localDateFormat)
}
