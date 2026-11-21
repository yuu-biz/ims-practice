package smsc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sms"
)

// Fictitious numbers, from +1 202 555 0100 to 0199.
const (
	alice = "12025550111"
	bob   = "12025550122"
	carol = "12025550133"
)

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func message(id, dest string) Message {
	return Message{ID: id, Destination: dest, Deliver: &sms.Deliver{OriginatingAddress: sms.InternationalNumber("+" + alice)}, Expires: t0.Add(time.Hour)}
}

func TestMemoryStoreClaimsInOrderOfTime(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Enqueue(ctx, message("m1", bob), t0.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(ctx, message("m2", carol), t0.Add(time.Second))
	s.Enqueue(ctx, message("m3", bob), t0.Add(3*time.Second)) // behind m1: bob keeps its time

	if _, ok, _ := s.ClaimDue(ctx, t0, time.Minute); ok {
		t.Fatal("claimed a destination before its time")
	}
	now := t0.Add(5 * time.Second)
	first, ok, err := s.ClaimDue(ctx, now, time.Minute)
	if err != nil || !ok || first.Destination != carol {
		t.Fatalf("first claim = %+v, %v, %v, want carol", first, ok, err)
	}
	second, ok, _ := s.ClaimDue(ctx, now, time.Minute)
	if !ok || second.Destination != bob {
		t.Fatalf("second claim = %+v, %v, want bob", second, ok)
	}
	if l, ok, _ := s.ClaimDue(ctx, now, time.Minute); ok {
		t.Fatalf("third claim = %+v, want none: both are leased", l)
	}
	if _, ok, _ := s.Claim(ctx, bob, now, time.Minute); ok {
		t.Fatal("claimed bob twice")
	}
	queue, err := s.Queue(ctx, second)
	if err != nil || len(queue) != 2 || queue[0].ID != "m1" || queue[1].ID != "m3" {
		t.Fatalf("queue of bob = %+v, %v", queue, err)
	}
}

func TestMemoryStoreLeaseRunsOut(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	s.Enqueue(ctx, message("m1", bob), t0)
	old, _, _ := s.Claim(ctx, bob, t0, time.Minute)
	if _, ok, _ := s.Claim(ctx, bob, t0.Add(59*time.Second), time.Minute); ok {
		t.Fatal("claimed bob during the lease")
	}
	// An instance that stopped never releases; another takes over.
	taken, ok, _ := s.ClaimDue(ctx, t0.Add(time.Minute), time.Minute)
	if !ok || taken.Destination != bob || taken.Token == old.Token {
		t.Fatalf("claim after the lease = %+v, %v", taken, ok)
	}
	for name, err := range map[string]error{
		"Queue":   func() error { _, err := s.Queue(ctx, old); return err }(),
		"Remove":  s.Remove(ctx, old, "m1"),
		"Release": s.Release(ctx, old, t0, 0),
	} {
		if !errors.Is(err, ErrLeaseLost) {
			t.Errorf("%s with the old lease: %v, want ErrLeaseLost", name, err)
		}
	}
	if q, err := s.Queue(ctx, taken); err != nil || len(q) != 1 {
		t.Errorf("queue with the new lease = %+v, %v", q, err)
	}
}

func TestMemoryStoreRelease(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	s.Enqueue(ctx, message("m1", bob), t0)
	l, _, _ := s.Claim(ctx, bob, t0, time.Minute)
	if err := s.Release(ctx, l, t0.Add(30*time.Second), 3); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ClaimDue(ctx, t0.Add(29*time.Second), time.Minute); ok {
		t.Fatal("claimed before the time of the release")
	}
	l, ok, _ := s.ClaimDue(ctx, t0.Add(30*time.Second), time.Minute)
	if !ok || l.Attempts != 3 {
		t.Fatalf("claim = %+v, %v, want 3 attempts kept", l, ok)
	}
	s.Remove(ctx, l, "m1")
	if err := s.Release(ctx, l, t0.Add(time.Hour), 0); err != nil {
		t.Fatal(err)
	}
	// An empty queue is forgotten.
	if ok, _ := s.Alert(ctx, bob, t0); ok {
		t.Error("Alert found bob after its queue emptied")
	}
	if _, ok, _ := s.ClaimDue(ctx, t0.Add(2*time.Hour), time.Minute); ok {
		t.Error("claimed a forgotten destination")
	}
}

func TestMemoryStoreAlert(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if ok, _ := s.Alert(ctx, bob, t0); ok {
		t.Error("Alert found bob, who has no queue")
	}
	s.Enqueue(ctx, message("m1", bob), t0)
	l, _, _ := s.Claim(ctx, bob, t0, time.Minute)
	s.Release(ctx, l, t0.Add(time.Hour), 1)

	// Waiting for a retry: the alert brings it forward.
	if ok, _ := s.Alert(ctx, bob, t0.Add(time.Minute)); !ok {
		t.Fatal("Alert did not find bob")
	}
	l, ok, _ := s.Claim(ctx, bob, t0.Add(time.Minute), time.Minute)
	if !ok {
		t.Fatal("bob is not due after the alert")
	}
	// An alert during the lease counts once the lease ends.
	s.Alert(ctx, bob, t0.Add(70*time.Second))
	s.Release(ctx, l, t0.Add(time.Hour), 2)
	if _, ok, _ := s.Claim(ctx, bob, t0.Add(70*time.Second), time.Minute); !ok {
		t.Error("the alert during the lease was lost")
	}
}

func TestMemoryStoreServiceCentreTimeStamp(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	at := t0.Add(400 * time.Millisecond)
	for i, want := range []time.Time{t0, t0.Add(time.Second), t0.Add(2 * time.Second)} {
		if got, _ := s.ServiceCentreTimeStamp(ctx, bob, at); !got.Equal(want) {
			t.Errorf("time stamp %d for bob = %v, want %v", i, got, want)
		}
	}
	if got, _ := s.ServiceCentreTimeStamp(ctx, carol, at); !got.Equal(t0) {
		t.Errorf("time stamp for carol = %v, want %v", got, t0)
	}
	later := t0.Add(10 * time.Second)
	if got, _ := s.ServiceCentreTimeStamp(ctx, bob, later); !got.Equal(later) {
		t.Errorf("time stamp for bob later = %v, want %v", got, later)
	}
	if len(s.timestamps) != 1 {
		t.Errorf("%d time stamps kept, want only the recent one", len(s.timestamps))
	}
}

// TestMemoryStoreOneLeasePerDestination has many instances claim at
// once. Run it with -race.
func TestMemoryStoreOneLeasePerDestination(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	for _, dest := range []string{alice, bob, carol} {
		s.Enqueue(ctx, message("m-"+dest, dest), t0)
	}
	var mu sync.Mutex
	claimed := map[string]int{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if l, ok, _ := s.ClaimDue(ctx, t0, time.Minute); ok {
				mu.Lock()
				claimed[l.Destination]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(claimed) != 3 {
		t.Errorf("claimed %v, want each of the three once", claimed)
	}
	for dest, n := range claimed {
		if n != 1 {
			t.Errorf("%s claimed %d times", dest, n)
		}
	}
}
