package storetest

import (
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
		{"Delivery", ipsmgwDelivery},
		{"OnlyOneTakesADelivery", ipsmgwOnlyOneTakes},
		{"RegSubscription", ipsmgwRegSubscription},
		{"RegSubscriptionReplacesTheOldOne", ipsmgwRegSubscriptionReplaces},
		{"PublicIdentitiesCompareTheUserPartWithCase", ipsmgwUserPartWithCase},
	})
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
		got = append(got, s.NextMessageReference(recipient, hourHence()))
	}
	if !slices.Equal(got, []uint8{0, 1, 2}) {
		t.Errorf("RP-MRs of one number written three ways = %v, want 0 1 2", got)
	}
	sip := "sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org"
	a.NextMessageReference(sip, hourHence())
	if mr := b.NextMessageReference("sip:001010000000002@IMS.mnc001.mcc001.3gppnetwork.org", hourHence()); mr != 1 {
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
		got = append(got, s.NextMessageReference(tel, hourHence()))
	}
	if !slices.Equal(got, []uint8{0, 1, 2}) {
		t.Errorf("RP-MRs = %v, want 0 1 2", got)
	}
	if mr := b.NextMessageReference("tel:+12025550102", hourHence()); mr != 0 {
		t.Errorf("first RP-MR of another recipient = %d", mr)
	}
	// TS 24.011 8.2.3 counts 0 to 255.
	for range 253 {
		a.NextMessageReference(tel, hourHence())
	}
	if mr := a.NextMessageReference(tel, hourHence()); mr != 0 {
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
			mr := s.NextMessageReference(tel, hourHence())
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
	a.NextMessageReference(tel, hourHence())
	if mr := b.NextMessageReference(tel, time.Now().Add(short)); mr != 1 {
		t.Fatalf("second RP-MR = %d, want 1", mr)
	}
	waitOut()
	if mr := a.NextMessageReference(tel, hourHence()); mr != 0 {
		t.Errorf("RP-MR after the counter expired = %d, want 0", mr)
	}
}

func delivery(callID string, lifetime time.Duration) ipsmgw.PendingDelivery {
	now := time.Now()
	return ipsmgw.PendingDelivery{CallID: callID, Reference: "ref-" + callID, Recipient: tel, MessageReference: 7,
		ReportDeadline: now.Add(lifetime / 2).Round(time.Millisecond), Expires: now.Add(lifetime).Round(time.Millisecond)}
}

func ipsmgwDelivery(t *testing.T, a, b ipsmgw.Store) {
	d := delivery("msg:1@ipsmgw", time.Hour)
	a.PutDelivery(d)
	if got, ok := b.Delivery(d.CallID); !ok || !sameJSON(t, got, d) {
		t.Fatalf("Delivery = %+v, %v; want %+v", got, ok, d)
	}
	if got, ok := b.TakeDelivery(d.CallID); !ok || !sameJSON(t, got, d) {
		t.Fatalf("TakeDelivery = %+v, %v", got, ok)
	}
	if _, ok := a.Delivery(d.CallID); ok {
		t.Error("Delivery after TakeDelivery")
	}
	brief := delivery("msg-2", short)
	a.PutDelivery(brief)
	waitOut()
	if _, ok := b.Delivery(brief.CallID); ok {
		t.Error("Delivery after its Expires")
	}
	if _, ok := b.TakeDelivery(brief.CallID); ok {
		t.Error("TakeDelivery after its Expires")
	}
}

// ipsmgwOnlyOneTakes: the report, a failed MESSAGE and the timer may
// end one delivery at once, on different instances.
func ipsmgwOnlyOneTakes(t *testing.T, a, b ipsmgw.Store) {
	for range 10 {
		d := delivery("msg-1", time.Hour)
		a.PutDelivery(d)
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := s.TakeDelivery(d.CallID); ok {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the delivery, want 1", got)
		}
	}
}

func regSubscription(callID string, lifetime time.Duration) ipsmgw.RegSubscription {
	return ipsmgw.RegSubscription{PublicIdentity: impu, MSISDN: "12025550101",
		Dialog: sip.Dialog{ID: sip.DialogID{CallID: callID, LocalTag: "l"}, LocalSeq: 1, LocalURI: "sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org",
			RemoteURI: impu, RemoteTarget: "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org", RouteSet: []string{"<sip:scscf;lr>"}},
		ICID: "icid-" + callID, Available: true, Expires: time.Now().Add(lifetime).Round(time.Millisecond)}
}

func ipsmgwRegSubscription(t *testing.T, a, b ipsmgw.Store) {
	sub := regSubscription("sub:1@ipsmgw", short)
	a.PutRegSubscription(sub)
	if got, ok := b.RegSubscription(sub.Dialog.ID.CallID); !ok || !sameJSON(t, got, sub) {
		t.Fatalf("RegSubscription = %+v, %v; want %+v", got, ok, sub)
	}
	// The host of a SIP URI compares without case (RFC 3261 19.1.4).
	if got, ok := b.RegSubscriptionOf("sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOf = %+v, %v", got, ok)
	}
	waitOut()
	if _, ok := b.RegSubscription(sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscription after its Expires")
	}
	if _, ok := b.RegSubscriptionOf(impu); ok {
		t.Error("RegSubscriptionOf after its Expires")
	}
	sub = regSubscription("sub-2", time.Hour)
	a.PutRegSubscription(sub)
	b.DeleteRegSubscription(sub.Dialog.ID.CallID)
	if _, ok := a.RegSubscription(sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscription after DeleteRegSubscription")
	}
	if _, ok := a.RegSubscriptionOf(impu); ok {
		t.Error("RegSubscriptionOf after DeleteRegSubscription")
	}
}

// ipsmgwRegSubscriptionReplaces: a new subscription to the same public
// identity takes the place of the old one (store.go).
func ipsmgwRegSubscriptionReplaces(t *testing.T, a, b ipsmgw.Store) {
	old, sub := regSubscription("sub-1", time.Hour), regSubscription("sub-2", time.Hour)
	a.PutRegSubscription(old)
	b.PutRegSubscription(sub)
	if _, ok := a.RegSubscription(old.Dialog.ID.CallID); ok {
		t.Error("the old subscription is still there")
	}
	if got, ok := a.RegSubscriptionOf(impu); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOf = %+v, %v; want the new one", got, ok)
	}
	// A refresh of the new one keeps it.
	sub.Dialog.LocalSeq = 2
	a.PutRegSubscription(sub)
	if got, ok := b.RegSubscription(sub.Dialog.ID.CallID); !ok || got.Dialog.LocalSeq != 2 {
		t.Errorf("RegSubscription after a refresh = %+v, %v", got, ok)
	}
}

// ipsmgwUserPartWithCase: the user part of a SIP URI compares with
// case and the host without (RFC 3261 19.1.4), so sip:Alice and
// sip:alice are two public identities with a subscription each.
func ipsmgwUserPartWithCase(t *testing.T, a, b ipsmgw.Store) {
	sub := regSubscription("sub-1", time.Hour)
	sub.PublicIdentity = "sip:Alice@ims.mnc001.mcc001.3gppnetwork.org"
	a.PutRegSubscription(sub)
	if got, ok := b.RegSubscriptionOf("sip:Alice@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscriptionOf with the host in capitals = %+v, %v", got, ok)
	}
	if _, ok := b.RegSubscriptionOf("sip:alice@ims.mnc001.mcc001.3gppnetwork.org"); ok {
		t.Error("RegSubscriptionOf found the subscription of sip:Alice for sip:alice")
	}
	other := regSubscription("sub-2", time.Hour)
	other.PublicIdentity = "sip:alice@ims.mnc001.mcc001.3gppnetwork.org"
	b.PutRegSubscription(other)
	if _, ok := a.RegSubscription(sub.Dialog.ID.CallID); !ok {
		t.Error("the subscription of sip:alice replaced that of sip:Alice")
	}
}
