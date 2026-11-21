package hss

import (
	"errors"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

const tel = "tel:+12025550101"

func TestMemoryStoreFindsASubscriberByEachIdentity(t *testing.T) {
	s, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// RFC 3261 19.1.4: the scheme and the host compare without case,
	// the user part does not. A NAI compares without case.
	for _, id := range []string{impu, tel, "SIP:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"} {
		if got, ok := s.SubscriberOf(id); !ok || got.PrivateIdentity != impi {
			t.Errorf("SubscriberOf(%s) = %+v, %v", id, got, ok)
		}
	}
	if _, ok := s.SubscriberOf("sip:001010000000001X@" + realm); ok {
		t.Error("found a subscriber for a user part it does not have")
	}
	if got, ok := s.Subscriber("001010000000001@IMS.MNC001.MCC001.3GPPNETWORK.ORG"); !ok || got.PrivateIdentity != impi {
		t.Errorf("Subscriber by the private identity in upper case = %+v, %v", got, ok)
	}
}

func TestMemoryStoreRefusesAnIdentityOfAnotherSubscription(t *testing.T) {
	s, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []Subscription{
		{PrivateIdentity: impi, PublicIdentities: []string{"sip:new@" + realm}, Password: "p"},
		{PrivateIdentity: otherImp, PublicIdentities: []string{"sip:new@" + realm, tel}, Password: "p"},
	} {
		if err := s.Add(sub); !errors.Is(err, ErrIdentityTaken) {
			t.Errorf("Add(%+v) = %v, want ErrIdentityTaken", sub, err)
		}
	}
	// The refused subscription left nothing behind.
	if _, ok := s.SubscriberOf("sip:new@" + realm); ok {
		t.Error("a refused subscription was indexed")
	}
}

func TestMemoryStoreDeleteDropsTheIndex(t *testing.T) {
	s, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.Delete(impi)
	if !ok || got.PrivateIdentity != impi {
		t.Fatalf("Delete = %+v, %v", got, ok)
	}
	if _, ok := s.SubscriberOf(tel); ok {
		t.Error("a public identity of the deleted subscription still finds it")
	}
	// The identities are free for a new subscription.
	if err := s.Add(Subscription{PrivateIdentity: otherImp, PublicIdentities: []string{tel}, Password: "p"}); err != nil {
		t.Errorf("Add after Delete: %v", err)
	}
	s.Put(Subscriber{Subscription: Subscription{PrivateIdentity: impi}, State: Registered})
	if _, ok := s.Subscriber(impi); ok {
		t.Error("Put brought back a deleted subscriber")
	}
}

func TestMemoryStoreListIsBoundedAndOrdered(t *testing.T) {
	s, err := NewMemoryStore(
		Subscription{PrivateIdentity: "c@" + realm, PublicIdentities: []string{"sip:c@" + realm}, Password: "p"},
		Subscription{PrivateIdentity: "a@" + realm, PublicIdentities: []string{"sip:a@" + realm}, Password: "p"},
		Subscription{PrivateIdentity: "b@" + realm, PublicIdentities: []string{"sip:b@" + realm}, Password: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := s.List(2)
	if len(got) != 2 || got[0].PrivateIdentity != "a@"+realm || got[1].PrivateIdentity != "b@"+realm {
		t.Errorf("List(2) = %+v", got)
	}
}

func TestValidateRefusesAPublicIdentityListedTwice(t *testing.T) {
	err := Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"}, Password: "p"}.Validate()
	if err == nil {
		t.Error("a public identity listed twice was accepted")
	}
}

// TestAssignmentStoresTheSCSCFOrigin checks that the HSS keeps where the
// S-CSCF is on Diameter, which a Registration-Termination-Request needs
// (TS 29.228 Table 6.1.3.1).
func TestAssignmentStoresTheSCSCFOrigin(t *testing.T) {
	h, store := newTestHSSWithStore()
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	got, _ := store.Subscriber(impi)
	if got.State != Registered || got.SCSCF.OriginHost != hdr.OriginHost || got.SCSCF.OriginRealm != hdr.OriginRealm {
		t.Errorf("after the SAR: %+v", got)
	}
	sar(t, h, scscf, cx.UserDeregistration)
	if got, _ := store.Subscriber(impi); got.State != NotRegistered || got.SCSCF.OriginHost != "" {
		t.Errorf("after the deregistration: %+v", got)
	}
}

// TestMemoryStoreStoresTheProvisionedRepositoryData: the operator's
// first data is version 0 (TS 29.328 6.1.2.1), and the subscription
// keeps no copy that could disagree with what Sh changes later.
func TestMemoryStoreStoresTheProvisionedRepositoryData(t *testing.T) {
	data := []sh.TransparentData{
		{ServiceIndication: "MMTEL-Services", SequenceNumber: 7, ServiceData: &sh.ServiceData{XML: []byte("<a/>")}},
		UtCredentialData(impi, realm, "test-password-1"),
	}
	s, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p", RepositoryData: data})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.RepositoryData(impi, "MMTEL-Services")
	if !ok || got.SequenceNumber != 0 || string(got.ServiceData.XML) != "<a/>" {
		t.Errorf("MMTEL-Services = %+v, %v", got, ok)
	}
	if _, ok := s.RepositoryData(impi, xcap.CredentialServiceIndication); !ok {
		t.Error("no Ut credential")
	}
	if sub, _ := s.Subscriber(impi); sub.RepositoryData != nil {
		t.Errorf("the stored subscription keeps %v", sub.RepositoryData)
	}
}

func TestValidateRefusesRepositoryDataTwice(t *testing.T) {
	d := UtCredentialData(impi, realm, "p")
	s := Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p", RepositoryData: []sh.TransparentData{d, d}}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("Validate = %v", err)
	}
}
