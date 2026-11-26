package ue

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/rtcp"
)

// SetRingingRefresh shortens the minute of RFC 3261 13.3.1.1 for one
// test.
func SetRingingRefresh(t testing.TB, d time.Duration) {
	old := ringingRefresh
	ringingRefresh = d
	t.Cleanup(func() { ringingRefresh = old })
}

// SetRTCPInterval sends RTCP every d for one test, instead of waiting
// seconds for it.
func SetRTCPInterval(t testing.TB, d time.Duration) {
	old := rtcpInterval
	rtcpInterval = func(rtcp.IntervalParams) time.Duration { return d }
	t.Cleanup(func() { rtcpInterval = old })
}

// SetBearerLossGrace shortens the wait of a UE that lost its media
// bearer for one test.
func SetBearerLossGrace(t testing.TB, d time.Duration) {
	old := bearerLossGrace
	bearerLossGrace = d
	t.Cleanup(func() { bearerLossGrace = old })
}

// RetryDelay is the retry delay of RFC 5626 4.5, with the random part
// given.
var RetryDelay = retryDelay

// SetRetryTimer has a Phone start each wait before registering again
// with start, for one test.
func SetRetryTimer(t testing.TB, start func(time.Duration) *time.Timer) {
	old := retryTimer
	retryTimer = start
	t.Cleanup(func() { retryTimer = old })
}

// RTPClock is the RTP timestamp of the next packet of m. It moves on by
// one packet each packet time, whether that packet goes out or not
// (RFC 3550 5.1).
func RTPClock(m *Media) uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ts
}
