package storetest

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Test identities of PLMN 001/01 and fictional numbers.
const (
	impi      = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	impu      = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	tel       = "tel:+12025550101"
	otherIMPI = "001010000000002@ims.mnc001.mcc001.3gppnetwork.org"
	otherIMPU = "sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org"
)

// SCSCF runs the suite of scscf.Store.
func SCSCF(t *testing.T, newPair func(t *testing.T) (scscf.Store, scscf.Store)) {
	run(t, newPair, []testCase[scscf.Store]{
		{"Challenge", scscfChallenge},
		{"RegistrationByEachPublicIdentity", scscfRegistrationOf},
		{"PublicIdentitiesCompareTheUserPartWithCase", scscfUserPartWithCase},
		{"RegistrationLosesAPublicIdentity", scscfRegistrationLosesIdentity},
		{"DeleteRegistration", scscfDeleteRegistration},
		{"Subscriptions", scscfSubscriptions},
		{"ConcurrentSubscriptionsOfOneUser", scscfConcurrentSubscriptions},
		{"OriginalDialogUntilItsExpires", scscfOriginalDialog},
		{"UnregisteredProfile", scscfUnregisteredProfile},
		{"UnregisteredProfileEndsOnce", scscfUnregisteredProfileEndsOnce},
	})
}

func registration() scscf.Registration {
	return scscf.Registration{
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu, tel},
		Contact:          "sip:ue1@192.0.2.10:5060",
		ContactParams:    sip.Params{{Name: "+g.3gpp.smsip"}},
		Expires:          time.Now().Add(time.Hour).Round(time.Millisecond),
		Path:             []string{"<sip:term@pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>"},
		HSSHost:          "hss.ims.mnc001.mcc001.3gppnetwork.org",
		ContactID:        "c1",
		CallID:           "reg-1",
		CSeq:             2,
		InitialFilterCriteria: []ifc.InitialFilterCriteria{{
			Priority:          1,
			TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Method: "MESSAGE", Group: []int{0}}}},
			ApplicationServer: ifc.ApplicationServer{ServerName: "sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org"},
		}},
	}
}

func scscfChallenge(t *testing.T, a, b scscf.Store) {
	c := scscf.Challenge{PrivateIdentity: impi, PublicIdentity: impu, CallID: "reg-1", Realm: "ims.mnc001.mcc001.3gppnetwork.org",
		Nonce: "n1", Algorithm: sip.AlgorithmAKAv1MD5, XRES: []byte{1, 2, 3, 4}, HSSHost: "hss"}
	if _, ok := b.Challenge(impi); ok {
		t.Fatal("a challenge before PutChallenge")
	}
	a.PutChallenge(c)
	// A private identity is a NAI, which compares without case.
	got, ok := b.Challenge("001010000000001@IMS.MNC001.MCC001.3GPPNETWORK.ORG")
	if !ok || !sameJSON(t, got, c) {
		t.Fatalf("Challenge = %+v, %v; want %+v", got, ok, c)
	}
	b.DeleteChallenge(impi)
	if _, ok := a.Challenge(impi); ok {
		t.Error("the challenge is still there after DeleteChallenge")
	}
}

func scscfRegistrationOf(t *testing.T, a, b scscf.Store) {
	r := registration()
	a.PutRegistration(r)
	if got, ok := b.Registration(impi); !ok || !sameJSON(t, got, r) {
		t.Fatalf("Registration = %+v, %v; want %+v", got, ok, r)
	}
	// RegistrationOf compares as ims.SamePublicIdentity: the host without
	// case.
	for _, id := range []string{impu, tel, "sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"} {
		if got, ok := b.RegistrationOf(id); !ok || got.PrivateIdentity != impi {
			t.Errorf("RegistrationOf(%s) = %+v, %v", id, got, ok)
		}
	}
	if _, ok := b.RegistrationOf(otherIMPU); ok {
		t.Errorf("RegistrationOf(%s) found a registration", otherIMPU)
	}
}

// The user part of a SIP URI compares with case (RFC 3261 19.1.4), as
// the HSS compares it, so that the S-CSCF and the HSS never take two
// identities for one.
func scscfUserPartWithCase(t *testing.T, a, b scscf.Store) {
	r := registration()
	r.PublicIdentities = []string{"sip:Alice@ims.mnc001.mcc001.3gppnetwork.org"}
	a.PutRegistration(r)
	if got, ok := b.RegistrationOf("sip:Alice@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.PrivateIdentity != impi {
		t.Errorf("RegistrationOf with the host in capitals = %+v, %v", got, ok)
	}
	if _, ok := b.RegistrationOf("sip:alice@ims.mnc001.mcc001.3gppnetwork.org"); ok {
		t.Error("RegistrationOf found the registration of sip:Alice for sip:alice")
	}
}

func scscfRegistrationLosesIdentity(t *testing.T, a, b scscf.Store) {
	r := registration()
	a.PutRegistration(r)
	r.PublicIdentities = []string{impu}
	r.CSeq = 3
	b.PutRegistration(r)
	if _, ok := a.RegistrationOf(tel); ok {
		t.Error("RegistrationOf finds the registration by an identity it no longer has")
	}
	if got, ok := a.RegistrationOf(impu); !ok || got.CSeq != 3 {
		t.Errorf("RegistrationOf(%s) = %+v, %v; want the new registration", impu, got, ok)
	}
}

func scscfDeleteRegistration(t *testing.T, a, b scscf.Store) {
	a.PutRegistration(registration())
	b.DeleteRegistration(impi)
	if _, ok := a.Registration(impi); ok {
		t.Error("Registration after DeleteRegistration")
	}
	if _, ok := a.RegistrationOf(tel); ok {
		t.Error("RegistrationOf after DeleteRegistration")
	}
	// The identities are free for the next registration.
	a.PutRegistration(registration())
	if _, ok := b.RegistrationOf(tel); !ok {
		t.Error("RegistrationOf after a new registration")
	}
}

func subscription(callID, privateID string) scscf.Subscription {
	sub := scscf.Subscription{PrivateIdentity: privateID, ICID: "icid-" + callID, Version: 1}
	sub.Dialog = sip.Dialog{ID: sip.DialogID{CallID: callID, LocalTag: "l-" + callID, RemoteTag: "r-" + callID},
		State: sip.DialogConfirmed, LocalSeq: 1, RemoteSeq: 1, LocalURI: impu, RemoteURI: impu,
		RemoteTarget: "sip:ue1@192.0.2.10:5060", RouteSet: []string{"<sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org;lr>"}}
	sub.Event = "reg"
	sub.Contact = "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org"
	sub.Expires = time.Now().Add(time.Hour).Round(time.Millisecond)
	sub.State = sip.SubscriptionActive
	return sub
}

func callIDs(subs []scscf.Subscription) []string {
	var ids []string
	for _, s := range subs {
		ids = append(ids, s.Dialog.ID.CallID)
	}
	slices.Sort(ids)
	return ids
}

func scscfSubscriptions(t *testing.T, a, b scscf.Store) {
	// A Call-ID may hold ":" (RFC 3261 25.1).
	one, two, other := subscription("sub:1", impi), subscription("sub-2", impi), subscription("sub-3", otherIMPI)
	a.PutSubscription(one)
	b.PutSubscription(two)
	a.PutSubscription(other)
	if got, ok := b.Subscription(one.Dialog.ID); !ok || !sameJSON(t, got, one) {
		t.Fatalf("Subscription = %+v, %v; want %+v", got, ok, one)
	}
	if got := callIDs(b.SubscriptionsOf(impi)); !slices.Equal(got, []string{"sub-2", "sub:1"}) {
		t.Errorf("SubscriptionsOf = %v", got)
	}
	one.Version = 2
	b.PutSubscription(one)
	if got, _ := a.Subscription(one.Dialog.ID); got.Version != 2 {
		t.Errorf("Version after the second PutSubscription = %d", got.Version)
	}
	if got := len(a.SubscriptionsOf(impi)); got != 2 {
		t.Errorf("SubscriptionsOf after a rewrite has %d subscriptions, want 2", got)
	}
	a.DeleteSubscription(one.Dialog.ID)
	if _, ok := b.Subscription(one.Dialog.ID); ok {
		t.Error("Subscription after DeleteSubscription")
	}
	if got := callIDs(b.SubscriptionsOf(impi)); !slices.Equal(got, []string{"sub-2"}) {
		t.Errorf("SubscriptionsOf after DeleteSubscription = %v", got)
	}
	if got := b.SubscriptionsOf("nobody@ims.mnc001.mcc001.3gppnetwork.org"); len(got) != 0 {
		t.Errorf("SubscriptionsOf of nobody = %v", got)
	}
}

// scscfConcurrentSubscriptions has two instances add subscriptions to
// one registration at once, as the UE's and the P-CSCF's SUBSCRIBEs may
// reach two S-CSCF instances. None may be lost.
func scscfConcurrentSubscriptions(t *testing.T, a, b scscf.Store) {
	const n = 20
	parallel(2*n, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		s.PutSubscription(subscription(fmt.Sprintf("sub-%02d", i), impi))
	})
	if got := len(a.SubscriptionsOf(impi)); got != 2*n {
		t.Errorf("SubscriptionsOf has %d subscriptions, want %d", got, 2*n)
	}
}

func scscfOriginalDialog(t *testing.T, a, b scscf.Store) {
	d := scscf.OriginalDialog{ID: "od-1", ServedUser: impu, SessionCase: ifc.OriginatingRegistered, RegState: sip.RegStateRegistered,
		Criteria: registration().InitialFilterCriteria, Aliases: []string{impu, tel}, ICID: "icid-1",
		Expires: time.Now().Add(short).Round(time.Millisecond)}
	a.PutOriginalDialog(d)
	if got, ok := b.OriginalDialog("od-1"); !ok || !sameJSON(t, got, d) {
		t.Fatalf("OriginalDialog = %+v, %v; want %+v", got, ok, d)
	}
	d.Returned = true
	b.PutOriginalDialog(d)
	if got, _ := a.OriginalDialog("od-1"); !got.Returned {
		t.Error("Returned is not kept")
	}
	waitOut()
	if _, ok := a.OriginalDialog("od-1"); ok {
		t.Error("OriginalDialog after its Expires")
	}
}

func unregisteredProfile(lifetime time.Duration) scscf.UnregisteredProfile {
	return scscf.UnregisteredProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, tel},
		InitialFilterCriteria: registration().InitialFilterCriteria, HSSHost: "hss",
		Expires: time.Now().Add(lifetime).Round(time.Millisecond)}
}

func scscfUnregisteredProfile(t *testing.T, a, b scscf.Store) {
	p := unregisteredProfile(short)
	a.PutUnregisteredProfile(p)
	for _, id := range []string{impu, tel} {
		if got, ok := b.UnregisteredProfileOf(id); !ok || !sameJSON(t, got, p) {
			t.Errorf("UnregisteredProfileOf(%s) = %+v, %v", id, got, ok)
		}
	}
	// A new profile of the user without the tel URI.
	p2 := unregisteredProfile(short)
	p2.PublicIdentities = []string{impu}
	p2.Expires = p.Expires.Add(time.Millisecond)
	b.PutUnregisteredProfile(p2)
	if _, ok := a.UnregisteredProfileOf(tel); ok {
		t.Error("UnregisteredProfileOf finds the profile by an identity it no longer has")
	}
	if a.DeleteUnregisteredProfile(impi, p.Expires) {
		t.Error("DeleteUnregisteredProfile removed a profile that came after its timer")
	}
	if _, ok := b.UnregisteredProfileOf(impu); !ok {
		t.Error("the newer profile is gone")
	}
	waitOut()
	if _, ok := b.UnregisteredProfileOf(impu); ok {
		t.Error("UnregisteredProfileOf after its Expires")
	}
	// The timer of the profile still removes it after its Expires.
	if !a.DeleteUnregisteredProfile(impi, p2.Expires) {
		t.Error("DeleteUnregisteredProfile at the right Expires did nothing")
	}
	if a.DeleteUnregisteredProfile(impi, p2.Expires) {
		t.Error("a second DeleteUnregisteredProfile reported true")
	}
}

// scscfUnregisteredProfileEndsOnce has the timers of two instances end
// the same profile at once: only one of them may send the SAR.
func scscfUnregisteredProfileEndsOnce(t *testing.T, a, b scscf.Store) {
	for range 10 {
		p := unregisteredProfile(time.Hour)
		a.PutUnregisteredProfile(p)
		var wins atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if s.DeleteUnregisteredProfile(impi, p.Expires) {
				wins.Add(1)
			}
		})
		if got := wins.Load(); got != 1 {
			t.Fatalf("%d instances removed the profile, want 1", got)
		}
	}
}
