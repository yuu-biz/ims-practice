package hss

import (
	"net/http"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// TestEverySubscriptionGetsTheDefaultServiceProfile checks the profile
// in the SAA: the criteria of the service profile that
// DefaultServiceProfile names, and not those of another profile of the
// configuration, with the MSISDN of each subscriber in ServiceInfo. A
// subscriber added after the HSS started, as the provisioning API adds
// them, gets the same profile.
func TestEverySubscriptionGetsTheDefaultServiceProfile(t *testing.T) {
	register := ifc.InitialFilterCriteria{
		TriggerPoint: &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "REGISTER"}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:ipsmgw." + realm + ":5060",
			ServiceInfo: "<msisdn>" + MSISDNPlaceholder + "</msisdn>"},
	}
	// A second profile, which no subscription of this configuration
	// gets: the criteria of a profile reach the SAA by its name, not by
	// being in the configuration.
	message := ifc.InitialFilterCriteria{
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "MESSAGE"}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:trial." + realm + ":5060"},
	}
	store, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+1-202-555-0101"}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h := mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm},
		ServiceProfiles:       map[string][]ifc.InitialFilterCriteria{"default": {register}, "trial": {message}},
		DefaultServiceProfile: "default"}, store, node)
	added := `{"private_identity": "` + otherImp + `", "public_identities": ["sip:001010000000002@` + realm + `"], "password": "p"}`
	if w := do(t, h.Provisioning(&fakeSCSCFs{}), "POST", "/subscriptions", added); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}

	for _, tt := range []struct{ private, public, serviceInfo string }{
		{impi, impu, "<msisdn>12025550101</msisdn>"},
		// Without a tel URI the subscription has no MSISDN.
		{otherImp, "sip:001010000000002@" + realm, "<msisdn></msisdn>"},
	} {
		saa, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
			UserName: tt.private, PublicIdentities: []string{tt.public}, ServerName: scscf, ServerAssignmentType: cx.AssignRegistration}.Message()))
		if err != nil || !saa.Result.Success() {
			t.Fatalf("SAA for %s: %+v, %v", tt.private, saa, err)
		}
		profile, err := cx.ParseUserProfile(saa.UserData)
		if err != nil {
			t.Fatal(err)
		}
		if len(profile.InitialFilterCriteria) != 1 {
			t.Fatalf("%s has %d criteria, want 1", tt.private, len(profile.InitialFilterCriteria))
		}
		server := profile.InitialFilterCriteria[0].ApplicationServer
		if server.ServerName != register.ApplicationServer.ServerName {
			t.Errorf("%s: the criterion names %q, want the one of the default profile, %q",
				tt.private, server.ServerName, register.ApplicationServer.ServerName)
		}
		if server.ServiceInfo != tt.serviceInfo {
			t.Errorf("%s: ServiceInfo = %q, want %q", tt.private, server.ServiceInfo, tt.serviceInfo)
		}
	}
	if got := h.config.ServiceProfiles["default"][0].ApplicationServer.ServiceInfo; got != register.ApplicationServer.ServiceInfo {
		t.Errorf("the configuration changed to %q", got)
	}
}
