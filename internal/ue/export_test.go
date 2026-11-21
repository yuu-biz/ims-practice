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
