package storetest

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// IPSMGW runs the suite of ipsmgw.Store.
func IPSMGW(t *testing.T, newPair func(t *testing.T) (ipsmgw.Store, ipsmgw.Store)) {
	run(t, newPair, []testCase[ipsmgw.Store]{
		{"MessageReferenceCountsPerRecipient", ipsmgwMessageReference},
		{"MessageReferenceCountsPerPublicIdentity", ipsmgwMessageReferencePerPublicIdentity},
		{"ConcurrentMessageReferences", ipsmgwConcurrentMessageReferences},
		{"MessageReferenceUntilItsExpiry", ipsmgwMessageReferenceExpires},
		{"WhatIsPutPastItsTimeIsGone", ipsmgwPastItsTime},
		{"Delivery", ipsmgwDelivery},
		{"OnlyOneTakesADelivery", ipsmgwOnlyOneTakes},
		{"ReportTimeoutIsClaimedOnce", ipsmgwReportTimeoutClaimedOnce},
		{"ClaimThatIsNotDoneRunsOut", ipsmgwClaimRunsOut},
		{"TakenDeliveryLeavesNoExpiry", ipsmgwTakenDeliveryLeavesNoExpiry},
		{"OutcomeReachesTheInstanceThatWaitsForIt", ipsmgwOutcome},
		{"OnlyOneTakesAnOutcome", ipsmgwOnlyOneTakesAnOutcome},
		{"NotReachableIsTakenOnce", ipsmgwNotReachable},
		{"RegSubscription", ipsmgwRegSubscription},
		{"RegSubscriptionOfIMSI", ipsmgwRegSubscriptionOfIMSI},
		{"RegSubscriptionOfMSISDN", ipsmgwRegSubscriptionOfMSISDN},
		{"RegSubscriptionReplacesTheOldOne", ipsmgwRegSubscriptionReplaces},
		{"PublicIdentitiesCompareTheUserPartWithCase", ipsmgwUserPartWithCase},
	})
}

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent cases call them on goroutines of
// their own.

func nextMessageReference(t *testing.T, s ipsmgw.Store, recipient string, expires time.Time) uint8 {
	t.Helper()
	mr, err := s.NextMessageReference(context.Background(), recipient, expires)
	if err != nil {
		t.Errorf("NextMessageReference(%s): %v", recipient, err)
	}
	return mr
}

func putDelivery(t *testing.T, s ipsmgw.Store, d ipsmgw.PendingDelivery) {
	t.Helper()
	if err := s.PutDelivery(context.Background(), d); err != nil {
		t.Errorf("PutDelivery(%s): %v", d.CallID, err)
	}
}

func getDelivery(t *testing.T, s ipsmgw.Store, callID string) (ipsmgw.PendingDelivery, bool) {
	t.Helper()
	d, ok, err := s.Delivery(context.Background(), callID)
	if err != nil {
		t.Errorf("Delivery(%s): %v", callID, err)
	}
	return d, ok
}

// claimDueDeliveries claims at most ten deliveries whose TR1N is due.
// The suite passes the now of its choosing, as the sweep of an IP-SM-GW
// instance passes its own, so that no case has to wait out a lease.
func claimDueDeliveries(t *testing.T, s ipsmgw.Store, now time.Time, lease time.Duration) []ipsmgw.Expiry {
	t.Helper()
	due, err := s.ClaimDue(context.Background(), now, lease, 10)
	if err != nil {
		t.Errorf("ClaimDue: %v", err)
	}
	return due
}

func doneDelivery(t *testing.T, s ipsmgw.Store, e ipsmgw.Expiry) {
	t.Helper()
	if err := s.Done(context.Background(), e); err != nil {
		t.Errorf("Done(%s): %v", e.CallID, err)
	}
}

func takeDelivery(t *testing.T, s ipsmgw.Store, callID string) (ipsmgw.PendingDelivery, bool) {
	t.Helper()
	d, ok, err := s.TakeDelivery(context.Background(), callID)
	if err != nil {
		t.Errorf("TakeDelivery(%s): %v", callID, err)
	}
	return d, ok
}

func putRegSubscription(t *testing.T, s ipsmgw.Store, sub ipsmgw.RegSubscription) {
	t.Helper()
	if err := s.PutRegSubscription(context.Background(), sub); err != nil {
		t.Errorf("PutRegSubscription(%s): %v", sub.Dialog.ID.CallID, err)
	}
}

func getRegSubscription(t *testing.T, s ipsmgw.Store, callID string) (ipsmgw.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.RegSubscription(context.Background(), callID)
	if err != nil {
		t.Errorf("RegSubscription(%s): %v", callID, err)
	}
	return sub, ok
}

func regSubscriptionOf(t *testing.T, s ipsmgw.Store, publicID string) (ipsmgw.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.RegSubscriptionOf(context.Background(), publicID)
	if err != nil {
		t.Errorf("RegSubscriptionOf(%s): %v", publicID, err)
	}
	return sub, ok
}

func regSubscriptionOfIMSI(t *testing.T, s ipsmgw.Store, imsi string) (ipsmgw.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.RegSubscriptionOfIMSI(context.Background(), imsi)
	if err != nil {
		t.Errorf("RegSubscriptionOfIMSI(%s): %v", imsi, err)
	}
	return sub, ok
}

func regSubscriptionOfMSISDN(t *testing.T, s ipsmgw.Store, msisdn string) (ipsmgw.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.RegSubscriptionOfMSISDN(context.Background(), msisdn)
	if err != nil {
		t.Errorf("RegSubscriptionOfMSISDN(%s): %v", msisdn, err)
	}
	return sub, ok
}

func deleteRegSubscription(t *testing.T, s ipsmgw.Store, callID string) {
	t.Helper()
	if err := s.DeleteRegSubscription(context.Background(), callID); err != nil {
		t.Errorf("DeleteRegSubscription(%s): %v", callID, err)
	}
}

// ipsmgwMessageReferencePerPublicIdentity writes one recipient in
// several ways: a tel URI with visual separators names the same number
// (RFC 3966 4), and a SIP URI compares its host without case (RFC 3261
// 19.1.4). Each way must count on from the others, or two deliveries
// waiting for their reports could hold the same RP-MR.
func ipsmgwMessageReferencePerPublicIdentity(t *testing.T, a, b ipsmgw.Store) {
	var got []uint8
	for i, recipient := range []string{tel, "tel:+1-202-555-0101", "tel:+1.202.555.0101"} {
		s := a
		if i == 1 {
			s = b
		}
		got = append(got, nextMessageReference(t, s, recipient, hourHence()))
	}
	if !slices.Equal(got, []uint8{0, 1, 2}) {
		t.Errorf("RP-MRs of one number written three ways = %v, want 0 1 2", got)
	}
	sip := "sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org"
	nextMessageReference(t, a, sip, hourHence())
	if mr := nextMessageReference(t, b, "sip:001010000000002@IMS.mnc001.mcc001.3gppnetwork.org", hourHence()); mr != 1 {
		t.Errorf("RP-MR of a SIP URI with its host in upper case = %d, want 1", mr)
	}
}

func ipsmgwMessageReference(t *testing.T, a, b ipsmgw.Store) {
	var got []uint8
	for i := range 3 {
		s := a
		if i == 1 {
			s = b
		}
		got = append(got, nextMessageReference(t, s, tel, hourHence()))
	}
	if !slices.Equal(got, []uint8{0, 1, 2}) {
		t.Errorf("RP-MRs = %v, want 0 1 2", got)
	}
	if mr := nextMessageReference(t, b, "tel:+12025550102", hourHence()); mr != 0 {
		t.Errorf("first RP-MR of another recipient = %d", mr)
	}
	// TS 24.011 8.2.3 counts 0 to 255.
	for range 253 {
		nextMessageReference(t, a, tel, hourHence())
	}
	if mr := nextMessageReference(t, a, tel, hourHence()); mr != 0 {
		t.Errorf("RP-MR after 255 = %d, want 0", mr)
	}
}

// ipsmgwConcurrentMessageReferences has two instances send to one
// recipient at once: no RP-MR may be given twice.
func ipsmgwConcurrentMessageReferences(t *testing.T, a, b ipsmgw.Store) {
	var mu sync.Mutex
	seen := map[uint8]bool{}
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		for range 50 {
			mr := nextMessageReference(t, s, tel, hourHence())
			mu.Lock()
			if seen[mr] {
				t.Errorf("RP-MR %d given twice", mr)
			}
			seen[mr] = true
			mu.Unlock()
		}
	})
}

// hourHence is the expiry of a counter of RP-MR that a case does not
// wait out.
func hourHence() time.Time { return time.Now().Add(time.Hour) }

// ipsmgwMessageReferenceExpires: the counter is kept until the expiry
// that the last NextMessageReference gave it, and starts at 0 again
// after it.
func ipsmgwMessageReferenceExpires(t *testing.T, a, b ipsmgw.Store) {
	nextMessageReference(t, a, tel, hourHence())
	if mr := nextMessageReference(t, b, tel, time.Now().Add(short)); mr != 1 {
		t.Fatalf("second RP-MR = %d, want 1", mr)
	}
	waitOut()
	if mr := nextMessageReference(t, a, tel, hourHence()); mr != 0 {
		t.Errorf("RP-MR after the counter expired = %d, want 0", mr)
	}
}

// ipsmgwPastItsTime: the Store keeps nothing past the time that the
// IP-SM-GW wrote into it, even what it is given when that time has
// passed already.
func ipsmgwPastItsTime(t *testing.T, a, b ipsmgw.Store) {
	past := time.Now().Add(-time.Second)
	nextMessageReference(t, a, tel, past)
	if mr := nextMessageReference(t, b, tel, hourHence()); mr != 0 {
		t.Errorf("RP-MR after a counter kept until a time that had passed = %d, want 0", mr)
	}
	d := delivery("msg-1", time.Hour)
	d.Expires = past
	putDelivery(t, a, d)
	if _, ok := getDelivery(t, b, d.CallID); ok {
		t.Error("Delivery of one put after its Expires")
	}
	if _, ok := takeDelivery(t, b, d.CallID); ok {
		t.Error("TakeDelivery of one put after its Expires")
	}
	sub := regSubscription("sub-1", time.Hour)
	sub.Expires = past
	putRegSubscription(t, a, sub)
	if _, ok := getRegSubscription(t, b, sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscription of one put after its Expires")
	}
	if _, ok := regSubscriptionOf(t, b, impu); ok {
		t.Error("RegSubscriptionOf of one put after its Expires")
	}
}

func delivery(callID string, lifetime time.Duration) ipsmgw.PendingDelivery {
	now := time.Now()
	return ipsmgw.PendingDelivery{CallID: callID, Reference: "ref-" + callID, Recipient: tel, MessageReference: 7,
		ReportDeadline: now.Add(lifetime / 2).Round(time.Millisecond), Expires: now.Add(lifetime).Round(time.Millisecond)}
}

func ipsmgwDelivery(t *testing.T, a, b ipsmgw.Store) {
	d := delivery("msg:1@ipsmgw", time.Hour)
	putDelivery(t, a, d)
	if got, ok := getDelivery(t, b, d.CallID); !ok || !sameJSON(t, got, d) {
		t.Fatalf("Delivery = %+v, %v; want %+v", got, ok, d)
	}
	if got, ok := takeDelivery(t, b, d.CallID); !ok || !sameJSON(t, got, d) {
		t.Fatalf("TakeDelivery = %+v, %v", got, ok)
	}
	if _, ok := getDelivery(t, a, d.CallID); ok {
		t.Error("Delivery after TakeDelivery")
	}
	brief := delivery("msg-2", short)
	putDelivery(t, a, brief)
	waitOut()
	if _, ok := getDelivery(t, b, brief.CallID); ok {
		t.Error("Delivery after its Expires")
	}
	if _, ok := takeDelivery(t, b, brief.CallID); ok {
		t.Error("TakeDelivery after its Expires")
	}
}

// ipsmgwReportTimeoutClaimedOnce: the TR1N of a delivery that is due
// goes to one instance, and Done ends it for good.
func ipsmgwReportTimeoutClaimedOnce(t *testing.T, a, b ipsmgw.Store) {
	d := delivery("msg-1", time.Hour)
	putDelivery(t, a, d)
	if got := claimDueDeliveries(t, b, d.ReportDeadline.Add(-time.Millisecond), time.Minute); len(got) != 0 {
		t.Fatalf("claimed %+v before TR1N ran out", got)
	}
	var mu sync.Mutex
	var claimed []ipsmgw.Expiry
	parallel(4, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		got := claimDueDeliveries(t, s, d.ReportDeadline, time.Minute)
		mu.Lock()
		defer mu.Unlock()
		claimed = append(claimed, got...)
	})
	if len(claimed) != 1 {
		t.Fatalf("%d instances claimed TR1N (%+v), want 1", len(claimed), claimed)
	}
	if e := claimed[0]; e.CallID != d.CallID || !e.Until.After(d.ReportDeadline) {
		t.Errorf("the claim = %+v, want the delivery %s until after its ReportDeadline", e, d.CallID)
	}
	doneDelivery(t, b, claimed[0])
	if got := claimDueDeliveries(t, a, d.ReportDeadline.Add(time.Hour), time.Minute); len(got) != 0 {
		t.Errorf("claimed %+v after Done", got)
	}
}

// ipsmgwClaimRunsOut: the instance that claimed a TR1N stops before it
// has ended the delivery, so no Done comes; once its claim runs out
// another instance takes the expiry over, and the late Done of the first
// one takes nothing away from it.
func ipsmgwClaimRunsOut(t *testing.T, a, b ipsmgw.Store) {
	d := delivery("msg-1", time.Hour)
	putDelivery(t, a, d)
	first := claimDueDeliveries(t, a, d.ReportDeadline, time.Minute)
	if len(first) != 1 {
		t.Fatalf("claims = %+v, want the delivery once", first)
	}
	if got := claimDueDeliveries(t, b, first[0].Until.Add(-time.Millisecond), time.Minute); len(got) != 0 {
		t.Fatalf("claimed %+v while the other instance held the claim", got)
	}
	second := claimDueDeliveries(t, b, first[0].Until, time.Minute)
	if len(second) != 1 {
		t.Fatalf("claims after the lease ran out = %+v, want the delivery once", second)
	}
	doneDelivery(t, a, first[0])
	if got := claimDueDeliveries(t, a, second[0].Until.Add(-time.Millisecond), time.Minute); len(got) != 0 {
		t.Errorf("the Done of the lost claim gave the expiry away again: %+v", got)
	}
	doneDelivery(t, b, second[0])
	if got := claimDueDeliveries(t, a, second[0].Until, time.Minute); len(got) != 0 {
		t.Errorf("claimed %+v after Done", got)
	}
}

// ipsmgwTakenDeliveryLeavesNoExpiry: the TR1N of a delivery that a
// report ended is gone with it, so no instance is left to end a delivery
// that is not there.
func ipsmgwTakenDeliveryLeavesNoExpiry(t *testing.T, a, b ipsmgw.Store) {
	d := delivery("msg-1", time.Hour)
	putDelivery(t, a, d)
	if _, ok := takeDelivery(t, b, d.CallID); !ok {
		t.Fatal("TakeDelivery did not get the delivery")
	}
	if got := claimDueDeliveries(t, a, d.ReportDeadline, time.Minute); len(got) != 0 {
		t.Errorf("TR1N of a delivery that was taken = %+v, want nothing", got)
	}
}

// ipsmgwOnlyOneTakes: the report, a failed MESSAGE and TR1N may
// end one delivery at once, on different instances.
func ipsmgwOnlyOneTakes(t *testing.T, a, b ipsmgw.Store) {
	for range 10 {
		d := delivery("msg-1", time.Hour)
		putDelivery(t, a, d)
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := takeDelivery(t, s, d.CallID); ok {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the delivery, want 1", got)
		}
	}
}

// ipsmgwRegSubscriptionOfIMSI: a TFR names the recipient by IMSI, so
// the Store finds the subscription by it (store.go). The index goes
// with the subscription, and a new subscription of the same user takes
// it over.
func ipsmgwRegSubscriptionOfIMSI(t *testing.T, a, b ipsmgw.Store) {
	sub := regSubscription("sub-1", time.Hour)
	putRegSubscription(t, a, sub)
	if got, ok := regSubscriptionOfIMSI(t, b, imsi); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOfIMSI = %+v, %v", got, ok)
	}
	if _, ok := regSubscriptionOfIMSI(t, b, "001010000000009"); ok {
		t.Error("RegSubscriptionOfIMSI found a subscription for an IMSI no user has")
	}
	// A subscription without an IMSI is in no index item, so an empty
	// IMSI must not find it.
	if _, ok := regSubscriptionOfIMSI(t, b, ""); ok {
		t.Error("RegSubscriptionOfIMSI found a subscription for an empty IMSI")
	}

	next := regSubscription("sub-2", time.Hour)
	putRegSubscription(t, b, next)
	if got, ok := regSubscriptionOfIMSI(t, a, imsi); !ok || got.Dialog.ID != next.Dialog.ID {
		t.Errorf("RegSubscriptionOfIMSI after a new subscription = %+v, %v; want the new one", got, ok)
	}

	deleteRegSubscription(t, a, next.Dialog.ID.CallID)
	if _, ok := regSubscriptionOfIMSI(t, b, imsi); ok {
		t.Error("RegSubscriptionOfIMSI after DeleteRegSubscription")
	}

	// The subscription goes at its Expires, and the index item with it.
	brief := regSubscription("sub-3", short)
	putRegSubscription(t, a, brief)
	waitOut()
	if _, ok := regSubscriptionOfIMSI(t, b, imsi); ok {
		t.Error("RegSubscriptionOfIMSI after the subscription expired")
	}
}

func ipsmgwRegSubscriptionOfMSISDN(t *testing.T, a, b ipsmgw.Store) {
	sub := regSubscription("sub-1", time.Hour)
	putRegSubscription(t, a, sub)
	if got, ok := regSubscriptionOfMSISDN(t, b, sub.MSISDN); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOfMSISDN = %+v, %v", got, ok)
	}
	if _, ok := regSubscriptionOfMSISDN(t, b, "12025550199"); ok {
		t.Error("RegSubscriptionOfMSISDN found a subscription for a number no user has")
	}
	if _, ok := regSubscriptionOfMSISDN(t, b, ""); ok {
		t.Error("RegSubscriptionOfMSISDN found a subscription for an empty MSISDN")
	}

	next := regSubscription("sub-2", time.Hour)
	putRegSubscription(t, b, next)
	if got, ok := regSubscriptionOfMSISDN(t, a, sub.MSISDN); !ok || got.Dialog.ID != next.Dialog.ID {
		t.Errorf("RegSubscriptionOfMSISDN after a new subscription = %+v, %v; want the new one", got, ok)
	}

	deleteRegSubscription(t, a, next.Dialog.ID.CallID)
	if _, ok := regSubscriptionOfMSISDN(t, b, sub.MSISDN); ok {
		t.Error("RegSubscriptionOfMSISDN after DeleteRegSubscription")
	}

	brief := regSubscription("sub-3", short)
	putRegSubscription(t, a, brief)
	waitOut()
	if _, ok := regSubscriptionOfMSISDN(t, b, sub.MSISDN); ok {
		t.Error("RegSubscriptionOfMSISDN after the subscription expired")
	}
}

// imsi is the IMSI of the user of impu, as the IP-SM-GW read it over Sh
// (TS 23.003 2.2).
const imsi = "001010000000001"

func putOutcome(t *testing.T, s ipsmgw.Store, reference string, o ipsmgw.Outcome, expires time.Time) {
	t.Helper()
	if err := s.PutOutcome(context.Background(), reference, o, expires); err != nil {
		t.Errorf("PutOutcome(%s): %v", reference, err)
	}
}

func takeOutcome(t *testing.T, s ipsmgw.Store, reference string) (ipsmgw.Outcome, bool) {
	t.Helper()
	o, ok, err := s.TakeOutcome(context.Background(), reference)
	if err != nil {
		t.Errorf("TakeOutcome(%s): %v", reference, err)
	}
	return o, ok
}

// ipsmgwOutcome: the instance that ends a delivery puts its outcome in
// the Store, and the instance that holds the request which brought the
// delivery takes it there.
func ipsmgwOutcome(t *testing.T, a, b ipsmgw.Store) {
	putOutcome(t, a, "ref:1@sc", ipsmgw.MemoryCapacityExceeded, hourHence())
	if got, ok := takeOutcome(t, b, "ref:1@sc"); !ok || got != ipsmgw.MemoryCapacityExceeded {
		t.Errorf("TakeOutcome = %v, %v; want memory capacity exceeded", got, ok)
	}
	if _, ok := takeOutcome(t, a, "ref:1@sc"); ok {
		t.Error("TakeOutcome of an outcome that was taken")
	}
	if _, ok := takeOutcome(t, b, "ref-none"); ok {
		t.Error("TakeOutcome of a reference no delivery has")
	}
	// Delivered is the zero Outcome, which must still come back as an
	// outcome that is there.
	putOutcome(t, a, "ref-2", ipsmgw.Delivered, hourHence())
	if got, ok := takeOutcome(t, b, "ref-2"); !ok || got != ipsmgw.Delivered {
		t.Errorf("TakeOutcome of a delivered short message = %v, %v", got, ok)
	}
	putOutcome(t, a, "ref-3", ipsmgw.Delivered, time.Now().Add(short))
	waitOut()
	if _, ok := takeOutcome(t, b, "ref-3"); ok {
		t.Error("TakeOutcome after the outcome expired")
	}
}

// ipsmgwOnlyOneTakesAnOutcome: a request is answered once, so of
// several instances that read one outcome only one gets it.
func ipsmgwOnlyOneTakesAnOutcome(t *testing.T, a, b ipsmgw.Store) {
	for range 10 {
		putOutcome(t, a, "ref-1", ipsmgw.AbsentSubscriber, hourHence())
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := takeOutcome(t, s, "ref-1"); ok {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the outcome, want 1", got)
		}
	}
}

// ipsmgwNotReachable: the IP-SM-GW's own UNRI is set on the instance
// whose delivery failed and found on every other one, and of several
// instances that find the user reachable again only one takes it, so
// that the HSS hears of the return once. The flag is that of the public
// identity, however the user is written.
func ipsmgwNotReachable(t *testing.T, a, b ipsmgw.Store) {
	notReachable := func(s ipsmgw.Store, publicID string) bool {
		t.Helper()
		set, err := s.TakeNotReachable(context.Background(), publicID)
		if err != nil {
			t.Errorf("TakeNotReachable(%s): %v", publicID, err)
		}
		return set
	}
	putNotReachable := func(s ipsmgw.Store, publicID string, expires time.Time) {
		t.Helper()
		if err := s.PutNotReachable(context.Background(), publicID, expires); err != nil {
			t.Errorf("PutNotReachable(%s): %v", publicID, err)
		}
	}
	if notReachable(a, impu) {
		t.Error("the flag of a user no delivery failed for is set")
	}
	putNotReachable(a, impu, hourHence())
	if !notReachable(b, "sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org") {
		t.Error("the other instance does not find the flag, or compares the host with case")
	}
	if notReachable(a, impu) {
		t.Error("the flag is still set after it was taken")
	}

	for range 10 {
		putNotReachable(a, impu, hourHence())
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if notReachable(s, impu) {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the flag, want 1", got)
		}
	}

	putNotReachable(a, impu, time.Now().Add(short))
	waitOut()
	if notReachable(b, impu) {
		t.Error("the flag is set after it expired")
	}
}

func regSubscription(callID string, lifetime time.Duration) ipsmgw.RegSubscription {
	return ipsmgw.RegSubscription{PublicIdentity: impu, MSISDN: "12025550101", IMSI: imsi,
		Dialog: sip.Dialog{ID: sip.DialogID{CallID: callID, LocalTag: "l"}, LocalSeq: 1, LocalURI: "sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org",
			RemoteURI: impu, RemoteTarget: "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org", RouteSet: []string{"<sip:scscf;lr>"}},
		ICID: "icid-" + callID, Available: true, Expires: time.Now().Add(lifetime).Round(time.Millisecond)}
}

func ipsmgwRegSubscription(t *testing.T, a, b ipsmgw.Store) {
	sub := regSubscription("sub:1@ipsmgw", short)
	putRegSubscription(t, a, sub)
	if got, ok := getRegSubscription(t, b, sub.Dialog.ID.CallID); !ok || !sameJSON(t, got, sub) {
		t.Fatalf("RegSubscription = %+v, %v; want %+v", got, ok, sub)
	}
	// The host of a SIP URI compares without case (RFC 3261 19.1.4).
	if got, ok := regSubscriptionOf(t, b, "sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOf = %+v, %v", got, ok)
	}
	waitOut()
	if _, ok := getRegSubscription(t, b, sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscription after its Expires")
	}
	if _, ok := regSubscriptionOf(t, b, impu); ok {
		t.Error("RegSubscriptionOf after its Expires")
	}
	sub = regSubscription("sub-2", time.Hour)
	putRegSubscription(t, a, sub)
	deleteRegSubscription(t, b, sub.Dialog.ID.CallID)
	if _, ok := getRegSubscription(t, a, sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscription after DeleteRegSubscription")
	}
	if _, ok := regSubscriptionOf(t, a, impu); ok {
		t.Error("RegSubscriptionOf after DeleteRegSubscription")
	}
}

// ipsmgwRegSubscriptionReplaces: a new subscription to the same public
// identity takes the place of the old one (store.go).
func ipsmgwRegSubscriptionReplaces(t *testing.T, a, b ipsmgw.Store) {
	old, sub := regSubscription("sub-1", time.Hour), regSubscription("sub-2", time.Hour)
	putRegSubscription(t, a, old)
	putRegSubscription(t, b, sub)
	if _, ok := getRegSubscription(t, a, old.Dialog.ID.CallID); ok {
		t.Error("the old subscription is still there")
	}
	if got, ok := regSubscriptionOf(t, a, impu); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOf = %+v, %v; want the new one", got, ok)
	}
	// A refresh of the new one keeps it.
	sub.Dialog.LocalSeq = 2
	putRegSubscription(t, a, sub)
	if got, ok := getRegSubscription(t, b, sub.Dialog.ID.CallID); !ok || got.Dialog.LocalSeq != 2 {
		t.Errorf("RegSubscription after a refresh = %+v, %v", got, ok)
	}
}

// ipsmgwUserPartWithCase: the user part of a SIP URI compares with
// case and the host without (RFC 3261 19.1.4), so sip:Alice and
// sip:alice are two public identities with a subscription each.
func ipsmgwUserPartWithCase(t *testing.T, a, b ipsmgw.Store) {
	sub := regSubscription("sub-1", time.Hour)
	sub.PublicIdentity = "sip:Alice@ims.mnc001.mcc001.3gppnetwork.org"
	putRegSubscription(t, a, sub)
	if got, ok := regSubscriptionOf(t, b, "sip:Alice@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOf with the host in capitals = %+v, %v", got, ok)
	}
	if _, ok := regSubscriptionOf(t, b, "sip:alice@ims.mnc001.mcc001.3gppnetwork.org"); ok {
		t.Error("RegSubscriptionOf found the subscription of sip:Alice for sip:alice")
	}
	other := regSubscription("sub-2", time.Hour)
	other.PublicIdentity = "sip:alice@ims.mnc001.mcc001.3gppnetwork.org"
	putRegSubscription(t, b, other)
	if _, ok := getRegSubscription(t, a, sub.Dialog.ID.CallID); !ok {
		t.Error("the subscription of sip:alice replaced that of sip:Alice")
	}
}
