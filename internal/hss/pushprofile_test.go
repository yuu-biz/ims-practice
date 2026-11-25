package hss

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sh"
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
		ServiceProfiles: map[string]ServiceProfile{
			"default": {Criteria: []ifc.InitialFilterCriteria{profileCriterion(defaultAS)}},
			"trial":   {Criteria: []ifc.InitialFilterCriteria{profileCriterion(trialAS)}},
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
		// 6.5.2.1 keeps a new default identity out of the PPR only for a
		// set in the Registered state.
		{"a new default identity in the unregistered state", "default", cx.UnregisteredUser, "PUT", "/subscriptions/" + impi,
			`{"private_identity": "` + impi + `", "public_identities": ["` + impu + `", "tel:+12025550101"], "barred_identities": ["` + impu +
				`"], "password": "test-password-1", "service_profile": "trial"}`},
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
		// A new default public identity deregisters the user instead
		// (TestAPutThatChangesTheDefaultIdentityDeregisters).
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

// TestAPutThatChangesTheDefaultIdentityDeregisters: TS 29.228 6.5.2.1
// keeps a new default public identity of a registered set out of the
// PPR and has the HSS deregister the set with an RTR instead, and has
// the user Not Registered, so a later change pushes nothing. While an
// identity of the set is not barred, the subscription stays valid and
// the RTR carries SERVER_CHANGE, with which the S-CSCF asks the user to
// register again (6.1.3.1); the next UAR is a first registration. When
// every identity is barred it carries PERMANENT_TERMINATION, and the
// next UAR is refused (6.1.1.1 step 4).
func TestAPutThatChangesTheDefaultIdentityDeregisters(t *testing.T) {
	const tel = "tel:+12025550101"
	identities := `"public_identities": ["` + impu + `", "` + tel + `"]`
	tests := []struct {
		name string
		// before is the body of a PUT before the user registers, if any.
		before string
		body   string
		reason cx.ReasonCode
		// registers is the identity of the next UAR, and uaa its answer.
		registers string
		uaa       diameter.Result
	}{
		{"a PUT that moves the default identity", "",
			`"public_identities": ["` + tel + `", "` + impu + `"]`, cx.ServerChange, impu, diameter.Experimental(cx.DiameterFirstRegistration)},
		{"a PUT that bars the default identity", "",
			identities + `, "barred_identities": ["` + impu + `"], "service_profile": "trial"`, cx.ServerChange, impu, diameter.Experimental(cx.DiameterFirstRegistration)},
		{"a PUT that lifts the barring of an identity before the default one", identities + `, "barred_identities": ["` + impu + `"]`,
			identities, cx.ServerChange, impu, diameter.Experimental(cx.DiameterFirstRegistration)},
		{"a PUT that takes the default identity away", "",
			`"public_identities": ["` + tel + `"]`, cx.ServerChange, tel, diameter.Experimental(cx.DiameterFirstRegistration)},
		{"a PUT that bars every identity", "",
			identities + `, "barred_identities": ["` + impu + `", "` + tel + `"]`, cx.PermanentTermination, impu, diameter.Base(diameter.DiameterAuthorizationRejected)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProfileHSS(t, "default")
			scscfs := &fakeSCSCFs{}
			api := h.Provisioning(scscfs)
			put := func(fields string) {
				t.Helper()
				body := `{"private_identity": "` + impi + `", ` + fields + `, "password": "test-password-1"}`
				if w := do(t, api, "PUT", "/subscriptions/"+impi, body); w.Code != http.StatusOK {
					t.Fatalf("PUT: %d %s", w.Code, w.Body)
				}
			}
			if tt.before != "" {
				put(tt.before)
			}
			mar(t, h, scscf)
			sar(t, h, scscf, cx.AssignRegistration)
			put(tt.body)
			if len(scscfs.pprs) != 0 || len(scscfs.rtrs) != 1 {
				t.Fatalf("%d PPRs and %d RTRs, want an RTR alone", len(scscfs.pprs), len(scscfs.rtrs))
			}
			if rtr := scscfs.rtrs[0]; rtr.UserName != impi || len(rtr.PublicIdentities) != 0 ||
				rtr.DeregistrationReason.ReasonCode != tt.reason || rtr.DestinationHost != hdr.OriginHost {
				t.Errorf("RTR = %+v, want %s of the private identity to the S-CSCF of the SAR", rtr, tt.reason)
			}
			u, _, err := h.store.Subscriber(context.Background(), impi)
			if err != nil || u.State != NotRegistered || u.ServerName != "" {
				t.Errorf("after the RTR the user is %s with server name %q (%v), want Not Registered with none", u.State, u.ServerName, err)
			}
			if w := do(t, api, "PATCH", "/subscriptions/"+impi, `{"service_profile": "trial"}`); w.Code != http.StatusOK {
				t.Fatalf("PATCH: %d %s", w.Code, w.Body)
			}
			if len(scscfs.pprs) != 0 {
				t.Errorf("%d PPRs after the deregistration, want none", len(scscfs.pprs))
			}
			if a := uar(t, h, impi, tt.registers, cx.Registration); a.Result != tt.uaa {
				t.Errorf("UAA of the next registration = %s, want %s", a.Result, tt.uaa)
			}
		})
	}
}

// TestAPutThatRespellsTheDefaultIdentityKeepsTheRegistration: another
// form of the same URI is the same public identity, so a PUT that
// writes the default identity so leaves it the default and deregisters
// nobody.
func TestAPutThatRespellsTheDefaultIdentityKeepsTheRegistration(t *testing.T) {
	h := newProfileHSS(t, "default")
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	respelled := strings.Replace(impu, realm, strings.ToUpper(realm), 1)
	body := `{"private_identity": "` + impi + `", "public_identities": ["` + respelled + `", "tel:+12025550101"], "password": "test-password-1"}`
	if w := do(t, h.Provisioning(scscfs), "PUT", "/subscriptions/"+impi, body); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	if len(scscfs.rtrs) != 0 {
		t.Errorf("%d RTRs for a default identity written in another form, want none", len(scscfs.rtrs))
	}
	if u, _, err := h.store.Subscriber(context.Background(), impi); err != nil || u.State != Registered {
		t.Errorf("after the PUT the user is %s (%v), want Registered", u.State, err)
	}
}

// repositoryDataFails is a store whose writes of repository data fail.
type repositoryDataFails struct{ *MemoryStore }

func (repositoryDataFails) CompareAndSwapRepositoryData(context.Context, string, *sh.TransparentData, sh.TransparentData) (bool, error) {
	return false, errors.New("the store failed")
}

// TestTheRTRFollowsTheWriteWhateverComesAfter: once the HSS has written
// the user Not Registered with the new default identity, the RTR goes,
// as a PUT sent again would find the user Not Registered and send none.
// Neither a failure of the rest of the request nor a client that has
// gone away keeps it back.
func TestTheRTRFollowsTheWriteWhateverComesAfter(t *testing.T) {
	moved := `{"private_identity": "` + impi + `", "public_identities": ["tel:+12025550101", "` + impu + `"], "password": "test-password-1"`
	tests := []struct {
		name   string
		failed bool
		body   string
		code   int
	}{
		{"the repository data cannot be written", true, moved + `, "ut_password": "ut"}`, http.StatusInternalServerError},
		{"the client has gone", false, moved + `}`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memory, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+12025550101"}, Password: "test-password-1"})
			if err != nil {
				t.Fatal(err)
			}
			var store Store = memory
			if tt.failed {
				store = repositoryDataFails{memory}
			}
			node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
			h := mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, node)
			mar(t, h, scscf)
			sar(t, h, scscf, cx.AssignRegistration)
			scscfs := &fakeSCSCFs{}
			ctx, cancel := context.WithCancel(context.Background())
			if !tt.failed {
				cancel()
			}
			defer cancel()
			r := httptest.NewRequest("PUT", "/subscriptions/"+impi, strings.NewReader(tt.body)).WithContext(ctx)
			w := httptest.NewRecorder()
			h.Provisioning(scscfs).ServeHTTP(w, r)
			if w.Code != tt.code {
				t.Fatalf("PUT: %d %s, want %d", w.Code, w.Body, tt.code)
			}
			if len(scscfs.rtrs) != 1 {
				t.Errorf("%d RTRs, want the one of the new default identity", len(scscfs.rtrs))
			}
		})
	}
}

// TestAPutThatChangesTheIdentitiesPushesThem: the service profile stays,
// but the user profile changes, so the S-CSCF gets it at once. A barred
// identity may only register (TS 29.228 B.2.1), so an S-CSCF left with
// the old profile would still serve it; an identity added to the set is
// pushed too (6.5.2.1).
func TestAPutThatChangesTheIdentitiesPushesThem(t *testing.T) {
	const added = "sip:+12025550101@" + realm
	tests := []struct {
		name       string
		identities string
		barred     string
		want       cx.UserProfile
	}{
		{"a barred identity", `["` + impu + `", "tel:+12025550101"]`, `["tel:+12025550101"]`,
			cx.UserProfile{PublicIdentities: []string{impu, "tel:+12025550101"}, BarredIdentities: []string{"tel:+12025550101"}}},
		{"an identity added to the set", `["` + impu + `", "tel:+12025550101", "` + added + `"]`, `[]`,
			cx.UserProfile{PublicIdentities: []string{impu, "tel:+12025550101", added}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newProfileHSS(t, "default")
			mar(t, h, scscf)
			sar(t, h, scscf, cx.AssignRegistration)
			scscfs := &fakeSCSCFs{}
			body := `{"private_identity": "` + impi + `", "public_identities": ` + tt.identities +
				`, "barred_identities": ` + tt.barred + `, "password": "test-password-1"}`
			if w := do(t, h.Provisioning(scscfs), "PUT", "/subscriptions/"+impi, body); w.Code != http.StatusOK {
				t.Fatalf("PUT: %d %s", w.Code, w.Body)
			}
			if len(scscfs.pprs) != 1 {
				t.Fatalf("%d PPRs, want 1", len(scscfs.pprs))
			}
			got, err := cx.ParseUserProfile(scscfs.pprs[0].UserData)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.PublicIdentities, tt.want.PublicIdentities) || !slices.Equal(got.BarredIdentities, tt.want.BarredIdentities) {
				t.Errorf("pushed identities %v, barred %v; want %v, barred %v",
					got.PublicIdentities, got.BarredIdentities, tt.want.PublicIdentities, tt.want.BarredIdentities)
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

// TestAChangeDuringASlowPushStillReachesTheSCSCF: the PPA of a first
// change takes longer than lockWait. A second change made meanwhile
// waits for that push rather than give up, and then sends the second
// profile, so the S-CSCF ends up with it.
func TestAChangeDuringASlowPushStillReachesTheSCSCF(t *testing.T) {
	h := newProfileHSS(t, "default")
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	release := make(chan struct{})
	scscfs := &fakeSCSCFs{beforePPA: func(n int) {
		if n == 1 {
			<-release
		}
	}}
	api := h.Provisioning(scscfs)
	codes := make(chan int, 2)
	go func() {
		codes <- do(t, api, "PATCH", "/subscriptions/"+impi, `{"service_profile": "trial"}`).Code
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		scscfs.mu.Lock()
		n := len(scscfs.pprs)
		scscfs.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gave up waiting 5 s for the first PPR")
		}
	}
	go func() {
		codes <- do(t, api, "PATCH", "/subscriptions/"+impi, `{"service_profile": "default"}`).Code
	}()
	// Longer than lockWait, as a PPA may take up to the Diameter answer
	// timer.
	time.Sleep(lockWait + 500*time.Millisecond)
	close(release)
	for range 2 {
		if code := <-codes; code != http.StatusOK {
			t.Fatalf("PATCH: %d", code)
		}
	}
	if len(scscfs.pprs) != 2 {
		t.Fatalf("%d PPRs, want 2: the first change, then the second", len(scscfs.pprs))
	}
	last, err := cx.ParseUserProfile(scscfs.pprs[1].UserData)
	if err != nil {
		t.Fatal(err)
	}
	if got := last.InitialFilterCriteria[0].ApplicationServer.ServerName; got != defaultAS {
		t.Errorf("the last PPR names %q, want the server of the second change, %q", got, defaultAS)
	}
}

// TestAPushOutlivesTheClient: a client that closes its side of the
// connection once the request is sent cancels the context of the
// request. The change is stored, so its PPR still goes.
func TestAPushOutlivesTheClient(t *testing.T) {
	h := newProfileHSS(t, "default")
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequestWithContext(ctx, "PATCH", "/subscriptions/"+impi, strings.NewReader(`{"service_profile": "trial"}`))
	w := httptest.NewRecorder()
	h.Provisioning(scscfs).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", w.Code, w.Body)
	}
	if got := pushedServer(t, scscfs); got != trialAS {
		t.Errorf("the pushed criterion names %q, want %q", got, trialAS)
	}
}

// TestARemovalOutlivesTheClient: the RTR of a removal goes to the
// S-CSCF even when the client has closed its side of the connection,
// which cancels the context of the request (TS 29.228 6.1.3).
func TestARemovalOutlivesTheClient(t *testing.T) {
	h := newProfileHSS(t, "default")
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	h.Provisioning(scscfs).ServeHTTP(w, httptest.NewRequestWithContext(ctx, "DELETE", "/subscriptions/"+impi, nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	if len(scscfs.rtrs) != 1 {
		t.Errorf("%d RTRs, want 1 to the S-CSCF of the removed user", len(scscfs.rtrs))
	}
}
