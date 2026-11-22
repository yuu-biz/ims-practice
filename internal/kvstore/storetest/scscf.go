package storetest

import (
	"context"
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
		{"ChallengeUntilItsKeepUntil", scscfChallengeKeptUntil},
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

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent cases call them on goroutines of
// their own.

func getChallenge(t *testing.T, s scscf.Store, privateID string) (scscf.Challenge, bool) {
	t.Helper()
	c, ok, err := s.Challenge(context.Background(), privateID)
	if err != nil {
		t.Errorf("Challenge(%s): %v", privateID, err)
	}
	return c, ok
}

func putChallenge(t *testing.T, s scscf.Store, c scscf.Challenge) {
	t.Helper()
	if err := s.PutChallenge(context.Background(), c); err != nil {
		t.Errorf("PutChallenge(%s): %v", c.PrivateIdentity, err)
	}
}

func deleteChallenge(t *testing.T, s scscf.Store, privateID string) {
	t.Helper()
	if err := s.DeleteChallenge(context.Background(), privateID); err != nil {
		t.Errorf("DeleteChallenge(%s): %v", privateID, err)
	}
}

func getRegistration(t *testing.T, s scscf.Store, privateID string) (scscf.Registration, bool) {
	t.Helper()
	r, ok, err := s.Registration(context.Background(), privateID)
	if err != nil {
		t.Errorf("Registration(%s): %v", privateID, err)
	}
	return r, ok
}

func registrationOf(t *testing.T, s scscf.Store, publicID string) (scscf.Registration, bool) {
	t.Helper()
	r, ok, err := s.RegistrationOf(context.Background(), publicID)
	if err != nil {
		t.Errorf("RegistrationOf(%s): %v", publicID, err)
	}
	return r, ok
}

func putRegistration(t *testing.T, s scscf.Store, r scscf.Registration) {
	t.Helper()
	if err := s.PutRegistration(context.Background(), r); err != nil {
		t.Errorf("PutRegistration(%s): %v", r.PrivateIdentity, err)
	}
}

func deleteRegistration(t *testing.T, s scscf.Store, privateID string) {
	t.Helper()
	if err := s.DeleteRegistration(context.Background(), privateID); err != nil {
		t.Errorf("DeleteRegistration(%s): %v", privateID, err)
	}
}

func getSubscription(t *testing.T, s scscf.Store, id sip.DialogID) (scscf.Subscription, bool) {
	t.Helper()
	sub, ok, err := s.Subscription(context.Background(), id)
	if err != nil {
		t.Errorf("Subscription(%+v): %v", id, err)
	}
	return sub, ok
}

func subscriptionsOf(t *testing.T, s scscf.Store, privateID string) []scscf.Subscription {
	t.Helper()
	subs, err := s.SubscriptionsOf(context.Background(), privateID)
	if err != nil {
		t.Errorf("SubscriptionsOf(%s): %v", privateID, err)
	}
	return subs
}

func putSubscription(t *testing.T, s scscf.Store, sub scscf.Subscription) {
	t.Helper()
	if err := s.PutSubscription(context.Background(), sub); err != nil {
		t.Errorf("PutSubscription(%+v): %v", sub.Dialog.ID, err)
	}
}

func deleteSubscription(t *testing.T, s scscf.Store, id sip.DialogID) {
	t.Helper()
	if err := s.DeleteSubscription(context.Background(), id); err != nil {
		t.Errorf("DeleteSubscription(%+v): %v", id, err)
	}
}

func getOriginalDialog(t *testing.T, s scscf.Store, id string) (scscf.OriginalDialog, bool) {
	t.Helper()
	d, ok, err := s.OriginalDialog(context.Background(), id)
	if err != nil {
		t.Errorf("OriginalDialog(%s): %v", id, err)
	}
	return d, ok
}

func putOriginalDialog(t *testing.T, s scscf.Store, d scscf.OriginalDialog) {
	t.Helper()
	if err := s.PutOriginalDialog(context.Background(), d); err != nil {
		t.Errorf("PutOriginalDialog(%s): %v", d.ID, err)
	}
}

func unregisteredProfileOf(t *testing.T, s scscf.Store, publicID string) (scscf.UnregisteredProfile, bool) {
	t.Helper()
	p, ok, err := s.UnregisteredProfileOf(context.Background(), publicID)
	if err != nil {
		t.Errorf("UnregisteredProfileOf(%s): %v", publicID, err)
	}
	return p, ok
}

func putUnregisteredProfile(t *testing.T, s scscf.Store, p scscf.UnregisteredProfile) {
	t.Helper()
	if err := s.PutUnregisteredProfile(context.Background(), p); err != nil {
		t.Errorf("PutUnregisteredProfile(%s): %v", p.PrivateIdentity, err)
	}
}

func deleteUnregisteredProfile(t *testing.T, s scscf.Store, privateID string, expires time.Time) bool {
	t.Helper()
	deleted, err := s.DeleteUnregisteredProfile(context.Background(), privateID, expires)
	if err != nil {
		t.Errorf("DeleteUnregisteredProfile(%s): %v", privateID, err)
	}
	return deleted
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
	expires := time.Now().Add(time.Minute).Round(time.Millisecond)
	c := scscf.Challenge{PrivateIdentity: impi, PublicIdentity: impu, CallID: "reg-1", Realm: "ims.mnc001.mcc001.3gppnetwork.org",
		Nonce: "n1", Algorithm: sip.AlgorithmAKAv1MD5, XRES: []byte{1, 2, 3, 4}, HSSHost: "hss", Expires: expires, KeepUntil: expires.Add(time.Minute)}
	if _, ok := getChallenge(t, b, impi); ok {
		t.Fatal("a challenge before PutChallenge")
	}
	putChallenge(t, a, c)
	// A private identity is a NAI, which compares without case.
	got, ok := getChallenge(t, b, "001010000000001@IMS.MNC001.MCC001.3GPPNETWORK.ORG")
	if !ok || !sameJSON(t, got, c) {
		t.Fatalf("Challenge = %+v, %v; want %+v", got, ok, c)
	}
	deleteChallenge(t, b, impi)
	if _, ok := getChallenge(t, a, impi); ok {
		t.Error("the challenge is still there after DeleteChallenge")
	}
}

// scscfChallengeKeptUntil: the Store keeps a challenge until the
// KeepUntil that the S-CSCF wrote into it, also one put after that
// time. A challenge that replaces it is kept until its own.
func scscfChallengeKeptUntil(t *testing.T, a, b scscf.Store) {
	c := scscf.Challenge{PrivateIdentity: impi, Nonce: "n1", Expires: time.Now(), KeepUntil: time.Now().Add(short).Round(time.Millisecond)}
	putChallenge(t, a, c)
	if _, ok := getChallenge(t, b, impi); !ok {
		t.Fatal("Challenge before its KeepUntil")
	}
	past := scscf.Challenge{PrivateIdentity: otherIMPI, Nonce: "n2", Expires: time.Now().Add(-2 * time.Second), KeepUntil: time.Now().Add(-time.Second)}
	putChallenge(t, b, past)
	waitOut()
	if _, ok := getChallenge(t, b, impi); ok {
		t.Error("Challenge after its KeepUntil")
	}
	if _, ok := getChallenge(t, a, otherIMPI); ok {
		t.Error("Challenge put after its KeepUntil")
	}

	c.KeepUntil = time.Now().Add(short)
	putChallenge(t, a, c)
	c.Nonce, c.KeepUntil = "n3", time.Now().Add(time.Hour).Round(time.Millisecond)
	putChallenge(t, b, c)
	waitOut()
	if got, ok := getChallenge(t, a, impi); !ok || got.Nonce != "n3" {
		t.Errorf("Challenge after the KeepUntil of the challenge it replaced = %+v, %v; want n3", got, ok)
	}
}

func scscfRegistrationOf(t *testing.T, a, b scscf.Store) {
	r := registration()
	putRegistration(t, a, r)
	if got, ok := getRegistration(t, b, impi); !ok || !sameJSON(t, got, r) {
		t.Fatalf("Registration = %+v, %v; want %+v", got, ok, r)
	}
	// RegistrationOf compares as ims.SamePublicIdentity: the host without
	// case.
	for _, id := range []string{impu, tel, "sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"} {
		if got, ok := registrationOf(t, b, id); !ok || got.PrivateIdentity != impi {
			t.Errorf("RegistrationOf(%s) = %+v, %v", id, got, ok)
		}
	}
	if _, ok := registrationOf(t, b, otherIMPU); ok {
		t.Errorf("RegistrationOf(%s) found a registration", otherIMPU)
	}
}

// The user part of a SIP URI compares with case (RFC 3261 19.1.4), as
// the HSS compares it, so that the S-CSCF and the HSS never take two
// identities for one.
func scscfUserPartWithCase(t *testing.T, a, b scscf.Store) {
	r := registration()
	r.PublicIdentities = []string{"sip:Alice@ims.mnc001.mcc001.3gppnetwork.org"}
	putRegistration(t, a, r)
	if got, ok := registrationOf(t, b, "sip:Alice@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.PrivateIdentity != impi {
		t.Errorf("RegistrationOf with the host in capitals = %+v, %v", got, ok)
	}
	if _, ok := registrationOf(t, b, "sip:alice@ims.mnc001.mcc001.3gppnetwork.org"); ok {
		t.Error("RegistrationOf found the registration of sip:Alice for sip:alice")
	}
}

func scscfRegistrationLosesIdentity(t *testing.T, a, b scscf.Store) {
	r := registration()
	putRegistration(t, a, r)
	r.PublicIdentities = []string{impu}
	r.CSeq = 3
	putRegistration(t, b, r)
	if _, ok := registrationOf(t, a, tel); ok {
		t.Error("RegistrationOf finds the registration by an identity it no longer has")
	}
	if got, ok := registrationOf(t, a, impu); !ok || got.CSeq != 3 {
		t.Errorf("RegistrationOf(%s) = %+v, %v; want the new registration", impu, got, ok)
	}
}

func scscfDeleteRegistration(t *testing.T, a, b scscf.Store) {
	putRegistration(t, a, registration())
	deleteRegistration(t, b, impi)
	if _, ok := getRegistration(t, a, impi); ok {
		t.Error("Registration after DeleteRegistration")
	}
	if _, ok := registrationOf(t, a, tel); ok {
		t.Error("RegistrationOf after DeleteRegistration")
	}
	// The identities are free for the next registration.
	putRegistration(t, a, registration())
	if _, ok := registrationOf(t, b, tel); !ok {
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
	putSubscription(t, a, one)
	putSubscription(t, b, two)
	putSubscription(t, a, other)
	if got, ok := getSubscription(t, b, one.Dialog.ID); !ok || !sameJSON(t, got, one) {
		t.Fatalf("Subscription = %+v, %v; want %+v", got, ok, one)
	}
	if got := callIDs(subscriptionsOf(t, b, impi)); !slices.Equal(got, []string{"sub-2", "sub:1"}) {
		t.Errorf("SubscriptionsOf = %v", got)
	}
	one.Version = 2
	putSubscription(t, b, one)
	if got, _ := getSubscription(t, a, one.Dialog.ID); got.Version != 2 {
		t.Errorf("Version after the second PutSubscription = %d", got.Version)
	}
	if got := len(subscriptionsOf(t, a, impi)); got != 2 {
		t.Errorf("SubscriptionsOf after a rewrite has %d subscriptions, want 2", got)
	}
	deleteSubscription(t, a, one.Dialog.ID)
	if _, ok := getSubscription(t, b, one.Dialog.ID); ok {
		t.Error("Subscription after DeleteSubscription")
	}
	if got := callIDs(subscriptionsOf(t, b, impi)); !slices.Equal(got, []string{"sub-2"}) {
		t.Errorf("SubscriptionsOf after DeleteSubscription = %v", got)
	}
	if got := subscriptionsOf(t, b, "nobody@ims.mnc001.mcc001.3gppnetwork.org"); len(got) != 0 {
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
		putSubscription(t, s, subscription(fmt.Sprintf("sub-%02d", i), impi))
	})
	if got := len(subscriptionsOf(t, a, impi)); got != 2*n {
		t.Errorf("SubscriptionsOf has %d subscriptions, want %d", got, 2*n)
	}
}

func scscfOriginalDialog(t *testing.T, a, b scscf.Store) {
	d := scscf.OriginalDialog{ID: "od-1", ServedUser: impu, SessionCase: ifc.OriginatingRegistered, RegState: sip.RegStateRegistered,
		Criteria: registration().InitialFilterCriteria, Aliases: []string{impu, tel}, ICID: "icid-1",
		Expires: time.Now().Add(short).Round(time.Millisecond)}
	putOriginalDialog(t, a, d)
	if got, ok := getOriginalDialog(t, b, "od-1"); !ok || !sameJSON(t, got, d) {
		t.Fatalf("OriginalDialog = %+v, %v; want %+v", got, ok, d)
	}
	d.Returned = true
	putOriginalDialog(t, b, d)
	if got, _ := getOriginalDialog(t, a, "od-1"); !got.Returned {
		t.Error("Returned is not kept")
	}
	waitOut()
	if _, ok := getOriginalDialog(t, a, "od-1"); ok {
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
	putUnregisteredProfile(t, a, p)
	for _, id := range []string{impu, tel} {
		if got, ok := unregisteredProfileOf(t, b, id); !ok || !sameJSON(t, got, p) {
			t.Errorf("UnregisteredProfileOf(%s) = %+v, %v", id, got, ok)
		}
	}
	// A new profile of the user without the tel URI.
	p2 := unregisteredProfile(short)
	p2.PublicIdentities = []string{impu}
	p2.Expires = p.Expires.Add(time.Millisecond)
	putUnregisteredProfile(t, b, p2)
	if _, ok := unregisteredProfileOf(t, a, tel); ok {
		t.Error("UnregisteredProfileOf finds the profile by an identity it no longer has")
	}
	if deleteUnregisteredProfile(t, a, impi, p.Expires) {
		t.Error("DeleteUnregisteredProfile removed a profile that came after its timer")
	}
	if _, ok := unregisteredProfileOf(t, b, impu); !ok {
		t.Error("the newer profile is gone")
	}
	waitOut()
	if _, ok := unregisteredProfileOf(t, b, impu); ok {
		t.Error("UnregisteredProfileOf after its Expires")
	}
	// The timer of the profile still removes it after its Expires.
	if !deleteUnregisteredProfile(t, a, impi, p2.Expires) {
		t.Error("DeleteUnregisteredProfile at the right Expires did nothing")
	}
	if deleteUnregisteredProfile(t, a, impi, p2.Expires) {
		t.Error("a second DeleteUnregisteredProfile reported true")
	}
}

// scscfUnregisteredProfileEndsOnce has the timers of two instances end
// the same profile at once: only one of them may send the SAR.
func scscfUnregisteredProfileEndsOnce(t *testing.T, a, b scscf.Store) {
	for range 10 {
		p := unregisteredProfile(time.Hour)
		putUnregisteredProfile(t, a, p)
		var wins atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if deleteUnregisteredProfile(t, s, impi, p.Expires) {
				wins.Add(1)
			}
		})
		if got := wins.Load(); got != 1 {
			t.Fatalf("%d instances removed the profile, want 1", got)
		}
	}
}
