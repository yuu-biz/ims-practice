package storetest

import (
	"context"
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
