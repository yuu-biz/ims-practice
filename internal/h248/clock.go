package h248

import "time"

// Clock gives an Endpoint the time and starts its timers. The tests
// use a clock that moves only when they say so, so that they can check
// the retransmission timers and LONG-TIMER without waiting.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f once d has passed, in a goroutine of its own.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a timer that a Clock started. It names the interface rather
// than being a type of its own, so that a Clock can return one without
// importing this package, as the clock of package h248test does.
type Timer = interface {
	// Stop keeps the timer from firing. It returns false if the timer
	// has already fired or been stopped.
	Stop() bool
}

// SystemClock is the clock of the operating system. It is what an
// Endpoint uses when its configuration names no clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
