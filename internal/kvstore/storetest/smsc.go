package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// SMSC runs the suite of smsc.Store.
func SMSC(t *testing.T, newPair func(t *testing.T) (smsc.Store, smsc.Store)) {
	run(t, newPair, []testCase[smsc.Store]{
		{"ClaimsInOrderOfTime", smscClaimsInOrderOfTime},
		{"QueueHoldsWhatWasEnqueued", smscQueueHoldsWhatWasEnqueued},
		{"LeaseRunsOut", smscLeaseRunsOut},
		{"Release", smscRelease},
		{"Alert", smscAlert},
		{"ServiceCentreTimeStamp", smscServiceCentreTimeStamp},
		{"OneLeasePerDestination", smscOneLeasePerDestination},
	})
}

// Fictitious numbers, from +1 202 555 0100 to 0199.
const (
	alice = "12025550111"
	bob   = "12025550122"
	carol = "12025550133"
)

// The cases give the Store the times they choose, as the SC gives it the
// time of its clock, so that none of them has to wait out a lease. They
// start from the real time, because the time to live of what a Store
// keeps outside the process runs on the clock of the store.
func smscNow() time.Time { return time.Now().Truncate(time.Second) }

func smscMessage(id, destination string, now time.Time) smsc.Message {
	return smsc.Message{
		ID:          id,
		Destination: destination,
		Deliver: &sms.Deliver{
			OriginatingAddress:     sms.InternationalNumber("+" + alice),
			ServiceCentreTimeStamp: now,
			UserData:               sms.UserData{Length: 5, Data: []byte{0xe8, 0x32, 0x9b, 0xfd, 0x06}},
		},
		Expires: now.Add(time.Hour),
	}
}

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent case calls them on goroutines of its
// own.

func enqueue(t *testing.T, s smsc.Store, m smsc.Message, now time.Time) {
	t.Helper()
	if err := s.Enqueue(context.Background(), m, now); err != nil {
		t.Errorf("Enqueue(%s): %v", m.ID, err)
	}
}

func claimDueDestination(t *testing.T, s smsc.Store, now time.Time, lease time.Duration) (smsc.Lease, bool) {
	t.Helper()
	l, ok, err := s.ClaimDue(context.Background(), now, lease)
	if err != nil {
		t.Errorf("ClaimDue: %v", err)
	}
	return l, ok
}

func claim(t *testing.T, s smsc.Store, destination string, now time.Time, lease time.Duration) (smsc.Lease, bool) {
	t.Helper()
	l, ok, err := s.Claim(context.Background(), destination, now, lease)
	if err != nil {
		t.Errorf("Claim(%s): %v", destination, err)
	}
	return l, ok
}

func queue(t *testing.T, s smsc.Store, l smsc.Lease) []smsc.Message {
	t.Helper()
	q, err := s.Queue(context.Background(), l)
	if err != nil {
		t.Errorf("Queue(%s): %v", l.Destination, err)
	}
	return q
}

func remove(t *testing.T, s smsc.Store, l smsc.Lease, id string) {
	t.Helper()
	if err := s.Remove(context.Background(), l, id); err != nil {
		t.Errorf("Remove(%s): %v", id, err)
	}
}

func release(t *testing.T, s smsc.Store, l smsc.Lease, next time.Time, attempts int) {
	t.Helper()
	if err := s.Release(context.Background(), l, next, attempts); err != nil {
		t.Errorf("Release(%s): %v", l.Destination, err)
	}
}

func alert(t *testing.T, s smsc.Store, destination string, now time.Time) bool {
	t.Helper()
	held, err := s.Alert(context.Background(), destination, now)
	if err != nil {
		t.Errorf("Alert(%s): %v", destination, err)
	}
	return held
}

func timeStamp(t *testing.T, s smsc.Store, destination string, now time.Time) time.Time {
	t.Helper()
	at, err := s.ServiceCentreTimeStamp(context.Background(), destination, now)
	if err != nil {
		t.Errorf("ServiceCentreTimeStamp(%s): %v", destination, err)
	}
	return at
}

// ids are the messages of a queue, in order.
func ids(messages []smsc.Message) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = m.ID
	}
	return out
}

// smscClaimsInOrderOfTime: a destination that had no queue becomes due
// when its first message arrives, and one that had a queue keeps its
// time, so that the destination waiting longest goes first. One instance
// enqueues and the other delivers.
func smscClaimsInOrderOfTime(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	enqueue(t, a, smscMessage("m1", bob, t0), t0.Add(2*time.Second))
	enqueue(t, a, smscMessage("m2", carol, t0), t0.Add(time.Second))
	// Behind m1, and bob keeps the time of m1.
	enqueue(t, a, smscMessage("m3", bob, t0), t0.Add(3*time.Second))

	if l, ok := claimDueDestination(t, b, t0, time.Minute); ok {
		t.Fatalf("claimed %s before its time", l.Destination)
	}
	now := t0.Add(5 * time.Second)
	first, ok := claimDueDestination(t, b, now, time.Minute)
	if !ok || first.Destination != carol {
		t.Fatalf("first claim = %+v, %v, want carol", first, ok)
	}
	second, ok := claimDueDestination(t, b, now, time.Minute)
	if !ok || second.Destination != bob {
		t.Fatalf("second claim = %+v, %v, want bob", second, ok)
	}
	if l, ok := claimDueDestination(t, b, now, time.Minute); ok {
		t.Fatalf("third claim = %+v, want none: both are leased", l)
	}
	if l, ok := claim(t, a, bob, now, time.Minute); ok {
		t.Fatalf("claim of bob during the lease of %s = %+v", second.Token, l)
	}
	if got := ids(queue(t, b, second)); len(got) != 2 || got[0] != "m1" || got[1] != "m3" {
		t.Errorf("queue of bob = %v, want m1 and m3 in that order", got)
	}
}

// smscQueueHoldsWhatWasEnqueued: the short message comes back as it went
// in, so that the SC writes the TPDU the sender meant, and the instance
// that enqueued it does not have to be the one that delivers it.
func smscQueueHoldsWhatWasEnqueued(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	m := smscMessage("m1", bob, t0)
	m.StatusReportRequest = &smsc.StatusReportRequest{
		Originator:       alice,
		MessageReference: 7,
		Recipient:        sms.Address{TypeOfNumber: sms.TONInternational, NumberingPlan: sms.NPIISDNTelephone, Digits: bob},
	}
	report := smsc.Message{
		ID:          "m2",
		Destination: alice,
		StatusReport: &sms.StatusReport{
			MessageReference:       7,
			RecipientAddress:       m.StatusReportRequest.Recipient,
			ServiceCentreTimeStamp: t0,
			DischargeTime:          t0.Add(time.Second),
			Status:                 sms.StatusReceivedBySME,
		},
		Expires: t0.Add(time.Hour),
	}
	enqueue(t, a, m, t0)
	enqueue(t, a, report, t0)

	for _, want := range []smsc.Message{m, report} {
		l, ok := claim(t, b, want.Destination, t0, time.Minute)
		if !ok {
			t.Fatalf("%s is not due", want.Destination)
		}
		got := queue(t, b, l)
		if len(got) != 1 || !sameJSON(t, got[0], want) {
			t.Errorf("queue of %s = %+v, want %+v", want.Destination, got, want)
		}
	}
}

// smscLeaseRunsOut: an instance that stopped never releases the
// destination it held, so another takes it over once the lease runs out,
// and the calls of the old lease are refused from then on.
func smscLeaseRunsOut(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	enqueue(t, a, smscMessage("m1", bob, t0), t0)
	old, ok := claim(t, a, bob, t0, time.Minute)
	if !ok {
		t.Fatal("bob is not due")
	}
	if l, ok := claim(t, b, bob, t0.Add(59*time.Second), time.Minute); ok {
		t.Fatalf("claimed bob during the lease: %+v", l)
	}
	taken, ok := claimDueDestination(t, b, t0.Add(time.Minute), time.Minute)
	if !ok || taken.Destination != bob || taken.Token == old.Token {
		t.Fatalf("claim after the lease = %+v, %v", taken, ok)
	}

	ctx := context.Background()
	_, queueErr := a.Queue(ctx, old)
	for name, err := range map[string]error{
		"Queue":   queueErr,
		"Remove":  a.Remove(ctx, old, "m1"),
		"Release": a.Release(ctx, old, t0, 0),
	} {
		if !errors.Is(err, smsc.ErrLeaseLost) {
			t.Errorf("%s with the old lease: %v, want ErrLeaseLost", name, err)
		}
	}
	if q := queue(t, b, taken); len(q) != 1 {
		t.Errorf("queue with the new lease = %+v, want the one message", q)
	}
}

// smscRelease: the destination comes back at the time of the release,
// with the attempts that it carried, and is forgotten once its queue is
// empty.
func smscRelease(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	enqueue(t, a, smscMessage("m1", bob, t0), t0)
	l, ok := claim(t, a, bob, t0, time.Minute)
	if !ok {
		t.Fatal("bob is not due")
	}
	release(t, a, l, t0.Add(30*time.Second), 3)

	if l, ok := claimDueDestination(t, b, t0.Add(29*time.Second), time.Minute); ok {
		t.Fatalf("claimed %s before the time of the release", l.Destination)
	}
	l, ok = claimDueDestination(t, b, t0.Add(30*time.Second), time.Minute)
	if !ok || l.Attempts != 3 {
		t.Fatalf("claim = %+v, %v, want the three attempts kept", l, ok)
	}
	remove(t, b, l, "m1")
	if q := queue(t, b, l); len(q) != 0 {
		t.Errorf("queue after Remove = %+v, want none", q)
	}
	release(t, b, l, t0.Add(time.Hour), 0)

	if alert(t, a, bob, t0) {
		t.Error("Alert found bob after its queue emptied")
	}
	if l, ok := claimDueDestination(t, a, t0.Add(2*time.Hour), time.Minute); ok {
		t.Errorf("claimed the forgotten %s: %+v", l.Destination, l)
	}
}

// smscAlert: the HSS says that a destination can take short messages
// again, which brings its next attempt forward. One that comes while
// another instance holds the destination counts once that instance has
// released it.
func smscAlert(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	if alert(t, a, bob, t0) {
		t.Error("Alert found bob, who has no queue")
	}
	enqueue(t, a, smscMessage("m1", bob, t0), t0)
	l, ok := claim(t, a, bob, t0, time.Minute)
	if !ok {
		t.Fatal("bob is not due")
	}
	release(t, a, l, t0.Add(time.Hour), 1)

	if !alert(t, b, bob, t0.Add(time.Minute)) {
		t.Fatal("Alert did not find bob")
	}
	l, ok = claim(t, b, bob, t0.Add(time.Minute), time.Minute)
	if !ok {
		t.Fatal("bob is not due after the alert")
	}
	if !alert(t, a, bob, t0.Add(70*time.Second)) {
		t.Fatal("Alert did not find the leased bob")
	}
	release(t, b, l, t0.Add(time.Hour), 2)
	if _, ok := claim(t, a, bob, t0.Add(70*time.Second), time.Minute); !ok {
		t.Error("the alert that came during the lease was lost")
	}
}

// smscServiceCentreTimeStamp: TS 23.040 6.2 wants the time stamps of the
// messages to one MS to differ, whichever instance took them.
func smscServiceCentreTimeStamp(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	at := t0.Add(400 * time.Millisecond)
	for i, want := range []time.Time{t0, t0.Add(time.Second), t0.Add(2 * time.Second)} {
		s := a
		if i == 1 {
			s = b
		}
		if got := timeStamp(t, s, bob, at); !got.Equal(want) {
			t.Errorf("time stamp %d for bob = %v, want %v", i, got, want)
		}
	}
	if got := timeStamp(t, b, carol, at); !got.Equal(t0) {
		t.Errorf("time stamp for carol = %v, want %v", got, t0)
	}
	later := t0.Add(10 * time.Second)
	if got := timeStamp(t, a, bob, later); !got.Equal(later) {
		t.Errorf("time stamp for bob later on = %v, want %v", got, later)
	}
}

// smscOneLeasePerDestination has many instances claim at once: TS 23.040
// 6.2 allows only one outstanding SMS-DELIVER to an MS, so of the
// instances that ask for one destination exactly one gets it.
func smscOneLeasePerDestination(t *testing.T, a, b smsc.Store) {
	t0 := smscNow()
	for _, destination := range []string{alice, bob, carol} {
		enqueue(t, a, smscMessage("m-"+destination, destination, t0), t0)
	}

	var mu sync.Mutex
	claimed := map[string]int{}
	parallel(20, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		l, ok, err := s.ClaimDue(context.Background(), t0, time.Minute)
		if err != nil {
			t.Errorf("ClaimDue: %v", err)
			return
		}
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		claimed[l.Destination]++
	})
	if len(claimed) != 3 {
		t.Errorf("claimed %v, want each of the three destinations once", claimed)
	}
	for destination, n := range claimed {
		if n != 1 {
			t.Errorf("%s was claimed %d times", destination, n)
		}
	}
}
