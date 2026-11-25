package ims

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// testExpiry is an expiry of no role, claimed until until.
type testExpiry struct{ until time.Time }

func (e testExpiry) ClaimedUntil() time.Time { return e.until }
func (e testExpiry) LogFields() []any        { return nil }

// TestTheStopOfASweepLetsTheExpiriesItClaimedFinish: a sweep told to stop
// claims nothing more, and lets an expiry it has claimed be handled to
// the end, with the context of its handler still running and its Done
// after it. The handler of a role sends a NOTIFY and a SAR after it has
// removed the item, so a handler cut short would leave the item gone
// and those requests unsent.
func TestTheStopOfASweepLetsTheExpiriesItClaimedFinish(t *testing.T) {
	var mu sync.Mutex
	claims := 0
	claimed := func() int {
		mu.Lock()
		defer mu.Unlock()
		return claims
	}
	handling := make(chan context.Context, 1)
	release := make(chan struct{})
	done := make(chan struct{}, 1)
	stop := Sweep[testExpiry]{
		Kinds: []string{"test"}, Interval: 5 * time.Millisecond, Lease: time.Minute,
		Claim: func(_ context.Context, _ string, now time.Time, lease time.Duration, _ int) ([]testExpiry, error) {
			mu.Lock()
			defer mu.Unlock()
			claims++
			if claims > 1 {
				return nil, nil
			}
			return []testExpiry{{until: now.Add(lease)}}, nil
		},
		Handle: func(ctx context.Context, _ testExpiry) error {
			handling <- ctx
			<-release
			return ctx.Err()
		},
		Done: func(context.Context, testExpiry) error {
			done <- struct{}{}
			return nil
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}.Start()

	var ctx context.Context
	select {
	case ctx = <-handling:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep handled no expiry within 5 s")
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	// The rounds come every 5 ms, so a sweep that went on claiming would
	// claim several times in the 100 ms that follow.
	time.Sleep(20 * time.Millisecond)
	before := claimed()
	select {
	case <-stopped:
		t.Fatal("the sweep stopped while the handler of the expiry it claimed was still at work")
	case <-ctx.Done():
		t.Fatal("the stop of the sweep ended the context of the handler")
	case <-time.After(100 * time.Millisecond):
	}
	if after := claimed(); after != before {
		t.Errorf("the sweep claimed %d times after it was told to stop, want none", after-before)
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not stop within 5 s of the end of the handler")
	}
	select {
	case <-done:
	default:
		t.Error("the handled expiry was not marked done")
	}
}
