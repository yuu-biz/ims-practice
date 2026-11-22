package hss

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// The cases of the Store itself, on MemoryStore and on the adapters,
// are in internal/kvstore/storetest. The tests of this package drive
// the HSS through Cx, Sh and the provisioning API.

const tel = "tel:+12025550101"

func TestValidateRefusesAPublicIdentityListedTwice(t *testing.T) {
	err := Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "sip:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org"}, Password: "p"}.Validate()
	if err == nil {
		t.Error("a public identity listed twice was accepted")
	}
}

// TestDeregistrationForgetsTheSCSCF: once the S-CSCF has deregistered
// the user, removing the subscription sends no RTR, as for a user who
// never registered (TS 29.228 6.1.3.1). Where an RTR goes while the user
// is registered is TestRemovingARegisteredSubscriptionSendsRTR.
func TestDeregistrationForgetsTheSCSCF(t *testing.T) {
	h := newTestHSS()
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	sar(t, h, scscf, cx.UserDeregistration)
	scscfs := &fakeSCSCFs{}
	if w := do(t, h.Provisioning(scscfs), "DELETE", "/subscriptions/"+impi, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	if len(scscfs.rtrs) != 0 {
		t.Errorf("RTR for a user who was deregistered: %+v", scscfs.rtrs)
	}
}

func TestValidateRefusesRepositoryDataTwice(t *testing.T) {
	d := UtCredentialData(impi, realm, "p")
	s := Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p", RepositoryData: []sh.TransparentData{d, d}}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("Validate = %v", err)
	}
}
