package storetest

import (
	"context"
	"crypto/rand"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// PCSCF runs the suite of pcscf.Store.
func PCSCF(t *testing.T, newPair func(t *testing.T) (pcscf.Store, pcscf.Store)) {
	run(t, newPair, []testCase[pcscf.Store]{
		{"Association", pcscfAssociation},
		{"AssociationOfContact", pcscfAssociationOfContact},
		{"AssociationChangesItsIndexes", pcscfAssociationChangesIndexes},
		{"AssociationUntilItsKeepUntil", pcscfAssociationKeptUntil},
		{"Dialog", pcscfDialog},
		{"DialogOfPolicySession", pcscfDialogOfPolicySession},
		{"ConcurrentUpdatesOfOneDialog", pcscfConcurrentUpdates},
		{"OnlyOneTakesADialog", pcscfOnlyOneTakes},
		{"RegSubscription", pcscfRegSubscription},
		{"RegSubscriptionReplacesTheOldOne", pcscfRegSubscriptionReplaces},
		{"OnlyOneCreatesARegSubscription", pcscfOnlyOneCreatesARegSubscription},
		{"OnlyOneRefreshesARegSubscription", pcscfOnlyOneRefreshesARegSubscription},
		{"SecurityAssociationsUntilTheyExpire", pcscfSecurityAssociations},
	})
}

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent cases call them on goroutines of
// their own.

func getAssociation(t *testing.T, s pcscf.Store, source sip.Destination) (pcscf.Association, bool) {
	t.Helper()
	a, ok, err := s.Association(context.Background(), source)
	if err != nil {
		t.Errorf("Association(%s): %v", source, err)
	}
	return a, ok
}

func associationOfContact(t *testing.T, s pcscf.Store, contact string) (pcscf.Association, bool) {
	t.Helper()
	a, ok, err := s.AssociationOfContact(context.Background(), contact)
	if err != nil {
		t.Errorf("AssociationOfContact(%s): %v", contact, err)
	}
	return a, ok
}

func associationOfPolicySession(t *testing.T, s pcscf.Store, session policy.SessionID) (pcscf.Association, bool) {
	t.Helper()
	a, ok, err := s.AssociationOfPolicySession(context.Background(), session)
	if err != nil {
		t.Errorf("AssociationOfPolicySession(%s): %v", session, err)
	}
	return a, ok
}

func putAssociation(t *testing.T, s pcscf.Store, a pcscf.Association) {
	t.Helper()
	if err := s.PutAssociation(context.Background(), a); err != nil {
		t.Errorf("PutAssociation(%s): %v", a.Source, err)
	}
}

func deleteAssociation(t *testing.T, s pcscf.Store, source sip.Destination) {
	t.Helper()
	if err := s.DeleteAssociation(context.Background(), source); err != nil {
		t.Errorf("DeleteAssociation(%s): %v", source, err)
	}
}

func putDialog(t *testing.T, s pcscf.Store, d pcscf.Dialog) {
	t.Helper()
	if err := s.PutDialog(context.Background(), d); err != nil {
		t.Errorf("PutDialog(%+v): %v", d.Key, err)
	}
}

func updateDialog(t *testing.T, s pcscf.Store, key pcscf.DialogKey, change func(*pcscf.Dialog)) (pcscf.Dialog, bool) {
	t.Helper()
	d, ok, err := s.UpdateDialog(context.Background(), key, change)
	if err != nil {
		t.Errorf("UpdateDialog(%+v): %v", key, err)
	}
	return d, ok
}

func takeDialog(t *testing.T, s pcscf.Store, key pcscf.DialogKey) (pcscf.Dialog, bool) {
	t.Helper()
	d, ok, err := s.TakeDialog(context.Background(), key)
	if err != nil {
		t.Errorf("TakeDialog(%+v): %v", key, err)
	}
	return d, ok
}

func dialogOfPolicySession(t *testing.T, s pcscf.Store, session policy.SessionID) (pcscf.Dialog, bool) {
	t.Helper()
	d, ok, err := s.DialogOfPolicySession(context.Background(), session)
	if err != nil {
		t.Errorf("DialogOfPolicySession(%s): %v", session, err)
	}
	return d, ok
}

func securityAssociations(t *testing.T, s pcscf.Store, ue netip.AddrPort) (pcscf.SecurityAssociations, bool) {
	t.Helper()
	sas, ok, err := s.SecurityAssociations(context.Background(), ue)
	if err != nil {
		t.Errorf("SecurityAssociations(%s): %v", ue, err)
	}
	return sas, ok
}

func putSecurityAssociations(t *testing.T, s pcscf.Store, sas pcscf.SecurityAssociations) {
	t.Helper()
	if err := s.PutSecurityAssociations(context.Background(), sas); err != nil {
		t.Errorf("PutSecurityAssociations(%s): %v", sas.UE, err)
	}
}

func deleteSecurityAssociations(t *testing.T, s pcscf.Store, ue netip.AddrPort) {
	t.Helper()
	if err := s.DeleteSecurityAssociations(context.Background(), ue); err != nil {
		t.Errorf("DeleteSecurityAssociations(%s): %v", ue, err)
	}
}

var ueSource = sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.10:5060")}

func association() pcscf.Association {
	expires := time.Now().Add(time.Hour).Round(time.Millisecond)
	return pcscf.Association{
		Source: ueSource, Contact: "sip:ue1@192.0.2.10:5060;transport=udp", SentBy: "192.0.2.10:5060",
		PrivateIdentity: impi, PublicIdentities: []string{impu, tel},
		ServiceRoute: []string{"<sip:orig@scscf.ims.mnc001.mcc001.3gppnetwork.org;lr>"},
		Expires:      expires, KeepUntil: expires.Add(time.Minute), SignallingSession: "pcscf;1;sig",
	}
}

func pcscfAssociation(t *testing.T, a, b pcscf.Store) {
	as := association()
	putAssociation(t, a, as)
	if got, ok := getAssociation(t, b, ueSource); !ok || !sameJSON(t, got, as) {
		t.Fatalf("Association = %+v, %v; want %+v", got, ok, as)
	}
	tcp := sip.Destination{Transport: sip.TCP, Addr: ueSource.Addr}
	if _, ok := getAssociation(t, b, tcp); ok {
		t.Error("the association of UDP is found for TCP")
	}
	deleteAssociation(t, b, ueSource)
	if _, ok := getAssociation(t, a, ueSource); ok {
		t.Error("Association after DeleteAssociation")
	}
	if _, ok := associationOfContact(t, a, as.Contact); ok {
		t.Error("AssociationOfContact after DeleteAssociation")
	}
	if _, ok := associationOfPolicySession(t, a, as.SignallingSession); ok {
		t.Error("AssociationOfPolicySession after DeleteAssociation")
	}
}

func pcscfAssociationOfContact(t *testing.T, a, b pcscf.Store) {
	as := association()
	putAssociation(t, a, as)
	// The host compares without case; the user and the port must be the
	// same. Parameters do not matter.
	for _, contact := range []string{"sip:ue1@192.0.2.10:5060", "sip:ue1@192.0.2.10:5060;ob"} {
		if got, ok := associationOfContact(t, b, contact); !ok || got.Source != ueSource {
			t.Errorf("AssociationOfContact(%s) = %+v, %v", contact, got, ok)
		}
	}
	for _, contact := range []string{"sip:ue2@192.0.2.10:5060", "sip:ue1@192.0.2.10:5070", "sip:ue1@192.0.2.10", "not a uri"} {
		if _, ok := associationOfContact(t, b, contact); ok {
			t.Errorf("AssociationOfContact(%s) found the association", contact)
		}
	}
	host := association()
	host.Source = sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.11:5060")}
	host.Contact = "sip:ue3@UE3.Example.Test:5060"
	host.SignallingSession = ""
	putAssociation(t, a, host)
	if got, ok := associationOfContact(t, b, "sip:ue3@ue3.example.test:5060"); !ok || got.Source != host.Source {
		t.Errorf("AssociationOfContact with the host in lower case = %+v, %v", got, ok)
	}
	if got, ok := associationOfPolicySession(t, b, as.SignallingSession); !ok || got.Source != ueSource {
		t.Errorf("AssociationOfPolicySession = %+v, %v", got, ok)
	}
}

// pcscfAssociationChangesIndexes: a refresh from a new contact, or a
// new policy session, leaves nothing to be found by the old ones.
func pcscfAssociationChangesIndexes(t *testing.T, a, b pcscf.Store) {
	as := association()
	putAssociation(t, a, as)
	as.Contact = "sip:ue1-new@192.0.2.10:5060"
	as.SignallingSession = "pcscf;2;sig"
	putAssociation(t, b, as)
	if _, ok := associationOfContact(t, a, "sip:ue1@192.0.2.10:5060"); ok {
		t.Error("AssociationOfContact finds the association by its old contact")
	}
	if _, ok := associationOfPolicySession(t, a, "pcscf;1;sig"); ok {
		t.Error("AssociationOfPolicySession finds the association by its old session")
	}
	if _, ok := associationOfContact(t, a, as.Contact); !ok {
		t.Error("AssociationOfContact misses the new contact")
	}
	if _, ok := associationOfPolicySession(t, a, as.SignallingSession); !ok {
		t.Error("AssociationOfPolicySession misses the new session")
	}
	as.SignallingSession = ""
	putAssociation(t, a, as)
	if _, ok := associationOfPolicySession(t, b, "pcscf;2;sig"); ok {
		t.Error("AssociationOfPolicySession after the session ended")
	}
}

// pcscfAssociationKeptUntil: the Store keeps an association until the
// KeepUntil that the P-CSCF wrote into it, also one put after that time,
// and then finds it by none of its keys. An association that a refresh
// keeps for longer stays.
func pcscfAssociationKeptUntil(t *testing.T, a, b pcscf.Store) {
	ended := association()
	ended.KeepUntil = time.Now().Add(short).Round(time.Millisecond)
	putAssociation(t, a, ended)
	if _, ok := getAssociation(t, b, ended.Source); !ok {
		t.Fatal("Association before its KeepUntil")
	}
	past := association()
	past.Source = sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.11:5060")}
	past.Contact, past.SignallingSession = "sip:ue3@192.0.2.11:5060", "pcscf;3;sig"
	past.KeepUntil = time.Now().Add(-time.Second)
	putAssociation(t, b, past)
	refreshed := association()
	refreshed.Source = sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.12:5060")}
	refreshed.Contact, refreshed.SignallingSession = "sip:ue4@192.0.2.12:5060", "pcscf;4;sig"
	refreshed.KeepUntil = time.Now().Add(short)
	putAssociation(t, a, refreshed)
	refreshed.KeepUntil = time.Now().Add(time.Hour)
	putAssociation(t, b, refreshed)

	waitOut()
	for _, gone := range []pcscf.Association{ended, past} {
		if _, ok := getAssociation(t, b, gone.Source); ok {
			t.Errorf("Association of %s after its KeepUntil", gone.Source)
		}
		if _, ok := associationOfContact(t, a, gone.Contact); ok {
			t.Errorf("AssociationOfContact(%s) after its KeepUntil", gone.Contact)
		}
		if _, ok := associationOfPolicySession(t, a, gone.SignallingSession); ok {
			t.Errorf("AssociationOfPolicySession(%s) after its KeepUntil", gone.SignallingSession)
		}
	}
	if _, ok := associationOfContact(t, a, refreshed.Contact); !ok {
		t.Error("a refresh did not keep the association for longer")
	}
}

func dialog() pcscf.Dialog {
	return pcscf.Dialog{
		Key:         pcscf.DialogKey{CallID: "call:1@192.0.2.10", UETag: "ue-tag"},
		Originating: true,
		Caller:      pcscf.Party{NameAddr: "<" + impu + ">;tag=ue-tag", Target: "sip:ue1@192.0.2.10:5060", CSeq: 1},
		Callee:      pcscf.Party{NameAddr: "<" + otherIMPU + ">", Target: "sip:ue2@192.0.2.20:5060", Route: []string{"<sip:orig@scscf;lr>"}},
		Relay: &pcscf.RelayContext{ID: "ctx-1", Access: netip.MustParseAddrPort("198.51.100.1:40000"),
			Core: netip.MustParseAddrPort("198.51.100.1:40002")},
		ICID: "icid-1",
	}
}

func pcscfDialog(t *testing.T, a, b pcscf.Store) {
	d := dialog()
	if _, ok := updateDialog(t, a, d.Key, func(*pcscf.Dialog) { t.Error("change ran for a dialog that is not there") }); ok {
		t.Error("UpdateDialog of no dialog reported true")
	}
	putDialog(t, a, d)
	got, ok := updateDialog(t, b, d.Key, func(d *pcscf.Dialog) { d.Confirmed = true; d.Caller.CSeq = 2 })
	if !ok || !got.Confirmed || got.Caller.CSeq != 2 {
		t.Fatalf("UpdateDialog = %+v, %v", got, ok)
	}
	got, ok = takeDialog(t, a, d.Key)
	d.Confirmed, d.Caller.CSeq = true, 2
	if !ok || !sameJSON(t, got, d) {
		t.Fatalf("TakeDialog = %+v, %v; want %+v", got, ok, d)
	}
	// A dialog that has ended does not come back.
	if _, ok := updateDialog(t, b, d.Key, func(*pcscf.Dialog) {}); ok {
		t.Error("UpdateDialog after TakeDialog reported true")
	}
	if _, ok := takeDialog(t, b, d.Key); ok {
		t.Error("a second TakeDialog got the dialog")
	}
}

func pcscfDialogOfPolicySession(t *testing.T, a, b pcscf.Store) {
	d := dialog()
	d.PolicySession = "pcscf;1;media"
	putDialog(t, a, d)
	if got, ok := dialogOfPolicySession(t, b, "pcscf;1;media"); !ok || got.Key != d.Key {
		t.Fatalf("DialogOfPolicySession = %+v, %v", got, ok)
	}
	// The PCRF took the resources: the dialog loses its session (as
	// release.go does), and a new one replaces it.
	updateDialog(t, b, d.Key, func(d *pcscf.Dialog) { d.PolicySession = "pcscf;2;media" })
	if _, ok := dialogOfPolicySession(t, a, "pcscf;1;media"); ok {
		t.Error("DialogOfPolicySession finds the dialog by its old session")
	}
	if got, ok := dialogOfPolicySession(t, a, "pcscf;2;media"); !ok || got.Key != d.Key {
		t.Errorf("DialogOfPolicySession of the new session = %+v, %v", got, ok)
	}
	takeDialog(t, a, d.Key)
	if _, ok := dialogOfPolicySession(t, b, "pcscf;2;media"); ok {
		t.Error("DialogOfPolicySession after TakeDialog")
	}
}

// pcscfConcurrentUpdates has two instances count requests of one
// dialog at once, as the requests of a dialog may pass two P-CSCF
// instances. No update may be lost.
func pcscfConcurrentUpdates(t *testing.T, a, b pcscf.Store) {
	d := dialog()
	d.Caller.CSeq = 0
	putDialog(t, a, d)
	const n = 25
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		for range n {
			updateDialog(t, s, d.Key, func(d *pcscf.Dialog) { d.Caller.CSeq++ })
		}
	})
	got, _ := updateDialog(t, a, d.Key, func(*pcscf.Dialog) {})
	if got.Caller.CSeq != 2*n {
		t.Errorf("CSeq = %d after %d updates", got.Caller.CSeq, 2*n)
	}
}

// pcscfOnlyOneTakes: the BYE, the PCRF's event and the ACK timer may
// all end one dialog at once, on different instances.
func pcscfOnlyOneTakes(t *testing.T, a, b pcscf.Store) {
	for range 10 {
		d := dialog()
		putDialog(t, a, d)
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := takeDialog(t, s, d.Key); ok {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the dialog, want 1", got)
		}
	}
}

// putPCSCFRegSubscription creates the subscription and reports a
// failure unless it was written, for the cases that need one there.
func putPCSCFRegSubscription(t *testing.T, s pcscf.Store, sub pcscf.RegSubscription) {
	t.Helper()
	ok, err := s.CreateRegSubscription(context.Background(), sub)
	if err != nil {
		t.Errorf("CreateRegSubscription(%s): %v", sub.PrivateIdentity, err)
	}
	if !ok {
		t.Errorf("CreateRegSubscription(%s) wrote nothing", sub.PrivateIdentity)
	}
}

func createPCSCFRegSubscription(t *testing.T, s pcscf.Store, sub pcscf.RegSubscription) bool {
	t.Helper()
	ok, err := s.CreateRegSubscription(context.Background(), sub)
	if err != nil {
		t.Errorf("CreateRegSubscription(%s): %v", sub.PrivateIdentity, err)
	}
	return ok
}

func updatePCSCFRegSubscription(t *testing.T, s pcscf.Store, privateID string, change func(*pcscf.RegSubscription) bool) (pcscf.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.UpdateRegSubscription(context.Background(), privateID, change)
	if err != nil {
		t.Errorf("UpdateRegSubscription(%s): %v", privateID, err)
	}
	return sub, ok
}

func pcscfRegSubscriptionOf(t *testing.T, s pcscf.Store, privateID string) (pcscf.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.RegSubscription(context.Background(), privateID)
	if err != nil {
		t.Errorf("RegSubscription(%s): %v", privateID, err)
	}
	return sub, ok
}

func pcscfRegSubscriptionOfDialog(t *testing.T, s pcscf.Store, callID string) (pcscf.RegSubscription, bool) {
	t.Helper()
	sub, ok, err := s.RegSubscriptionOfDialog(context.Background(), callID)
	if err != nil {
		t.Errorf("RegSubscriptionOfDialog(%s): %v", callID, err)
	}
	return sub, ok
}

func deletePCSCFRegSubscription(t *testing.T, s pcscf.Store, privateID string) {
	t.Helper()
	if err := s.DeleteRegSubscription(context.Background(), privateID); err != nil {
		t.Errorf("DeleteRegSubscription(%s): %v", privateID, err)
	}
}

func newPCSCFRegSubscription(callID string, lifetime time.Duration) pcscf.RegSubscription {
	return pcscf.RegSubscription{PrivateIdentity: impi, PublicIdentity: impu,
		Dialog: sip.Dialog{ID: sip.DialogID{CallID: callID, LocalTag: "l", RemoteTag: "r"}, State: sip.DialogConfirmed,
			LocalSeq: 1, RemoteSeq: 1, LocalURI: "sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060", RemoteURI: impu,
			RemoteTarget: "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060", RouteSet: []string{"sip:scscf;lr"}},
		Requested: 3761 * time.Second, Granted: 3761 * time.Second, Subscribed: time.Now().Round(time.Millisecond),
		Expires: time.Now().Add(lifetime).Round(time.Millisecond)}
}

// pcscfOnlyOneCreatesARegSubscription: two REGISTERs of one user, at one
// instance or at two, must leave one subscription; the instance whose
// write loses does not subscribe (pcscf.Store).
func pcscfOnlyOneCreatesARegSubscription(t *testing.T, a, b pcscf.Store) {
	for range 10 {
		deletePCSCFRegSubscription(t, a, impi)
		first, second := newPCSCFRegSubscription(rand.Text(), time.Hour), newPCSCFRegSubscription(rand.Text(), time.Hour)
		var created atomic.Int32
		var winner atomic.Value
		parallel(2, func(i int) {
			s, sub := a, first
			if i == 1 {
				s, sub = b, second
			}
			if createPCSCFRegSubscription(t, s, sub) {
				created.Add(1)
				winner.Store(sub.Dialog.ID.CallID)
			}
		})
		if got := created.Load(); got != 1 {
			t.Fatalf("%d of two instances created the subscription, want 1", got)
		}
		// The subscription of the loser finds none by its dialog, so its
		// NOTIFYs get 481 rather than reaching the winner's subscription.
		got, ok := pcscfRegSubscriptionOf(t, a, impi)
		if !ok || got.Dialog.ID.CallID != winner.Load() {
			t.Fatalf("RegSubscription = %+v, %v; want the one that was created", got, ok)
		}
		for _, sub := range []pcscf.RegSubscription{first, second} {
			_, found := pcscfRegSubscriptionOfDialog(t, b, sub.Dialog.ID.CallID)
			if found != (sub.Dialog.ID.CallID == winner.Load()) {
				t.Errorf("RegSubscriptionOfDialog(%s) = %v", sub.Dialog.ID.CallID, found)
			}
		}
	}
	deletePCSCFRegSubscription(t, a, impi)
}

// pcscfOnlyOneRefreshesARegSubscription: the CSeq of a refresh goes up
// once however many instances find the refresh due at the same moment,
// so that no two SUBSCRIBEs go out with one CSeq (refreshRegEvent).
func pcscfOnlyOneRefreshesARegSubscription(t *testing.T, a, b pcscf.Store) {
	sub := newPCSCFRegSubscription("sub-refresh", time.Hour)
	putPCSCFRegSubscription(t, a, sub)
	var refreshing atomic.Int32
	parallel(4, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		// Each instance takes the next CSeq only while the subscription is
		// still at the one it read.
		if _, ok := updatePCSCFRegSubscription(t, s, impi, func(v *pcscf.RegSubscription) bool {
			if v.Dialog.LocalSeq != sub.Dialog.LocalSeq {
				return false
			}
			v.Dialog.LocalSeq++
			return true
		}); ok {
			refreshing.Add(1)
		}
	})
	if got := refreshing.Load(); got != 1 {
		t.Errorf("%d instances took the refresh, want 1", got)
	}
	if got, ok := pcscfRegSubscriptionOf(t, b, impi); !ok || got.Dialog.LocalSeq != sub.Dialog.LocalSeq+1 {
		t.Errorf("the CSeq after the refresh = %+v, %v; want %d", got, ok, sub.Dialog.LocalSeq+1)
	}
	// A change that reports false writes nothing and says so.
	if got, ok := updatePCSCFRegSubscription(t, a, impi, func(*pcscf.RegSubscription) bool { return false }); ok {
		t.Errorf("UpdateRegSubscription of a change that reported false = %+v, %v", got, ok)
	}
	// Neither does one of a user with no subscription.
	if _, ok := updatePCSCFRegSubscription(t, a, "nobody@example.net", func(*pcscf.RegSubscription) bool { return true }); ok {
		t.Error("UpdateRegSubscription of a user with no subscription reported true")
	}
	deletePCSCFRegSubscription(t, a, impi)
}

// pcscfRegSubscription: the subscription of TS 24.229 5.2.3 is found by
// the private identity it belongs to, and by the Call-ID of its dialog
// for a NOTIFY that arrives in it.
func pcscfRegSubscription(t *testing.T, a, b pcscf.Store) {
	sub := newPCSCFRegSubscription("sub-1@pcscf", short)
	putPCSCFRegSubscription(t, a, sub)
	if got, ok := pcscfRegSubscriptionOf(t, b, sub.PrivateIdentity); !ok || !sameJSON(t, got, sub) {
		t.Fatalf("RegSubscription = %+v, %v; want %+v", got, ok, sub)
	}
	// The realm of a private identity compares without case.
	if got, ok := pcscfRegSubscriptionOf(t, b, "001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"); !ok || got.Dialog.ID != sub.Dialog.ID {
		t.Errorf("RegSubscription of the identity in another case = %+v, %v", got, ok)
	}
	if got, ok := pcscfRegSubscriptionOfDialog(t, b, sub.Dialog.ID.CallID); !ok || !sameJSON(t, got, sub) {
		t.Errorf("RegSubscriptionOfDialog = %+v, %v; want %+v", got, ok, sub)
	}
	if _, ok := pcscfRegSubscriptionOfDialog(t, b, "another-call-id"); ok {
		t.Error("RegSubscriptionOfDialog of a Call-ID of no subscription")
	}
	waitOut()
	if _, ok := pcscfRegSubscriptionOf(t, b, sub.PrivateIdentity); ok {
		t.Error("RegSubscription after its Expires")
	}
	if _, ok := pcscfRegSubscriptionOfDialog(t, b, sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscriptionOfDialog after its Expires")
	}
	sub = newPCSCFRegSubscription("sub-2", time.Hour)
	putPCSCFRegSubscription(t, a, sub)
	deletePCSCFRegSubscription(t, b, sub.PrivateIdentity)
	if _, ok := pcscfRegSubscriptionOf(t, a, sub.PrivateIdentity); ok {
		t.Error("RegSubscription after DeleteRegSubscription")
	}
	if _, ok := pcscfRegSubscriptionOfDialog(t, a, sub.Dialog.ID.CallID); ok {
		t.Error("RegSubscriptionOfDialog after DeleteRegSubscription")
	}
}

// pcscfRegSubscriptionReplaces: a live subscription holds its private
// identity, so the next one takes a Delete first; the dialog of the one
// before it then finds none.
func pcscfRegSubscriptionReplaces(t *testing.T, a, b pcscf.Store) {
	old, sub := newPCSCFRegSubscription("sub-1", time.Hour), newPCSCFRegSubscription("sub-2", time.Hour)
	putPCSCFRegSubscription(t, a, old)
	if createPCSCFRegSubscription(t, b, sub) {
		t.Error("a second subscription was created while the first holds")
	}
	deletePCSCFRegSubscription(t, b, old.PrivateIdentity)
	putPCSCFRegSubscription(t, b, sub)
	if got, ok := pcscfRegSubscriptionOf(t, a, sub.PrivateIdentity); !ok || got.Dialog.ID.CallID != sub.Dialog.ID.CallID {
		t.Errorf("RegSubscription = %+v, %v; want the new one", got, ok)
	}
	if _, ok := pcscfRegSubscriptionOfDialog(t, a, old.Dialog.ID.CallID); ok {
		t.Error("the dialog of the old subscription still finds one")
	}
	// A refresh of the new one keeps it, with the CSeq of the refresh.
	if _, ok := updatePCSCFRegSubscription(t, a, sub.PrivateIdentity, func(v *pcscf.RegSubscription) bool {
		v.Dialog.LocalSeq = 2
		return true
	}); !ok {
		t.Error("the refresh of the new subscription changed nothing")
	}
	if got, ok := pcscfRegSubscriptionOfDialog(t, b, sub.Dialog.ID.CallID); !ok || got.Dialog.LocalSeq != 2 {
		t.Errorf("RegSubscriptionOfDialog after a refresh = %+v, %v", got, ok)
	}
	// A subscription that has run out no longer holds the identity.
	ran := newPCSCFRegSubscription("sub-3", short)
	deletePCSCFRegSubscription(t, a, sub.PrivateIdentity)
	putPCSCFRegSubscription(t, a, ran)
	waitOut()
	if !createPCSCFRegSubscription(t, b, newPCSCFRegSubscription("sub-4", time.Hour)) {
		t.Error("no subscription was created while one that had run out was stored")
	}
	deletePCSCFRegSubscription(t, a, sub.PrivateIdentity)
}

func pcscfSecurityAssociations(t *testing.T, a, b pcscf.Store) {
	ue := netip.MustParseAddrPort("192.0.2.10:5062")
	sas := pcscf.SecurityAssociations{UE: ue, Temporary: true,
		UEMechanism:    ims.IPsec3GPP{Transform: ims.Transform{Algorithm: "hmac-sha-1-96"}, SPIC: 1111, SPIS: 2222, PortC: 5063, PortS: 5062},
		PCSCFMechanism: ims.IPsec3GPP{Transform: ims.Transform{Algorithm: "hmac-sha-1-96"}, SPIC: 3333, SPIS: 4444, PortC: 5065, PortS: 5064},
		Client:         []sip.SecurityMechanism{{Name: "ipsec-3gpp", Params: sip.Params{{Name: "alg", Value: "hmac-sha-1-96"}}}},
		IK:             [16]byte{1}, CK: [16]byte{2}, PrivateIdentity: impi,
		Expires: time.Now().Add(short).Round(time.Millisecond)}
	putSecurityAssociations(t, a, sas)
	if got, ok := securityAssociations(t, b, ue); !ok || !sameJSON(t, got, sas) {
		t.Fatalf("SecurityAssociations = %+v, %v; want %+v", got, ok, sas)
	}
	waitOut()
	if _, ok := securityAssociations(t, b, ue); ok {
		t.Error("SecurityAssociations after they expired")
	}
	sas.Expires = time.Now().Add(time.Hour)
	putSecurityAssociations(t, b, sas)
	deleteSecurityAssociations(t, a, ue)
	if _, ok := securityAssociations(t, b, ue); ok {
		t.Error("SecurityAssociations after DeleteSecurityAssociations")
	}
}
