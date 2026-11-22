package ue

import (
	"errors"
	"math/rand/v2"
	"time"
)

// The times of the retry delay of RFC 5626 4.5. TS 24.229 5.1.1.2.1 has
// the UE use their defaults when the network has provided none.
//
// SIMPLIFIED: TS 24.229 5.1.1.2.1. The network may provide max-time and
// base-time with the management objects of TS 24.167; the UE has no
// management object, so it always uses the defaults.
const (
	// retryMaxTime is max-time.
	retryMaxTime = 1800 * time.Second
	// retryBaseTime is base-time (if all failed). The UE has one P-CSCF,
	// so when its registration fails, all of its flows have failed.
	retryBaseTime = 30 * time.Second
)

// RegistrationBackoff counts the registrations of a UE that failed in a
// row, and tells how long the UE waits before its next initial
// registration (TS 24.229 5.1.1.2.1). Only one goroutine uses it.
type RegistrationBackoff struct {
	// consecutiveFailures is the consecutive-failures of RFC 5626 4.5.
	consecutiveFailures int
}

// Failed counts the registration that err ended and returns the wait
// before the next one.
//
// A response with Retry-After asks for that wait. For any other
// failure the wait is the retry delay of RFC 5626 4.5, which grows with
// each failure in a row. A REGISTER that got no response at all, when
// Timer F fired, waits the same retry delay.
//
// SIMPLIFIED: TS 24.229 5.1.1.2.1 and 5.1.1.4.1. When Timer F fires, or
// a 503 comes with a Retry-After longer than Timer F, the UE should
// mark the P-CSCF as unavailable and register through another one, or
// find the P-CSCFs again. It knows one P-CSCF address, which a new
// discovery would give again, so it waits and registers through the
// same P-CSCF. After a failed refresh it always makes an initial
// registration, also where 5.1.1.4.1 lets it refresh after a short
// Retry-After of a 503: no response to a REGISTER reaches the UE with
// Retry-After here. When the S-CSCF answers 503 with one, the P-CSCF
// answers the UE 504.
func (b *RegistrationBackoff) Failed(err error) time.Duration {
	if wait := retryAfter(err); wait > 0 {
		// TS 24.229 5.1.1.2.1: no initial registration comes within the
		// time of Retry-After, so the count of failed attempts starts
		// again.
		b.consecutiveFailures = 0
		return wait
	}
	b.consecutiveFailures++
	return retryDelay(b.consecutiveFailures, rand.Float64())
}

// Reset starts the count again, after a registration that succeeded or
// when the UE stops trying.
func (b *RegistrationBackoff) Reset() { b.consecutiveFailures = 0 }

// retryDelay is the retry delay of RFC 5626 4.5 after
// consecutiveFailures failures in a row: the upper-bound wait time
//
//	W = min(max-time, base-time * 2^consecutive-failures)
//
// and, with uniform from [0, 1), a time from 50 to 100% of W.
func retryDelay(consecutiveFailures int, uniform float64) time.Duration {
	w := retryBaseTime
	for range consecutiveFailures {
		if w >= retryMaxTime {
			break
		}
		w *= 2
	}
	w = min(w, retryMaxTime)
	return w/2 + time.Duration(uniform*float64(w/2))
}

// AsksToWait tells whether err is a response with Retry-After. After a
// refresh that got one, the UE waits too before its initial
// registration: for the time of Retry-After it makes none through the
// same P-CSCF (TS 24.229 5.1.1.2.1).
func AsksToWait(err error) bool { return retryAfter(err) > 0 }

func retryAfter(err error) time.Duration {
	var failure *RegisterFailure
	if errors.As(err, &failure) {
		return failure.RetryAfter
	}
	return 0
}
