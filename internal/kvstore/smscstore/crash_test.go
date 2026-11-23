package smscstore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/smscstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// The tests in this file stop an instance before each of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. Two things are checked of every state
// such a stop leaves behind:
//
//   - the instance is told of the failure, never that a queue is missing
//     or that a destination is not due, so that the SC keeps the short
//     message instead of losing it;
//   - a queue that holds messages is still handed out by ClaimDue at the
//     time the queue is due, which is the rule the Store keeps between
//     the queue and its entry in the time index.
//
// The number of writes each operation makes is named where a case stops
// one, since that is what a stop counts.

// Fictitious numbers, from +1 202 555 0100 to 0199.
const bob = "12025550122"

const lease = time.Minute

func message(id string, now time.Time) smsc.Message {
	return smsc.Message{ID: id, Destination: bob, Expires: now.Add(time.Hour),
		Deliver: &sms.Deliver{OriginatingAddress: sms.InternationalNumber("+12025550111"), ServiceCentreTimeStamp: now}}
}

// instances are two Stores on one store, as two SC instances are, and
// stopped is a third whose store stops after writes writes.
func instances(t *testing.T, kv kvstore.Store, writes int) (one, other, stopped *smscstore.Store) {
	t.Helper()
	cfg := kvjson.Config{Prefix: storetest.Prefix()}
	return smscstore.New(kv, cfg), smscstore.New(kv, cfg), smscstore.New(storetest.StopAfter(kv, writes), cfg)
}

// claimedAt claims the destination that is due at when, and fails the
// test when it is not the one expected. want is empty for "none is due".
func claimedAt(t *testing.T, s *smscstore.Store, when time.Time, want string) smsc.Lease {
	t.Helper()
	l, ok, err := s.ClaimDue(context.Background(), when, lease)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	switch {
	case want == "" && ok:
		t.Fatalf("ClaimDue handed out %s, want none due", l.Destination)
	case want != "" && !ok:
		t.Fatalf("ClaimDue handed out none, want %s", want)
	case want != "" && l.Destination != want:
		t.Fatalf("ClaimDue handed out %s, want %s", l.Destination, want)
	}
	return l
}

// messagesOf are the messages of a leased destination.
func messagesOf(t *testing.T, s *smscstore.Store, l smsc.Lease) []string {
	t.Helper()
	q, err := s.Queue(context.Background(), l)
	if err != nil {
		t.Fatalf("Queue(%s): %v", l.Destination, err)
	}
	ids := make([]string, len(q))
	for i, m := range q {
		ids[i] = m.ID
	}
	return ids
}

// waiting puts one message in the queue of bob and leaves the
// destination due at next, with one failed attempt behind it, as a
// delivery that did not get through does.
func waiting(t *testing.T, s *smscstore.Store, now, next time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.Enqueue(ctx, message("m1", now), now); err != nil {
		t.Fatal(err)
	}
	l, ok, err := s.Claim(ctx, bob, now, lease)
	if err != nil || !ok {
		t.Fatalf("Claim = %v, %v", ok, err)
	}
	if err := s.Release(ctx, l, next, 1); err != nil {
		t.Fatal(err)
	}
}

// TestAStoppedInstanceIsToldOfTheFailure: every operation of an instance
// whose store has stopped returns the failure, and what it failed to
// change is as it was.
func TestAStoppedInstanceIsToldOfTheFailure(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			other, _, stopped := instances(t, backend.New(t), 0)
			if err := other.Enqueue(ctx, message("m1", now), now); err != nil {
				t.Fatal(err)
			}
			held, ok, err := other.Claim(ctx, bob, now, lease)
			if err != nil || !ok {
				t.Fatalf("Claim = %v, %v", ok, err)
			}

			if err := stopped.Enqueue(ctx, message("m2", now), now); err == nil {
				t.Error("Enqueue returned no error")
			}
			if _, err := stopped.ServiceCentreTimeStamp(ctx, bob, now); err == nil {
				t.Error("ServiceCentreTimeStamp returned no error")
			}
			if l, ok, err := stopped.ClaimDue(ctx, now.Add(time.Hour), lease); err == nil {
				t.Errorf("ClaimDue = %+v, %v, no error", l, ok)
			}
			if l, ok, err := stopped.Claim(ctx, bob, now.Add(time.Hour), lease); err == nil {
				t.Errorf("Claim = %+v, %v, no error", l, ok)
			}
			if q, err := stopped.Queue(ctx, held); err == nil {
				t.Errorf("Queue = %+v, no error", q)
			}
			if err := stopped.Remove(ctx, held, "m1"); err == nil {
				t.Error("Remove returned no error")
			}
			if err := stopped.Release(ctx, held, now.Add(time.Hour), 1); err == nil {
				t.Error("Release returned no error")
			}
			if held, err := stopped.Alert(ctx, bob, now); err == nil {
				t.Errorf("Alert = %v, no error", held)
			}

			if got := messagesOf(t, other, held); len(got) != 1 || got[0] != "m1" {
				t.Errorf("queue on the other instance = %v; want the one message", got)
			}
		})
	}
}

// TestEnqueueStoppedAtEveryStep: an Enqueue to a destination that has no
// queue yet writes the entry in the index (write 1), the queue (write 2)
// and the entry again, for the queue it read back (write 3). From the
// queue's write on, the message is in the store and the destination is
// handed out at the time it is due; before that, nothing of it is.
func TestEnqueueStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			for _, c := range []struct {
				name   string
				writes int
			}{
				{"BeforeTheEntry", 0},
				{"BeforeTheQueue", 1},
				{"BeforeTheEntryIsReconciled", 2},
				{"WithEveryWriteDone", 3},
			} {
				t.Run(c.name, func(t *testing.T) {
					other, _, stopped := instances(t, backend.New(t), c.writes)
					err := stopped.Enqueue(ctx, message("m1", now), now)
					if (c.writes < 3) != (err != nil) {
						t.Fatalf("Enqueue = %v, after %d writes", err, c.writes)
					}
					if c.writes >= 2 {
						// The queue is written: whether or not the Enqueue got
						// as far as its reconcile, the message is handed out.
						l := claimedAt(t, other, now, bob)
						if got := messagesOf(t, other, l); len(got) != 1 || got[0] != "m1" {
							t.Errorf("queue = %v, want the message", got)
						}
						return
					}
					// Nothing of the message is in the store, and a stale
					// entry, if one was written, is taken out rather than
					// handed out again and again.
					if _, ok, err := other.Claim(ctx, bob, now, lease); err != nil || ok {
						t.Errorf("Claim = %v, %v; want no queue", ok, err)
					}
					claimedAt(t, other, now, "")
					claimedAt(t, other, now, "")
					// The destination works again afterwards.
					if err := other.Enqueue(ctx, message("m2", now), now); err != nil {
						t.Fatal(err)
					}
					l := claimedAt(t, other, now, bob)
					if got := messagesOf(t, other, l); len(got) != 1 || got[0] != "m2" {
						t.Errorf("queue after a second Enqueue = %v, want m2", got)
					}
				})
			}
		})
	}
}

// TestEnqueueToAWaitingDestinationStoppedAtEveryStep: an Enqueue to a
// destination that is already waiting for its next attempt writes the
// entry at now (write 1), the queue (write 2) and then the entry back at
// the time the queue is due (write 3), since a destination that had a
// queue keeps its time. A stop leaves the entry early, never late.
func TestEnqueueToAWaitingDestinationStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			next := now.Add(10 * time.Minute)
			for writes := range 4 {
				t.Run(fmt.Sprintf("After%dWrites", writes), func(t *testing.T) {
					other, _, stopped := instances(t, backend.New(t), writes)
					waiting(t, other, now, next)

					err := stopped.Enqueue(ctx, message("m2", now), now)
					if (writes < 3) != (err != nil) {
						t.Fatalf("Enqueue = %v, after %d writes", err, writes)
					}
					want := []string{"m1", "m2"}
					if writes < 2 {
						want = []string{"m1"}
					}
					// Whatever the entry says, the destination comes back at
					// the time its queue is due, and not before.
					claimedAt(t, other, now, "")
					l := claimedAt(t, other, next, bob)
					if got := messagesOf(t, other, l); len(got) != len(want) || got[0] != want[0] {
						t.Errorf("queue = %v, want %v", got, want)
					}
				})
			}
		})
	}
}

// TestReleaseStoppedAtEveryStep: a release whose next attempt comes
// after the end of the lease writes the queue (write 1) and then the
// entry (write 2); one that comes before writes the entry first (write
// 1), then the queue (write 2), then the entry again for the queue it
// read back (write 3). Either way a stop leaves the entry at or before
// the time the queue is due.
func TestReleaseStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			for _, when := range []struct {
				name string
				next time.Duration
				// queueWrite is the write that puts the released queue in
				// the store: the first, or the second where the entry goes
				// in before it. writes is how many the release makes.
				queueWrite int
				writes     int
			}{
				{"AfterTheLease", 10 * time.Minute, 1, 2},
				{"WithinTheLease", lease / 2, 2, 3},
			} {
				for writes := range when.writes + 1 {
					t.Run(fmt.Sprintf("%sAfter%dWrites", when.name, writes), func(t *testing.T) {
						other, _, stopped := instances(t, backend.New(t), writes)
						if err := other.Enqueue(ctx, message("m1", now), now); err != nil {
							t.Fatal(err)
						}
						l, ok, err := other.Claim(ctx, bob, now, lease)
						if err != nil || !ok {
							t.Fatalf("Claim = %v, %v", ok, err)
						}
						next := now.Add(when.next)

						err = stopped.Release(ctx, l, next, 1)
						if (writes < when.writes) != (err != nil) {
							t.Fatalf("Release = %v, after %d writes", err, writes)
						}
						// A release that did not get as far as the queue
						// leaves the lease standing, so the destination comes
						// back when the lease runs out; one that wrote the
						// queue comes back at the time it named.
						back := next
						if writes < when.queueWrite {
							back = now.Add(lease)
						}
						claimedAt(t, other, back.Add(-time.Millisecond), "")
						taken := claimedAt(t, other, back, bob)
						if got := messagesOf(t, other, taken); len(got) != 1 || got[0] != "m1" {
							t.Errorf("queue = %v, want the message", got)
						}
					})
				}
			}
		})
	}
}

// TestReleaseOfAnEmptyQueueStoppedAtEveryStep: the release of a queue
// whose last message is gone removes the queue (write 1) and then takes
// its entry out (write 2). A stop in between leaves an entry that names
// no queue, which the next ClaimDue takes out.
func TestReleaseOfAnEmptyQueueStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			for writes := range 3 {
				t.Run(fmt.Sprintf("After%dWrites", writes), func(t *testing.T) {
					other, _, stopped := instances(t, backend.New(t), writes)
					if err := other.Enqueue(ctx, message("m1", now), now); err != nil {
						t.Fatal(err)
					}
					l, ok, err := other.Claim(ctx, bob, now, lease)
					if err != nil || !ok {
						t.Fatalf("Claim = %v, %v", ok, err)
					}
					if err := other.Remove(ctx, l, "m1"); err != nil {
						t.Fatal(err)
					}

					err = stopped.Release(ctx, l, now.Add(time.Hour), 0)
					if (writes < 2) != (err != nil) {
						t.Fatalf("Release = %v, after %d writes", err, writes)
					}
					if writes == 0 {
						// The lease stands and the queue, empty, is still
						// there: the instance that takes the destination over
						// releases it again.
						taken := claimedAt(t, other, now.Add(lease), bob)
						if got := messagesOf(t, other, taken); len(got) != 0 {
							t.Errorf("queue = %v, want it empty", got)
						}
						return
					}
					// The queue is gone. Whatever is left of the entry, no
					// destination is handed out, and a stale entry is taken
					// out rather than handed out again.
					claimedAt(t, other, now.Add(time.Hour), "")
					claimedAt(t, other, now.Add(time.Hour), "")
					if held, err := other.Alert(ctx, bob, now); err != nil || held {
						t.Errorf("Alert = %v, %v; want no queue", held, err)
					}
				})
			}
		})
	}
}

// TestAlertStoppedAtEveryStep: an alert writes the entry at now (write
// 1), the queue (write 2) and the entry again for the queue it read back
// (write 3). A stop before the queue loses the alert, which the instance
// reports to the HSS, and leaves the entry early; the destination still
// comes back at the time its queue says.
func TestAlertStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			next := now.Add(10 * time.Minute)
			for writes := range 4 {
				t.Run(fmt.Sprintf("After%dWrites", writes), func(t *testing.T) {
					other, _, stopped := instances(t, backend.New(t), writes)
					waiting(t, other, now, next)
					alerted := now.Add(time.Minute)

					held, err := stopped.Alert(ctx, bob, alerted)
					switch {
					case writes < 3 && err == nil:
						t.Fatalf("Alert = %v, no error after %d writes", held, writes)
					case writes == 3 && (err != nil || !held):
						t.Fatalf("Alert = %v, %v; want the queue found", held, err)
					}
					if writes >= 2 {
						// The queue holds the alert, so the destination is due
						// at the time of the alert whether or not the entry was
						// written again afterwards.
						claimedAt(t, other, alerted, bob)
						return
					}
					claimedAt(t, other, alerted, "")
					if got := messagesOf(t, other, claimedAt(t, other, next, bob)); len(got) != 1 {
						t.Errorf("queue = %v, want the message", got)
					}
				})
			}
		})
	}
}

// TestClaimDueStoppedAtEveryStep: a ClaimDue claims the entry of the
// destination that is due (write 1), which moves the entry to the end of
// the claim, and then writes the lease into the queue (write 2). An
// instance that stops in between leaves the destination to be taken over
// when the claim runs out, as a lease holder that stops does.
func TestClaimDueStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			for writes := range 3 {
				t.Run(fmt.Sprintf("After%dWrites", writes), func(t *testing.T) {
					other, _, stopped := instances(t, backend.New(t), writes)
					if err := other.Enqueue(ctx, message("m1", now), now); err != nil {
						t.Fatal(err)
					}

					l, ok, err := stopped.ClaimDue(ctx, now, lease)
					if (writes < 2) != (err != nil) {
						t.Fatalf("ClaimDue = %+v, %v, %v; after %d writes", l, ok, err, writes)
					}
					if writes == 2 {
						if !ok || l.Destination != bob {
							t.Fatalf("ClaimDue = %+v, %v; want the lease of %s", l, ok, bob)
						}
						return
					}
					// Before the claim nothing moved, so the destination is
					// still due now; after it, it comes back at the end of the
					// claim, which is the end of the lease it would have given.
					back := now
					if writes == 1 {
						back = now.Add(lease)
						claimedAt(t, other, back.Add(-time.Millisecond), "")
					}
					taken := claimedAt(t, other, back, bob)
					if got := messagesOf(t, other, taken); len(got) != 1 || got[0] != "m1" {
						t.Errorf("queue = %v, want the message", got)
					}
				})
			}
		})
	}
}

// TestTheFixUpOfAStaleEntryStoppedAtEveryStep: a ClaimDue that finds an
// entry whose queue is gone claims the entry (write 1), takes it out
// (write 2) and looks for the next destination that is due (write 3,
// since a claim moves what it hands out). An instance that stops in
// between leaves the entry for the next claim to take out.
func TestTheFixUpOfAStaleEntryStoppedAtEveryStep(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			for writes := range 4 {
				t.Run(fmt.Sprintf("After%dWrites", writes), func(t *testing.T) {
					kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
					other := smscstore.New(kv, cfg)
					// An Enqueue that stopped before it wrote the queue leaves
					// an entry that names none.
					if err := smscstore.New(storetest.StopAfter(kv, 1), cfg).Enqueue(ctx, message("m1", now), now); err == nil {
						t.Fatal("the Enqueue that stopped before the queue returned no error")
					}

					stopped := smscstore.New(storetest.StopAfter(kv, writes), cfg)
					l, ok, err := stopped.ClaimDue(ctx, now, lease)
					switch {
					case ok:
						t.Fatalf("ClaimDue handed out %s, want none: its queue is gone", l.Destination)
					case (writes < 3) != (err != nil):
						t.Fatalf("ClaimDue = %v after %d writes", err, writes)
					}
					// However far it got, no destination is handed out, and
					// the entry is taken out by this claim or the next.
					claimedAt(t, other, now, "")
					claimedAt(t, other, now, "")
					// The destination works again afterwards.
					if err := other.Enqueue(ctx, message("m2", now), now); err != nil {
						t.Fatal(err)
					}
					taken := claimedAt(t, other, now, bob)
					if got := messagesOf(t, other, taken); len(got) != 1 || got[0] != "m2" {
						t.Errorf("queue = %v, want m2", got)
					}
				})
			}
		})
	}
}
