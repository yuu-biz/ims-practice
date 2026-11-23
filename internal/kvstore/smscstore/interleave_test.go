package smscstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/smscstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

// The tests in this file let one instance work in the middle of another
// instance's operation, with storetest.BeforeWrite, so that each
// interleaving of the two is a case of its own. The queue of a
// destination and its entry in the time index are two items, and these
// are the orderings in which the writes to the two could undo each
// other.

// TestAClaimerBetweenTheEntryAndTheQueueOfAnEnqueue: the first message
// to a destination writes the entry before the queue, and a ClaimDue in
// between claims that entry, finds no queue and takes the entry out. The
// Enqueue may not take the entry for granted afterwards: nothing else
// would ever hand the queue it is about to write out.
func TestAClaimerBetweenTheEntryAndTheQueueOfAnEnqueue(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := smscstore.New(kv, cfg)

			enqueueing := smscstore.New(storetest.BeforeWrite(kv, 2, func() {
				if l, ok, err := other.ClaimDue(ctx, now, lease); err != nil || ok {
					t.Errorf("the ClaimDue in between = %+v, %v, %v; want none due", l, ok, err)
				}
			}), cfg)
			if err := enqueueing.Enqueue(ctx, message("m1", now), now); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}

			taken := claimedAt(t, other, now, bob)
			if got := messagesOf(t, other, taken); len(got) != 1 || got[0] != "m1" {
				t.Errorf("queue = %v, want the message", got)
			}
		})
	}
}

// TestAnEnqueueBetweenTheEntryAndTheQueueOfAnAlert: an alert writes the
// entry at its own time before the queue, and a whole Enqueue in between
// leaves the entry at the time the waiting queue is due, an hour out. The
// alert may not take the entry for granted afterwards: the queue it then
// writes says the destination is due at once.
func TestAnEnqueueBetweenTheEntryAndTheQueueOfAnAlert(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			next := now.Add(time.Hour)
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := smscstore.New(kv, cfg)
			waiting(t, other, now, next)

			alerted := now.Add(time.Minute)
			alerting := smscstore.New(storetest.BeforeWrite(kv, 2, func() {
				if err := other.Enqueue(ctx, message("m2", now), now); err != nil {
					t.Errorf("the Enqueue in between: %v", err)
				}
			}), cfg)
			if held, err := alerting.Alert(ctx, bob, alerted); err != nil || !held {
				t.Fatalf("Alert = %v, %v; want the queue found", held, err)
			}

			taken := claimedAt(t, other, alerted, bob)
			if got := messagesOf(t, other, taken); len(got) != 2 {
				t.Errorf("queue = %v, want both messages", got)
			}
		})
	}
}

// TestReleaseOfAnEmptyQueueKeepsAMessageThatCameInBetween: one instance
// forgets a destination whose queue it has emptied while another puts a
// new short message there. The entry the second instance wrote for its
// own queue must stay, or nothing would ever hand that queue out again.
func TestReleaseOfAnEmptyQueueKeepsAMessageThatCameInBetween(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := smscstore.New(kv, cfg)
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

			// The release removes the queue with its first write and takes
			// the entry out with its second. In between, the new message
			// arrives at another instance.
			releasing := smscstore.New(storetest.BeforeWrite(kv, 2, func() {
				if err := other.Enqueue(ctx, message("m2", now), now); err != nil {
					t.Errorf("the Enqueue in between: %v", err)
				}
			}), cfg)
			if err := releasing.Release(ctx, l, now.Add(time.Hour), 0); err != nil {
				t.Fatalf("Release: %v", err)
			}

			taken := claimedAt(t, other, now, bob)
			if got := messagesOf(t, other, taken); len(got) != 1 || got[0] != "m2" {
				t.Errorf("queue = %v, want the message that came in between", got)
			}
		})
	}
}

// TestReleaseDoesNotOverwriteAnAlertThatCameInBetween: one instance
// releases a destination for a later attempt while an alert from the HSS
// brings it forward. The entry must end up at the time of the alert: the
// alert is the whole reason the HSS sent it, and nothing looks at a queue
// before the time its entry names.
func TestReleaseDoesNotOverwriteAnAlertThatCameInBetween(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			next := now.Add(10 * time.Minute)
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := smscstore.New(kv, cfg)
			if err := other.Enqueue(ctx, message("m1", now), now); err != nil {
				t.Fatal(err)
			}
			l, ok, err := other.Claim(ctx, bob, now, lease)
			if err != nil || !ok {
				t.Fatalf("Claim = %v, %v", ok, err)
			}

			// The release writes the queue with its first write and the
			// entry with its second. In between, the alert reaches another
			// instance, which finds the destination released and brings it
			// forward.
			alerted := now.Add(time.Minute)
			releasing := smscstore.New(storetest.BeforeWrite(kv, 2, func() {
				if held, err := other.Alert(ctx, bob, alerted); err != nil || !held {
					t.Errorf("the Alert in between = %v, %v", held, err)
				}
			}), cfg)
			if err := releasing.Release(ctx, l, next, 1); err != nil {
				t.Fatalf("Release: %v", err)
			}

			taken := claimedAt(t, other, alerted, bob)
			if taken.Attempts != 1 {
				t.Errorf("the lease after the alert = %+v, want the one attempt kept", taken)
			}
			if got := messagesOf(t, other, taken); len(got) != 1 || got[0] != "m1" {
				t.Errorf("queue = %v, want the message", got)
			}
		})
	}
}

// TestTheFixUpOfAStaleEntryKeepsANewQueue: an entry names a destination
// whose queue is not there, which the instance that claims it takes out;
// in between, another instance writes a queue for that destination and an
// entry of its own. The entry taken out must be the stale one, not the
// new one.
func TestTheFixUpOfAStaleEntryKeepsANewQueue(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := smscstore.New(kv, cfg)
			// An instance that stopped after it had written the entry, and
			// before the queue, leaves the entry behind.
			if err := smscstore.New(storetest.StopAfter(kv, 1), cfg).Enqueue(ctx, message("m1", now), now); err == nil {
				t.Fatal("the Enqueue that stopped before the queue returned no error")
			}

			// The claimer of that entry takes it out with its first write.
			// In between, the message is enqueued again, as the SC does
			// when the UE sends it a second time.
			claiming := smscstore.New(storetest.BeforeWrite(kv, 1, func() {
				if err := other.Enqueue(ctx, message("m2", now), now); err != nil {
					t.Errorf("the Enqueue in between: %v", err)
				}
			}), cfg)
			l, ok, err := claiming.ClaimDue(ctx, now, lease)
			if err != nil {
				t.Fatalf("ClaimDue: %v", err)
			}
			// Whether the queue that came in between is handed to this
			// claimer or to the next one, it is handed out.
			if !ok {
				l = claimedAt(t, other, now, bob)
			}
			if l.Destination != bob {
				t.Fatalf("ClaimDue handed out %s, want %s", l.Destination, bob)
			}
			if got := messagesOf(t, claiming, l); len(got) != 1 || got[0] != "m2" {
				t.Errorf("queue = %v, want the message that came in between", got)
			}
		})
	}
}

// TestAnAlertKeepsTheQueueOfADestinationThatIsBeingForgotten: the alert
// reaches an instance just as another forgets the destination, so the
// alert writes an entry for a queue that is on its way out. That entry
// must not be left behind to be claimed for ever.
func TestAnAlertKeepsTheQueueOfADestinationThatIsBeingForgotten(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := smscstore.New(kv, cfg)
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

			// The alert writes its entry with its first write, before it
			// knows whether the destination has a queue at all. In between,
			// the destination is forgotten.
			alerting := smscstore.New(storetest.BeforeWrite(kv, 1, func() {
				if err := other.Release(ctx, l, now.Add(time.Hour), 0); err != nil {
					t.Errorf("the Release in between: %v", err)
				}
			}), cfg)
			if held, err := alerting.Alert(ctx, bob, now); err != nil || held {
				t.Errorf("Alert = %v, %v; want no queue found", held, err)
			}

			claimedAt(t, other, now.Add(time.Hour), "")
			claimedAt(t, other, now.Add(time.Hour), "")
		})
	}
}
