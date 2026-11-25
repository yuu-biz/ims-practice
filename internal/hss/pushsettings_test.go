package hss

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// The tests of this file are TS 29.228 6.6.1 for the settings that name
// the service profile of a kind of subscription: a change of either name
// gives another user profile to every subscription that name decides
// for, and the HSS pushes the complete profile to the S-CSCF of each of
// those whose profile an S-CSCF has. They drive the HSS through the
// maintenance API of its node, as the OAM server does.

// The subscribers that settingsHSS does not have: one that names a
// service profile of its own, which neither setting decides, and one
// that no S-CSCF serves.
const (
	namedIMPI    = "001010000000011@" + realm
	namedIMPU    = "sip:001010000000011@" + realm
	unservedIMPI = "001010000000012@" + realm
	unservedIMPU = "sip:001010000000012@" + realm
)

// pushNetwork is settingsHSS with those two subscribers added, the
// S-CSCF holding the user profile of three of the four, and the
// S-CSCFs as the HSS's peers, which the Diameter server of the node is
// (cmd/ims). The test user that names its own profile is in the
// unregistered state, the other served ones are registered: 6.6.1 has
// the HSS push to both.
func pushNetwork(t *testing.T) (*HSS, *maintenance.Node, *fakeSCSCFs) {
	t.Helper()
	h, api := settingsHSS(t)
	addSubscription(t, h, Subscription{PrivateIdentity: namedIMPI, PublicIdentities: []string{namedIMPU},
		Password: "test-password-11", TestUser: true, ServiceProfile: "trial"})
	addSubscription(t, h, Subscription{PrivateIdentity: unservedIMPI, PublicIdentities: []string{unservedIMPU},
		Password: "test-password-12", TestUser: true})
	serve(t, h, impi, impu, cx.AssignRegistration)
	serve(t, h, testIMPI, testIMPU, cx.AssignRegistration)
	serve(t, h, namedIMPI, namedIMPU, cx.UnregisteredUser)
	scscfs := &fakeSCSCFs{}
	h.SetPeers(scscfs)
	return h, api, scscfs
}

func addSubscription(t *testing.T, h *HSS, sub Subscription) {
	t.Helper()
	if err := h.store.Add(t.Context(), sub); err != nil {
		t.Fatalf("Add(%s): %v", sub.PrivateIdentity, err)
	}
}

// serve has an S-CSCF take the user profile of a subscriber, as the SAR
// of a registration, or of a request to a user that is not registered,
// does.
func serve(t *testing.T, h *HSS, private, public string, typ cx.ServerAssignmentType) {
	t.Helper()
	a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
		UserName: private, PublicIdentities: []string{public}, ServerName: scscf, ServerAssignmentType: typ}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Result.Success() {
		t.Fatalf("SAA for %s: %+v", private, a.Result)
	}
}

// pushedTo waits for the S-CSCFs to have taken n PPRs and then stops
// the HSS, which lets no further push begin and waits for the one on
// its way (HSS.Close), so that what came is all that comes; stops are
// the stops of the other instances that push to the same S-CSCFs. It
// returns the private identity of each PPR with the application server
// its criteria name, which says which service profile it carried.
func pushedTo(t *testing.T, h *HSS, scscfs *fakeSCSCFs, n int, stops ...func()) map[string]string {
	t.Helper()
	// The pushes run behind the answer to the maintenance request, so
	// they are waited for rather than found there already.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		scscfs.mu.Lock()
		got := len(scscfs.pprs)
		scscfs.mu.Unlock()
		if got >= n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting 5 s for %d PPRs; %d came", n, got)
		}
	}
	// A moment for a PPR that should not have gone at all to arrive
	// before Close ends the pushes that have not sent one yet.
	time.Sleep(50 * time.Millisecond)
	h.Close()
	for _, stop := range stops {
		stop()
	}
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	pushed := map[string]string{}
	for _, ppr := range scscfs.pprs {
		profile, err := cx.ParseUserProfile(ppr.UserData)
		if err != nil {
			t.Fatalf("the User-Data of the PPR for %s: %v", ppr.UserName, err)
		}
		if len(profile.InitialFilterCriteria) != 1 {
			t.Fatalf("the pushed profile of %s has %d criteria, want the one of a single profile",
				ppr.UserName, len(profile.InitialFilterCriteria))
		}
		if _, twice := pushed[ppr.UserName]; twice {
			t.Errorf("%s got more than one PPR", ppr.UserName)
		}
		pushed[ppr.UserName] = profile.InitialFilterCriteria[0].ApplicationServer.ServerName
	}
	return pushed
}

// TestChangingAProfileNameSettingPushesItsSubscribers: the setting of
// the test users decides for the test user alone, and the default
// setting for the ordinary subscriber alone. Neither reaches the
// subscription that names a profile of its own, or the subscriber that
// no S-CSCF serves, whose profile nothing holds to be changed.
func TestChangingAProfileNameSettingPushesItsSubscribers(t *testing.T) {
	for _, tt := range []struct {
		name     string
		setting  string
		pushedTo string
	}{
		{"the test users", "hss.test_user_service_profile", testIMPI},
		{"every other subscription", "hss.default_service_profile", impi},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, api, scscfs := pushNetwork(t)
			change(t, api, tt.setting, "trial")
			pushed := pushedTo(t, h, scscfs, 1)
			if want := map[string]string{tt.pushedTo: settingsTrialAS}; len(pushed) != len(want) || pushed[tt.pushedTo] != settingsTrialAS {
				t.Errorf("the PPRs went to %v, want %v", pushed, want)
			}
		})
	}
}

// TestAChangeSentToEveryInstancePushesEachSubscriberOnce: the OAM server
// sends a change to every instance of the role, and the two instances
// here share the store and the lock of each subscriber, as two instances
// on the store of a deployment do. Each served subscriber of the
// changed setting gets one PPR, whichever instance sends it.
func TestAChangeSentToEveryInstancePushesEachSubscriberOnce(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	first, firstAPI := settingsInstance(t, store, locks)
	second, secondAPI := settingsInstance(t, store, locks)
	want := []string{impi}
	serve(t, first, impi, impu, cx.AssignRegistration)
	for i := range 5 {
		id := fmt.Sprintf("0010100002%05d@%s", i, realm)
		addSubscription(t, first, Subscription{PrivateIdentity: id,
			PublicIdentities: []string{"sip:" + id}, Password: "test-password-once"})
		registerInStore(t, first, id)
		want = append(want, id)
	}
	// One S-CSCF, which both instances reach, as every instance of the
	// HSS has the connections of the S-CSCFs of the network.
	scscfs := &fakeSCSCFs{}
	first.SetPeers(scscfs)
	second.SetPeers(scscfs)

	for _, api := range []*maintenance.Node{firstAPI, secondAPI} {
		if _, err := changeAt(api, 1, "hss.default_service_profile", "trial"); err != nil {
			t.Fatal(err)
		}
	}
	// pushedTo stops the first instance; the second stops here, so that
	// no PPR of it comes after the count.
	pushed := pushedTo(t, first, scscfs, len(want), second.Close)
	got := slices.Sorted(maps.Keys(pushed))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the PPRs went to %v, want %v", got, want)
	}
}

// TestAChangeOfTheSettingsThatPushesNothing: a change that gives no
// subscription another profile sends no PPR. The admission policy is
// not a profile, and a setting that names the profile it named already
// changes nothing to push.
//
// A change that does push follows each of them, and the test waits for
// the one PPR of that one: a change that pushed when it should not have
// pushed first, so its PPRs would be among those counted here.
func TestAChangeOfTheSettingsThatPushesNothing(t *testing.T) {
	for _, tt := range []struct{ name, setting, value string }{
		{"the admission policy", "hss.admission", string(AdmitTestUsers)},
		{"the profile that is in force already", "hss.default_service_profile", "default"},
		{"the test users' profile that is in force already", "hss.test_user_service_profile", "default"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, api, scscfs := pushNetwork(t)
			change(t, api, tt.setting, tt.value)
			change(t, api, "hss.default_service_profile", "trial")
			pushed := pushedTo(t, h, scscfs, 1)
			if len(pushed) != 1 || pushed[impi] != settingsTrialAS {
				t.Errorf("the PPRs went to %v, want the ordinary subscriber of the change that follows alone", pushed)
			}
		})
	}
}

// TestAChangedSettingIsPushedPageByPage: the HSS reads the served
// subscribers of the changed setting a page at a time, so a network
// with more of them than one page holds has every one of them pushed.
func TestAChangedSettingIsPushedPageByPage(t *testing.T) {
	h, api, scscfs := pushNetwork(t)
	// More than one page, and one over it, so that the last page is not
	// full and the one before it is.
	const extra = profilePushPage + 1
	want := []string{impi}
	for i := range extra {
		id := fmt.Sprintf("0010100001%05d@%s", i, realm)
		addSubscription(t, h, Subscription{PrivateIdentity: id,
			PublicIdentities: []string{"sip:" + id}, Password: "test-password-page"})
		registerInStore(t, h, id)
		want = append(want, id)
	}
	change(t, api, "hss.default_service_profile", "trial")
	pushed := pushedTo(t, h, scscfs, len(want))
	got := slices.Sorted(maps.Keys(pushed))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%d of the %d subscribers of the default profile were pushed", len(got), len(want))
	}
	for _, id := range got {
		if pushed[id] != settingsTrialAS {
			t.Fatalf("the PPR for %s carried the criteria of %q, want %q", id, pushed[id], settingsTrialAS)
		}
	}
}

// registerInStore writes the registration state that a SAR of a
// registration leaves, without the SAR: the test needs many subscribers
// served and nothing of the Cx exchange itself.
func registerInStore(t *testing.T, h *HSS, privateID string) {
	t.Helper()
	u, ok, err := h.store.Subscriber(t.Context(), privateID)
	if err != nil || !ok {
		t.Fatalf("Subscriber(%s): %v, %v", privateID, ok, err)
	}
	u.State, u.ServerName = Registered, scscf
	u.SCSCF = diameter.Identity{OriginHost: hdr.OriginHost, OriginRealm: hdr.OriginRealm}
	if err := h.store.Put(t.Context(), u); err != nil {
		t.Fatalf("Put(%s): %v", privateID, err)
	}
}

// TestAPushOfAChangedSettingTheSCSCFRefuses: the setting stays in
// force, the user stays registered and the HSS only logs the answer (TS
// 29.228 6.2.2.1, the SIMPLIFIED of pushProfile). The next SAR of the
// user carries the profile that the push did not land.
func TestAPushOfAChangedSettingTheSCSCFRefuses(t *testing.T) {
	h, api, _ := pushNetwork(t)
	refusing := &fakeSCSCFs{result: diameter.Experimental(cx.DiameterErrorUserUnknown)}
	h.SetPeers(refusing)
	change(t, api, "hss.default_service_profile", "trial")
	if pushed := pushedTo(t, h, refusing, 1); pushed[impi] != settingsTrialAS {
		t.Errorf("the PPRs went to %v, want the ordinary subscriber with the trial criteria", pushed)
	}
	if got := settingValue(t, api, "hss.default_service_profile"); got != "trial" {
		t.Errorf("the default profile in force is %q after the refused push, want trial", got)
	}
	u, ok, err := h.store.Subscriber(t.Context(), impi)
	if err != nil || !ok || u.State != Registered || u.ServerName != scscf {
		t.Errorf("the subscriber after the refused push: %v, %q (%v, %v)", u.State, u.ServerName, ok, err)
	}
}

// TestCloseWaitsForThePushOfAChangedSetting: a node that stops does not
// leave a PPR behind it. Close ends the pushes that are on their way
// and waits for the one in flight, whose PPA may take as long as the
// Diameter answer timer.
func TestCloseWaitsForThePushOfAChangedSetting(t *testing.T) {
	h, api, _ := pushNetwork(t)
	release := make(chan struct{})
	held := make(chan struct{})
	scscfs := &fakeSCSCFs{beforePPA: func(int) {
		close(held)
		<-release
	}}
	h.SetPeers(scscfs)
	change(t, api, "hss.default_service_profile", "trial")
	<-held

	closed := make(chan struct{})
	go func() {
		h.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a push was on its way")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return 5 s after the push ended")
	}
}

// TestSettingsPushesAndRequestsRunTogether: the pushes of a changed
// setting read the subscribers and the settings while the Cx requests
// write and read them both, so they must not race. Run with -race.
func TestSettingsPushesAndRequestsRunTogether(t *testing.T) {
	h, api, scscfs := pushNetwork(t)
	const rounds = 20
	done := make(chan struct{})
	for _, user := range [][2]string{{impi, impu}, {testIMPI, testIMPU}} {
		go func() {
			defer func() { done <- struct{}{} }()
			for range rounds {
				// A registration, which writes the state the pushes read,
				// and a deregistration, which takes the subscriber out of
				// what the pushes read at all.
				serve(t, h, user[0], user[1], cx.AssignRegistration)
				serve(t, h, user[0], user[1], cx.UserDeregistration)
			}
		}()
	}
	go func() {
		defer func() { done <- struct{}{} }()
		for i := range rounds {
			profile := "default"
			if i%2 == 0 {
				profile = "trial"
			}
			change(t, api, "hss.default_service_profile", profile)
			change(t, api, "hss.test_user_service_profile", profile)
		}
	}()
	for range 3 {
		<-done
	}
	h.Close()
	// Every PPR that went carried one whole profile, never a mixture of
	// the two (pushedTo checks each one).
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	for _, ppr := range scscfs.pprs {
		profile, err := cx.ParseUserProfile(ppr.UserData)
		if err != nil {
			t.Fatalf("the User-Data of the PPR for %s: %v", ppr.UserName, err)
		}
		if len(profile.InitialFilterCriteria) != 1 {
			t.Fatalf("the pushed profile of %s has %d criteria", ppr.UserName, len(profile.InitialFilterCriteria))
		}
		if as := profile.InitialFilterCriteria[0].ApplicationServer.ServerName; as != settingsDefaultAS && as != settingsTrialAS {
			t.Errorf("the criterion pushed to %s names %q, neither profile's server", ppr.UserName, as)
		}
	}
}
