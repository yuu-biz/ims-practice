package pcscfstore_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. The instance is told of the failure,
// never that an item is missing, so that the P-CSCF can answer it; once
// the main item is written or removed, what is left is a miss.

func association() pcscf.Association {
	return pcscf.Association{Source: sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.10:5060")},
		Contact: "sip:ue1@192.0.2.10:5060", SignallingSession: "pcscf;1;sig",
		Expires: time.Now().Add(time.Hour), KeepUntil: time.Now().Add(time.Hour + time.Minute)}
}

func dialog() pcscf.Dialog {
	return pcscf.Dialog{Key: pcscf.DialogKey{CallID: "call-1", UETag: "ue"}, PolicySession: "pcscf;1;media",
		Caller: pcscf.Party{NameAddr: "<sip:ue1>;tag=ue", CSeq: 1}}
}

// TestAStoppedInstanceIsToldOfTheFailure: every operation of an instance
// whose store has stopped returns the failure.
func TestAStoppedInstanceIsToldOfTheFailure(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := pcscfstore.New(kv, cfg)
			a, d := association(), dialog()
			sas := pcscf.SecurityAssociations{UE: netip.MustParseAddrPort("192.0.2.10:5062"), Expires: time.Now().Add(time.Hour)}
			if err := other.PutAssociation(ctx, a); err != nil {
				t.Fatal(err)
			}
			if err := other.PutDialog(ctx, d); err != nil {
				t.Fatal(err)
			}
			if err := other.PutSecurityAssociations(ctx, sas); err != nil {
				t.Fatal(err)
			}
			// The first write stops the instance, and every operation fails
			// from then on.
			stopped := pcscfstore.New(storetest.StopAfter(kv, 0), cfg)

			ended := a
			ended.Expires = time.Now()
			if err := stopped.PutAssociation(ctx, ended); err == nil {
				t.Error("PutAssociation returned no error")
			}
			if err := stopped.DeleteAssociation(ctx, a.Source); err == nil {
				t.Error("DeleteAssociation returned no error")
			}
			if _, ok, err := stopped.Association(ctx, a.Source); err == nil {
				t.Errorf("Association = %v, no error", ok)
			}
			if _, ok, err := stopped.AssociationOfContact(ctx, a.Contact); err == nil {
				t.Errorf("AssociationOfContact = %v, no error", ok)
			}
			if _, ok, err := stopped.AssociationOfPolicySession(ctx, a.SignallingSession); err == nil {
				t.Errorf("AssociationOfPolicySession = %v, no error", ok)
			}
			if err := stopped.PutDialog(ctx, pcscf.Dialog{Key: pcscf.DialogKey{CallID: "call-2", UETag: "ue"}}); err == nil {
				t.Error("PutDialog returned no error")
			}
			if _, ok, err := stopped.UpdateDialog(ctx, d.Key, func(d *pcscf.Dialog) { d.Confirmed = true }); err == nil {
				t.Errorf("UpdateDialog = %v, no error", ok)
			}
			if _, ok, err := stopped.TakeDialog(ctx, d.Key); err == nil {
				t.Errorf("TakeDialog = %v, no error", ok)
			}
			if _, ok, err := stopped.DialogOfPolicySession(ctx, d.PolicySession); err == nil {
				t.Errorf("DialogOfPolicySession = %v, no error", ok)
			}
			if _, ok, err := stopped.SecurityAssociations(ctx, sas.UE); err == nil {
				t.Errorf("SecurityAssociations = %v, no error", ok)
			}
			if err := stopped.PutSecurityAssociations(ctx, sas); err == nil {
				t.Error("PutSecurityAssociations returned no error")
			}
			if err := stopped.DeleteSecurityAssociations(ctx, sas.UE); err == nil {
				t.Error("DeleteSecurityAssociations returned no error")
			}

			// What the stopped instance failed to change is as it was.
			if got, ok, err := other.AssociationOfContact(ctx, a.Contact); err != nil || !ok || !got.Expires.Equal(a.Expires) {
				t.Errorf("AssociationOfContact on the other instance = %+v, %v, %v; want the association as it was", got, ok, err)
			}
			if got, ok, err := other.DialogOfPolicySession(ctx, d.PolicySession); err != nil || !ok || got.Confirmed {
				t.Errorf("DialogOfPolicySession on the other instance = %+v, %v, %v; want the dialog as it was", got, ok, err)
			}
			if _, ok, err := other.SecurityAssociations(ctx, sas.UE); err != nil || !ok {
				t.Errorf("SecurityAssociations on the other instance = %v, %v; want the entry", ok, err)
			}
		})
	}
}

// TestAWriterThatStopsLeavesOnlyAMiss stops an instance after it wrote
// the index item of a new contact, before the association: it is told
// so, the contact finds nothing, and the old one still finds the
// association.
func TestAWriterThatStopsLeavesOnlyAMiss(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			s := pcscfstore.New(kv, cfg)
			a := association()
			if err := s.PutAssociation(ctx, a); err != nil {
				t.Fatal(err)
			}
			refresh := a
			refresh.Contact = "sip:ue1-new@192.0.2.10:5060"
			if err := pcscfstore.New(storetest.StopAfter(kv, 1), cfg).PutAssociation(ctx, refresh); err == nil {
				t.Fatal("PutAssociation that stopped half way returned no error")
			}
			if _, ok, err := s.AssociationOfContact(ctx, refresh.Contact); err != nil || ok {
				t.Errorf("the contact of an association that was never written finds it: %v, %v", ok, err)
			}
			if _, ok, err := s.AssociationOfContact(ctx, a.Contact); err != nil || !ok {
				t.Errorf("the contact of the stored association no longer finds it: %v, %v", ok, err)
			}
		})
	}
}

// TestADeleteThatStopsLeavesOnlyAMiss stops an instance after it removed
// the association, before its index items. The association is gone, so
// the instance is told that DeleteAssociation succeeded, and the index
// items that stay find nothing.
func TestADeleteThatStopsLeavesOnlyAMiss(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := pcscfstore.New(kv, cfg)
			a := association()
			if err := other.PutAssociation(ctx, a); err != nil {
				t.Fatal(err)
			}
			if err := pcscfstore.New(storetest.StopAfter(kv, 1), cfg).DeleteAssociation(ctx, a.Source); err != nil {
				t.Fatalf("DeleteAssociation that removed the association = %v", err)
			}
			if _, ok, err := other.AssociationOfContact(ctx, a.Contact); err != nil || ok {
				t.Errorf("AssociationOfContact after the delete = %v, %v; want none", ok, err)
			}
			if _, ok, err := other.AssociationOfPolicySession(ctx, a.SignallingSession); err != nil || ok {
				t.Errorf("AssociationOfPolicySession after the delete = %v, %v; want none", ok, err)
			}
		})
	}
}

// TestADialogThatStopsHalfWayIsNotFound: an instance that stops after
// it wrote the index item of a policy session, before the dialog, is
// told so. The index item finds no dialog that holds the session: for a
// new dialog none, and for a dialog that UpdateDialog was to move to the
// session, the dialog as it was, which DialogOfPolicySession still finds
// by its old session.
func TestADialogThatStopsHalfWayIsNotFound(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := pcscfstore.New(kv, cfg)
			d := dialog()
			if err := pcscfstore.New(storetest.StopAfter(kv, 1), cfg).PutDialog(ctx, d); err == nil {
				t.Fatal("PutDialog that stopped half way returned no error")
			}
			if _, ok, err := other.DialogOfPolicySession(ctx, d.PolicySession); err != nil || ok {
				t.Fatalf("DialogOfPolicySession of a dialog never written = %v, %v; want none", ok, err)
			}

			if err := other.PutDialog(ctx, d); err != nil {
				t.Fatal(err)
			}
			_, ok, err := pcscfstore.New(storetest.StopAfter(kv, 1), cfg).UpdateDialog(ctx, d.Key, func(d *pcscf.Dialog) { d.PolicySession = "pcscf;2;media" })
			if err == nil {
				t.Fatalf("UpdateDialog that stopped half way = %v, no error", ok)
			}
			if _, ok, err := other.DialogOfPolicySession(ctx, "pcscf;2;media"); err != nil || ok {
				t.Errorf("DialogOfPolicySession of the session never written = %v, %v; want none", ok, err)
			}
			if got, ok, err := other.DialogOfPolicySession(ctx, d.PolicySession); err != nil || !ok || got.Key != d.Key {
				t.Errorf("DialogOfPolicySession of the old session = %+v, %v, %v; want the dialog", got, ok, err)
			}
		})
	}
}

// TestATakeThatStopsReturnsTheDialog: an instance that stops after it
// removed the dialog, before the index item of its policy session, has
// taken the dialog, and gets it to end what it holds. The index item
// that stays finds nothing.
func TestATakeThatStopsReturnsTheDialog(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := pcscfstore.New(kv, cfg)
			d := dialog()
			if err := other.PutDialog(ctx, d); err != nil {
				t.Fatal(err)
			}
			got, ok, err := pcscfstore.New(storetest.StopAfter(kv, 1), cfg).TakeDialog(ctx, d.Key)
			if err != nil || !ok || got.Key != d.Key {
				t.Fatalf("TakeDialog that removed the dialog = %+v, %v, %v; want the dialog", got, ok, err)
			}
			if _, ok, err := other.DialogOfPolicySession(ctx, d.PolicySession); err != nil || ok {
				t.Errorf("DialogOfPolicySession after the take = %v, %v; want none", ok, err)
			}
			if _, ok, err := other.TakeDialog(ctx, d.Key); err != nil || ok {
				t.Errorf("a second TakeDialog = %v, %v; want none", ok, err)
			}
		})
	}
}
