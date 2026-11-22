package storetest

import (
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// PCSCF runs the suite of pcscf.Store.
func PCSCF(t *testing.T, newPair func(t *testing.T) (pcscf.Store, pcscf.Store)) {
	run(t, newPair, []testCase[pcscf.Store]{
		{"Association", pcscfAssociation},
		{"AssociationOfContact", pcscfAssociationOfContact},
		{"AssociationChangesItsIndexes", pcscfAssociationChangesIndexes},
		{"Dialog", pcscfDialog},
		{"DialogOfPolicySession", pcscfDialogOfPolicySession},
		{"ConcurrentUpdatesOfOneDialog", pcscfConcurrentUpdates},
		{"OnlyOneTakesADialog", pcscfOnlyOneTakes},
		{"SecurityAssociationsUntilTheyExpire", pcscfSecurityAssociations},
	})
}

var ueSource = sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.10:5060")}

func association() pcscf.Association {
	return pcscf.Association{
		Source: ueSource, Contact: "sip:ue1@192.0.2.10:5060;transport=udp", SentBy: "192.0.2.10:5060",
		PrivateIdentity: impi, PublicIdentities: []string{impu, tel},
		ServiceRoute: []string{"<sip:orig@scscf.ims.mnc001.mcc001.3gppnetwork.org;lr>"},
		Expires:      time.Now().Add(time.Hour).Round(time.Millisecond), SignallingSession: "pcscf;1;sig",
	}
}

func pcscfAssociation(t *testing.T, a, b pcscf.Store) {
	as := association()
	a.PutAssociation(as)
	if got, ok := b.Association(ueSource); !ok || !sameJSON(t, got, as) {
		t.Fatalf("Association = %+v, %v; want %+v", got, ok, as)
	}
	tcp := sip.Destination{Transport: sip.TCP, Addr: ueSource.Addr}
	if _, ok := b.Association(tcp); ok {
		t.Error("the association of UDP is found for TCP")
	}
	b.DeleteAssociation(ueSource)
	if _, ok := a.Association(ueSource); ok {
		t.Error("Association after DeleteAssociation")
	}
	if _, ok := a.AssociationOfContact(as.Contact); ok {
		t.Error("AssociationOfContact after DeleteAssociation")
	}
	if _, ok := a.AssociationOfPolicySession(as.SignallingSession); ok {
		t.Error("AssociationOfPolicySession after DeleteAssociation")
	}
}

func pcscfAssociationOfContact(t *testing.T, a, b pcscf.Store) {
	as := association()
	a.PutAssociation(as)
	// The host compares without case; the user and the port must be the
	// same. Parameters do not matter.
	for _, contact := range []string{"sip:ue1@192.0.2.10:5060", "sip:ue1@192.0.2.10:5060;ob"} {
		if got, ok := b.AssociationOfContact(contact); !ok || got.Source != ueSource {
			t.Errorf("AssociationOfContact(%s) = %+v, %v", contact, got, ok)
		}
	}
	for _, contact := range []string{"sip:ue2@192.0.2.10:5060", "sip:ue1@192.0.2.10:5070", "sip:ue1@192.0.2.10", "not a uri"} {
		if _, ok := b.AssociationOfContact(contact); ok {
			t.Errorf("AssociationOfContact(%s) found the association", contact)
		}
	}
	host := association()
	host.Source = sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.11:5060")}
	host.Contact = "sip:ue3@UE3.Example.Test:5060"
	host.SignallingSession = ""
	a.PutAssociation(host)
	if got, ok := b.AssociationOfContact("sip:ue3@ue3.example.test:5060"); !ok || got.Source != host.Source {
		t.Errorf("AssociationOfContact with the host in lower case = %+v, %v", got, ok)
	}
	if got, ok := b.AssociationOfPolicySession(as.SignallingSession); !ok || got.Source != ueSource {
		t.Errorf("AssociationOfPolicySession = %+v, %v", got, ok)
	}
}

// pcscfAssociationChangesIndexes: a refresh from a new contact, or a
// new policy session, leaves nothing to be found by the old ones.
func pcscfAssociationChangesIndexes(t *testing.T, a, b pcscf.Store) {
	as := association()
	a.PutAssociation(as)
	as.Contact = "sip:ue1-new@192.0.2.10:5060"
	as.SignallingSession = "pcscf;2;sig"
	b.PutAssociation(as)
	if _, ok := a.AssociationOfContact("sip:ue1@192.0.2.10:5060"); ok {
		t.Error("AssociationOfContact finds the association by its old contact")
	}
	if _, ok := a.AssociationOfPolicySession("pcscf;1;sig"); ok {
		t.Error("AssociationOfPolicySession finds the association by its old session")
	}
	if _, ok := a.AssociationOfContact(as.Contact); !ok {
		t.Error("AssociationOfContact misses the new contact")
	}
	if _, ok := a.AssociationOfPolicySession(as.SignallingSession); !ok {
		t.Error("AssociationOfPolicySession misses the new session")
	}
	as.SignallingSession = ""
	a.PutAssociation(as)
	if _, ok := b.AssociationOfPolicySession("pcscf;2;sig"); ok {
		t.Error("AssociationOfPolicySession after the session ended")
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
	if _, ok := a.UpdateDialog(d.Key, func(*pcscf.Dialog) { t.Error("change ran for a dialog that is not there") }); ok {
		t.Error("UpdateDialog of no dialog reported true")
	}
	a.PutDialog(d)
	got, ok := b.UpdateDialog(d.Key, func(d *pcscf.Dialog) { d.Confirmed = true; d.Caller.CSeq = 2 })
	if !ok || !got.Confirmed || got.Caller.CSeq != 2 {
		t.Fatalf("UpdateDialog = %+v, %v", got, ok)
	}
	got, ok = a.TakeDialog(d.Key)
	d.Confirmed, d.Caller.CSeq = true, 2
	if !ok || !sameJSON(t, got, d) {
		t.Fatalf("TakeDialog = %+v, %v; want %+v", got, ok, d)
	}
	// A dialog that has ended does not come back.
	if _, ok := b.UpdateDialog(d.Key, func(*pcscf.Dialog) {}); ok {
		t.Error("UpdateDialog after TakeDialog reported true")
	}
	if _, ok := b.TakeDialog(d.Key); ok {
		t.Error("a second TakeDialog got the dialog")
	}
}

func pcscfDialogOfPolicySession(t *testing.T, a, b pcscf.Store) {
	d := dialog()
	d.PolicySession = "pcscf;1;media"
	a.PutDialog(d)
	if got, ok := b.DialogOfPolicySession("pcscf;1;media"); !ok || got.Key != d.Key {
		t.Fatalf("DialogOfPolicySession = %+v, %v", got, ok)
	}
	// The PCRF took the resources: the dialog loses its session (as
	// release.go does), and a new one replaces it.
	b.UpdateDialog(d.Key, func(d *pcscf.Dialog) { d.PolicySession = "pcscf;2;media" })
	if _, ok := a.DialogOfPolicySession("pcscf;1;media"); ok {
		t.Error("DialogOfPolicySession finds the dialog by its old session")
	}
	if got, ok := a.DialogOfPolicySession("pcscf;2;media"); !ok || got.Key != d.Key {
		t.Errorf("DialogOfPolicySession of the new session = %+v, %v", got, ok)
	}
	a.TakeDialog(d.Key)
	if _, ok := b.DialogOfPolicySession("pcscf;2;media"); ok {
		t.Error("DialogOfPolicySession after TakeDialog")
	}
}

// pcscfConcurrentUpdates has two instances count requests of one
// dialog at once, as the requests of a dialog may pass two P-CSCF
// instances. No update may be lost.
func pcscfConcurrentUpdates(t *testing.T, a, b pcscf.Store) {
	d := dialog()
	d.Caller.CSeq = 0
	a.PutDialog(d)
	const n = 25
	parallel(2, func(i int) {
		s := a
		if i == 1 {
			s = b
		}
		for range n {
			s.UpdateDialog(d.Key, func(d *pcscf.Dialog) { d.Caller.CSeq++ })
		}
	})
	got, _ := a.UpdateDialog(d.Key, func(*pcscf.Dialog) {})
	if got.Caller.CSeq != 2*n {
		t.Errorf("CSeq = %d after %d updates", got.Caller.CSeq, 2*n)
	}
}

// pcscfOnlyOneTakes: the BYE, the PCRF's event and the ACK timer may
// all end one dialog at once, on different instances.
func pcscfOnlyOneTakes(t *testing.T, a, b pcscf.Store) {
	for range 10 {
		d := dialog()
		a.PutDialog(d)
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := s.TakeDialog(d.Key); ok {
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
	a.PutSecurityAssociations(sas)
	if got, ok := b.SecurityAssociations(ue); !ok || !sameJSON(t, got, sas) {
		t.Fatalf("SecurityAssociations = %+v, %v; want %+v", got, ok, sas)
	}
	waitOut()
	if _, ok := b.SecurityAssociations(ue); ok {
		t.Error("SecurityAssociations after they expired")
	}
	sas.Expires = time.Now().Add(time.Hour)
	b.PutSecurityAssociations(sas)
	a.DeleteSecurityAssociations(ue)
	if _, ok := b.SecurityAssociations(ue); ok {
		t.Error("SecurityAssociations after DeleteSecurityAssociations")
	}
}
