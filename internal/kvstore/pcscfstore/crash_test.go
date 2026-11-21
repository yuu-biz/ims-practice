package pcscfstore_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestAWriterThatStopsLeavesOnlyAMiss stops an instance after it wrote
// the index item of a new contact, before the association: the contact
// finds nothing, and the old one still finds the association.
func TestAWriterThatStopsLeavesOnlyAMiss(t *testing.T) {
	kv := kvstore.NewMemoryStore(nil)
	s := pcscfstore.New(kv, kvjson.Config{})
	a := pcscf.Association{Source: sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("192.0.2.10:5060")},
		Contact: "sip:ue1@192.0.2.10:5060", Expires: time.Now().Add(time.Hour)}
	s.PutAssociation(a)
	refresh := a
	refresh.Contact = "sip:ue1-new@192.0.2.10:5060"
	pcscfstore.New(storetest.StopAfter(kv, 1), kvjson.Config{}).PutAssociation(refresh)
	if _, ok := s.AssociationOfContact(refresh.Contact); ok {
		t.Error("the contact of an association that was never written finds it")
	}
	if _, ok := s.AssociationOfContact(a.Contact); !ok {
		t.Error("the contact of the stored association no longer finds it")
	}
}
