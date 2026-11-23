package hss

import (
	"net/http"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// The tests of this file are TS 29.228 6.6.1: a provisioning request
// that changes the service profile of a user whom an S-CSCF serves has
// the HSS push the whole profile there, rather than leave the S-CSCF
// with the profile of the last registration.

const (
	defaultAS = "sip:ipsmgw.ims.example:5060"
	trialAS   = "sip:trial.ims.example:5060"
)

// profileCriterion sends every MESSAGE of a user to server, so that the
// profile a push carries is told by the server its criterion names.
func profileCriterion(server string) ifc.InitialFilterCriteria {
	return ifc.InitialFilterCriteria{
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "MESSAGE"}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: server},
	}
}

// newProfileHSS is an HSS with two service profiles, each naming one
// application server, and the test subscriber on the default one. Test
// users get the profile that testUsers names.
func newProfileHSS(t *testing.T, testUsers string) *HSS {
	t.Helper()
	store, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+12025550101"}, Password: "test-password-1"})
	if err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	return mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm},
		ServiceProfiles: map[string][]ifc.InitialFilterCriteria{
			"default": {profileCriterion(defaultAS)},
			"trial":   {profileCriterion(trialAS)},
		},
		DefaultServiceProfile:  "default",
		TestUserServiceProfile: testUsers,
	}, store, node)
}

// pushedServer is the application server of the criteria of the one PPR
// that the S-CSCFs got, with the checks of TS 29.228 Table 6.2.2.1 and
// 6.5.2.1 on the rest of the request.
func pushedServer(t *testing.T, scscfs *fakeSCSCFs) string {
	t.Helper()
	if len(scscfs.pprs) != 1 {
		t.Fatalf("%d PPRs, want 1", len(scscfs.pprs))
	}
	ppr := scscfs.pprs[0]
	// Table 6.2.2.1, Routing Information: the PPR goes to the S-CSCF
	// whose SAR stored the server name, by its Origin-Host.
	if scscfs.hosts[0] != hdr.OriginHost || ppr.DestinationHost != hdr.OriginHost || ppr.DestinationRealm != hdr.OriginRealm {
		t.Errorf("PPR sent to %s with Destination-Host %s and Destination-Realm %s, want the Origin of the SAR (%s)",
			scscfs.hosts[0], ppr.DestinationHost, ppr.DestinationRealm, hdr.OriginHost)
	}
	if ppr.UserName != impi || ppr.OriginHost != "hss."+realm || ppr.SessionID == "" {
		t.Errorf("PPR = %+v", ppr.Header)
	}
	profile, err := cx.ParseUserProfile(ppr.UserData)
	if err != nil {
		t.Fatalf("User-Data: %v", err)
	}
	// 6.5.2.1: the whole implicit registration set, with its service
	// profile.
	if len(profile.PublicIdentities) != 2 || profile.PublicIdentities[0] != impu {
		t.Errorf("the pushed profile holds %v, want the implicit registration set", profile.PublicIdentities)
	}
	if len(profile.InitialFilterCriteria) != 1 {
		t.Fatalf("the pushed profile has %d criteria, want 1", len(profile.InitialFilterCriteria))
	}
	return profile.InitialFilterCriteria[0].ApplicationServer.ServerName
}

// TestChangingTheProfileOfAServedUserPushesIt: whichever provisioning
// request gives the user another service profile, and whether the user
// is registered or in the unregistered state, the S-CSCF gets the new
// profile at once (TS 29.228 6.6.1).
func TestChangingTheProfileOfAServedUserPushesIt(t *testing.T) {
	put := `{"private_identity": "` + impi + `", "public_identities": ["` + impu + `", "tel:+12025550101"],
		"password": "test-password-1", "service_profile": "trial"}`
	tests := []struct {
		name       string
		testUsers  string
		assignment cx.ServerAssignmentType
		method     string
		path       string
		body       string
	}{
		{"a PATCH of the profile name", "default", cx.AssignRegistration, "PATCH", "/subscriptions/" + impi, `{"service_profile": "trial"}`},
		{"a PATCH of the mark", "trial", cx.AssignRegistration, "PATCH", "/subscriptions/" + impi, `{"test_user": true}`},
		{"a PUT", "default", cx.AssignRegistration, "PUT", "/subscriptions/" + impi, put},
		// 6.6.1: the unregistered state is one in which an S-CSCF keeps
		// the profile, so a change there is pushed too.
		{"a user in the unregistered state", "default", cx.UnregisteredUser, "PATCH", "/subscriptions/" + impi, `{"service_profile": "trial"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProfileHSS(t, tt.testUsers)
			if tt.assignment == cx.AssignRegistration {
				mar(t, h, scscf)
			}
			sar(t, h, scscf, tt.assignment)
			scscfs := &fakeSCSCFs{}
			if w := do(t, h.Provisioning(scscfs), tt.method, tt.path, tt.body); w.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", tt.method, w.Code, w.Body)
			}
			if got := pushedServer(t, scscfs); got != trialAS {
				t.Errorf("the pushed criterion names %q, want the server of the new profile, %q", got, trialAS)
			}
		})
	}
}

// TestProvisioningThatPushesNothing: the HSS pushes for a change of the
// profile that an S-CSCF holds, and for nothing else.
func TestProvisioningThatPushesNothing(t *testing.T) {
	sameProfile := `{"private_identity": "` + impi + `", "public_identities": ["` + impu + `", "tel:+12025550101"],
		"password": "test-password-1", "ut_password": "ut"}`
	tests := []struct {
		name      string
		testUsers string
		register  bool
		method    string
		path      string
		body      string
	}{
		// The mark chooses the profile, but here both names are the same
		// profile, so the S-CSCF holds what it holds.
		{"a mark that leaves the profile", "default", true, "PATCH", "/subscriptions/" + impi, `{"test_user": true}`},
		{"a PATCH that changes nothing", "default", true, "PATCH", "/subscriptions/" + impi, `{}`},
		// 6.6.1: nothing is pushed for a user that no S-CSCF serves.
		{"a user who is not registered", "trial", false, "PATCH", "/subscriptions/" + impi, `{"test_user": true}`},
		{"a PUT that leaves the profile", "default", true, "PUT", "/subscriptions/" + impi, sameProfile},
		{"a dry run", "default", true, "PUT", "/subscriptions/" + impi + "?dry_run=true", `{"private_identity": "` + impi +
			`", "public_identities": ["` + impu + `", "tel:+12025550101"], "password": "test-password-1", "service_profile": "trial"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProfileHSS(t, tt.testUsers)
			if tt.register {
				mar(t, h, scscf)
				sar(t, h, scscf, cx.AssignRegistration)
			}
			scscfs := &fakeSCSCFs{}
			if w := do(t, h.Provisioning(scscfs), tt.method, tt.path, tt.body); w.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", tt.method, w.Code, w.Body)
			}
			if len(scscfs.pprs) != 0 {
				t.Errorf("%d PPRs, want none", len(scscfs.pprs))
			}
		})
	}
}

// TestAPushTheSCSCFRefuses: the subscription keeps the profile the
// operator gave it, and the user stays registered. The HSS neither sets
// the user to Not Registered nor deregisters it (TS 29.228 6.2.2.1, the
// SIMPLIFIED of pushProfile).
func TestAPushTheSCSCFRefuses(t *testing.T) {
	h := newProfileHSS(t, "default")
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{result: diameter.Experimental(cx.DiameterErrorUserUnknown)}
	api := h.Provisioning(scscfs)
	if w := do(t, api, "PATCH", "/subscriptions/"+impi, `{"service_profile": "trial"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body)
	}
	if got := pushedServer(t, scscfs); got != trialAS {
		t.Errorf("the pushed criterion names %q, want %q", got, trialAS)
	}
	if got := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+impi, "")); got.ServiceProfile != "trial" ||
		got.RegistrationState != Registered.String() {
		t.Errorf("subscription after the refused push = %+v, want the new profile and the user still registered", got)
	}
	// The S-CSCF is still the one the HSS tells an I-CSCF about, so the
	// next registration brings the profile that this PPR did not.
	if a := uar(t, h, impi, impu, cx.Registration); a.Result != diameter.Experimental(cx.DiameterSubsequentRegistration) || a.ServerName != scscf {
		t.Errorf("UAR after the refused push: %+v", a)
	}
}

// TestPushesOfOneSubscriberAreSerialised: of two changes at once, the
// last PPR that the S-CSCF gets carries the profile that the HSS ends
// up with, so the S-CSCF is never left with the older of the two. Each
// PPR is built from the subscriber as it stands when it is sent, and one
// subscriber has one push at a time.
func TestPushesOfOneSubscriberAreSerialised(t *testing.T) {
	h := newProfileHSS(t, "default")
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	api := h.Provisioning(scscfs)
	var wg sync.WaitGroup
	for _, profile := range []string{"trial", "default"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := do(t, api, "PATCH", "/subscriptions/"+impi, `{"service_profile": "`+profile+`"}`); w.Code != http.StatusOK {
				t.Errorf("PATCH %s: %d %s", profile, w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	stored := decode[subscriptionResponse](t, do(t, api, "GET", "/subscriptions/"+impi, "")).ServiceProfile
	if stored == "" {
		stored = "default"
	}
	want := map[string]string{"default": defaultAS, "trial": trialAS}[stored]
	if len(scscfs.pprs) == 0 {
		t.Fatal("no PPR")
	}
	last, err := cx.ParseUserProfile(scscfs.pprs[len(scscfs.pprs)-1].UserData)
	if err != nil {
		t.Fatal(err)
	}
	if got := last.InitialFilterCriteria[0].ApplicationServer.ServerName; got != want {
		t.Errorf("the last of %d PPRs names %q, want the server of the stored profile %q, %q",
			len(scscfs.pprs), got, stored, want)
	}
}
