// clock.go provides an injectable time source for deterministic testing.
// Owns: time abstraction for business logic, risk rules, and token buckets.
// Must never: call time.Now() directly in business logic — always inject Clock.
// Relies on: time standard library.
package platform

import (
	"sync"
	"time"
)

// Clock provides current time.
type Clock interface {
	Now() time.Time
}

// RealClock uses the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

// FakeClock is a controllable clock for deterministic unit and property tests.
type FakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFakeClock creates a FakeClock initialized to the given time.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t.UTC()}
}

func (f *FakeClock) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

// Set explicitly sets the current time.
func (f *FakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

// Advance moves the fake clock forward by the given duration.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
