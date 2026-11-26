package hss

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
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

// pushedTo waits for the S-CSCFs to have taken n PPRs and for the
// rounds of the pushes to be done (roundsDone), and then stops the HSS,
// which lets no further push begin and waits for the one on its way
// (HSS.Close), so that what came is all that comes; stops are the stops
// of the other instances that push to the same S-CSCFs. It returns the
// private identity of each PPR with the application server its criteria
// name, which says which service profile it carried, and fails the test
// for a subscriber that got more than one.
func pushedTo(t *testing.T, h *HSS, scscfs *fakeSCSCFs, n int, stops ...func()) map[string]string {
	t.Helper()
	pushed := map[string]string{}
	for _, p := range pprsTo(t, h, scscfs, n, stops...) {
		if _, twice := pushed[p.user]; twice {
			t.Errorf("%s got more than one PPR", p.user)
		}
		pushed[p.user] = p.server
	}
	return pushed
}

// ppr is the private identity of a PPR and the application server of
// the criteria it carried.
type ppr struct{ user, server string }

// pprsTo is pushedTo with every PPR in the order the S-CSCFs took them.
func pprsTo(t *testing.T, h *HSS, scscfs *fakeSCSCFs, n int, stops ...func()) []ppr {
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
	// Every PPR of a round reaches the S-CSCF before the round records
	// it, so once the rounds are done, a PPR that should not have gone at
	// all has come.
	roundsDone(t, h)
	h.Close()
	for _, stop := range stops {
		stop()
	}
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	var pprs []ppr
	for _, req := range scscfs.pprs {
		profile, err := cx.ParseUserProfile(req.UserData)
		if err != nil {
			t.Fatalf("the User-Data of the PPR for %s: %v", req.UserName, err)
		}
		if len(profile.InitialFilterCriteria) != 1 {
			t.Fatalf("the pushed profile of %s has %d criteria, want the one of a single profile",
				req.UserName, len(profile.InitialFilterCriteria))
		}
		pprs = append(pprs, ppr{req.UserName, profile.InitialFilterCriteria[0].ApplicationServer.ServerName})
	}
	return pprs
}

// roundsDone waits until the store of h records the push of each source
// as done for the name that the settings of the store give it: no round
// is under way, and none is due to start.
func roundsDone(t *testing.T, h *HSS) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		stored, ok, err := h.store.Settings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		done := ok
		for _, source := range profilePushSources {
			p, recorded, err := h.store.ProfilePush(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			done = done && recorded && p.Done && p.Name == settingsOf(stored).nameFor(source)
		}
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("gave up waiting 5 s for the rounds of the pushes to be done")
		}
	}
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

// stoppingSCSCFs is an S-CSCF that answers the first answered PPRs and
// none after them: the next PPR waits until its sender gives it up, as
// the PPR does that an instance had on its way when it stopped. stuck is
// closed once that PPR has come.
type stoppingSCSCFs struct {
	fakeSCSCFs
	answered int
	stuck    chan struct{}
	// once closes stuck once, however many PPRs come after the answered
	// ones.
	once sync.Once
}

func (s *stoppingSCSCFs) Request(ctx context.Context, originHost string, m *diam.Message) (*diam.Message, error) {
	s.mu.Lock()
	n := len(s.pprs)
	s.mu.Unlock()
	if n < s.answered {
		return s.fakeSCSCFs.Request(ctx, originHost, m)
	}
	s.once.Do(func() { close(s.stuck) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestARoundThatIsClaimedLateIsNotTakenOver: a round that no instance
// could claim for an hour, as when every instance was without its
// connections, is claimed at last, and its first PPR takes a while.
// Nothing has recorded a PPR of the round for far longer than two
// leases, but it is being sent, and the other instance, which sweeps
// many times while that PPR waits for its answer, does not make it due
// again and send the same subscriber.
func TestARoundThatIsClaimedLateIsNotTakenOver(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	first, api := settingsInstance(t, store, locks)
	other, _ := settingsInstance(t, store, locks)
	serve(t, first, impi, impu, cx.AssignRegistration)
	change(t, api, "hss.default_service_profile", "trial")
	p, _, err := store.ProfilePush(t.Context(), ProfileOfOtherUsers)
	if err != nil {
		t.Fatal(err)
	}
	waited := p
	waited.Updated = p.Updated.Add(-time.Hour)
	if swapped, err := store.CompareAndSwapProfilePush(t.Context(), &p, waited); err != nil || !swapped {
		t.Fatalf("CompareAndSwapProfilePush: %v, %v", swapped, err)
	}

	sending, release := make(chan struct{}), make(chan struct{})
	slow := &fakeSCSCFs{beforePPA: func(n int) {
		if n == 1 {
			close(sending)
			<-release
		}
	}}
	first.SetPeers(slow)
	select {
	case <-sending:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the first instance to send")
	}
	other.SetPeers(slow)
	// The other instance sweeps every few milliseconds, some forty times
	// in this; the claim of the first lasts far longer.
	time.Sleep(200 * time.Millisecond)
	close(release)
	if pushed := pushedTo(t, first, slow, 1, other.Close); len(pushed) != 1 || pushed[impi] != settingsTrialAS {
		t.Errorf("the PPRs went to %v, want the ordinary subscriber once", pushed)
	}
}

// TestAPushGoesOnWhenItsInstanceStops: an instance that stops in the
// middle of the push of a changed setting leaves the rest of the push in
// the store. Another instance on the store goes on with it once the
// claim of the stopped one has run out, from the first subscriber whose
// PPR was not answered, and each subscriber that the stopped instance
// did not reach gets one PPR.
func TestAPushGoesOnWhenItsInstanceStops(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	shortLease := func(c *Config) { c.PushLease = 300 * time.Millisecond }
	first, api := newSettingsInstance(t, store, locks, shortLease)
	serve(t, first, impi, impu, cx.AssignRegistration)
	var rest []string
	for i := range 4 {
		id := fmt.Sprintf("0010100003%05d@%s", i, realm)
		addSubscription(t, first, Subscription{PrivateIdentity: id,
			PublicIdentities: []string{"sip:" + id}, Password: "test-password-stop"})
		registerInStore(t, first, id)
		rest = append(rest, id)
	}
	stopping := &stoppingSCSCFs{answered: 1, stuck: make(chan struct{})}
	first.SetPeers(stopping)
	change(t, api, "hss.default_service_profile", "trial")
	select {
	case <-stopping.stuck:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the second PPR of the first instance")
	}
	first.Close()

	second, _ := newSettingsInstance(t, store, locks, shortLease)
	scscfs := &fakeSCSCFs{}
	second.SetPeers(scscfs)
	pushed := pushedTo(t, second, scscfs, len(rest))
	// The first instance had its PPR to the first subscriber answered,
	// and that one alone.
	stopping.mu.Lock()
	answered := len(stopping.pprs)
	stopping.mu.Unlock()
	if answered != 1 {
		t.Errorf("the first instance had %d PPRs answered, want 1", answered)
	}
	if got := slices.Sorted(maps.Keys(pushed)); !slices.Equal(got, rest) {
		t.Errorf("the second instance pushed to %v, want %v, the subscribers after the one the first instance reached", got, rest)
	}
	for id, as := range pushed {
		if as != settingsTrialAS {
			t.Errorf("the PPR for %s carried the criteria of %q, want %q", id, as, settingsTrialAS)
		}
	}
}

// TestTwoChangesBackToBackArePushedEach: a change of the default
// profile and the change back, the second before the round of the first
// has begun, each push the subscriber, in their order: the first round
// carries the profile of the first change, and the round after it that
// of the second. The S-CSCF is left with the profile in force.
func TestTwoChangesBackToBackArePushedEach(t *testing.T) {
	h, api, scscfs := pushNetwork(t)
	change(t, api, "hss.default_service_profile", "trial")
	change(t, api, "hss.default_service_profile", "default")
	pprs := pprsTo(t, h, scscfs, 2)
	want := []ppr{{impi, settingsTrialAS}, {impi, settingsDefaultAS}}
	if !slices.Equal(pprs, want) {
		t.Errorf("the PPRs were %v, want %v", pprs, want)
	}
}

// TestAChangeAndItsUndoDuringARoundPushAgain: the default profile goes
// to trial, and while the round of that change is under way, back to
// default and to trial again. The name in force at the end of the round
// is the name of the round, but the settings changed in between, and the
// instances may have given the subscriber the default profile in a SAA
// then. So the round of the last change follows, and the subscriber gets
// the trial profile twice.
func TestAChangeAndItsUndoDuringARoundPushAgain(t *testing.T) {
	h, api, _ := pushNetwork(t)
	release := make(chan struct{})
	held := make(chan struct{})
	scscfs := &fakeSCSCFs{beforePPA: func(n int) {
		if n == 1 {
			close(held)
			<-release
		}
	}}
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	h.SetPeers(scscfs)
	change(t, api, "hss.default_service_profile", "trial")
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the first PPR")
	}
	change(t, api, "hss.default_service_profile", "default")
	change(t, api, "hss.default_service_profile", "trial")
	released.Do(func() { close(release) })

	pprs := pprsTo(t, h, scscfs, 2)
	if want := []ppr{{impi, settingsTrialAS}, {impi, settingsTrialAS}}; !slices.Equal(pprs, want) {
		t.Errorf("the PPRs were %v, want %v", pprs, want)
	}
}

// TestAChangeBackAfterAChangeWhoseRoundNeverStarted: the instance that
// changed the default profile to trial stopped before it started the
// round, and the change back to default comes before any instance has
// found that. The S-CSCFs may have the trial profile from SAAs given in
// between, so the change back is pushed, although its name is the name
// of the last round.
func TestAChangeBackAfterAChangeWhoseRoundNeverStarted(t *testing.T) {
	store := settingsStore(t)
	// An instance that reads the store only as it starts and as it takes
	// a change, so that it finds the first change only through the
	// second.
	h, api := settingsInstanceReading(t, store, nil, time.Hour)
	serve(t, h, impi, impu, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	h.SetPeers(scscfs)
	stored, _, err := store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// All that the stopped instance wrote: the change.
	trial := stored
	trial.Generation, trial.DefaultServiceProfile, trial.DefaultServiceProfileGeneration = 1, "trial", 1
	if swapped, err := store.CompareAndSwapSettings(t.Context(), &stored, trial); err != nil || !swapped {
		t.Fatalf("CompareAndSwapSettings: %v, %v", swapped, err)
	}
	if _, err := changeAt(api, 2, "hss.default_service_profile", "default"); err != nil {
		t.Fatal(err)
	}
	if pushed := pushedTo(t, h, scscfs, 1); len(pushed) != 1 || pushed[impi] != settingsDefaultAS {
		t.Errorf("the PPRs went to %v, want the ordinary subscriber with the default criteria", pushed)
	}
}

// TestASecondChangeWhileARoundIsMidPage: the second change reaches
// another instance while the first instance is in the middle of the
// round of the first change. The round goes on with the profile of its
// change, and the round of the second change follows it, so each
// subscriber gets the first profile and then the second, once each.
func TestASecondChangeWhileARoundIsMidPage(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	first, firstAPI := settingsInstance(t, store, locks)
	second, secondAPI := settingsInstance(t, store, locks)
	ids := []string{impi}
	serve(t, first, impi, impu, cx.AssignRegistration)
	for i := range 2 {
		id := fmt.Sprintf("0010100005%05d@%s", i, realm)
		addSubscription(t, first, Subscription{PrivateIdentity: id,
			PublicIdentities: []string{"sip:" + id}, Password: "test-password-mid"})
		registerInStore(t, first, id)
		ids = append(ids, id)
	}
	release := make(chan struct{})
	held := make(chan struct{})
	scscfs := &fakeSCSCFs{beforePPA: func(n int) {
		if n == 1 {
			close(held)
			<-release
		}
	}}
	// A test that fails before the release lets the PPR go, so that the
	// stops of the instances do not wait for it.
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) })
	first.SetPeers(scscfs)
	second.SetPeers(scscfs)
	if _, err := changeAt(firstAPI, 1, "hss.default_service_profile", "trial"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the first PPR")
	}
	if _, err := changeAt(secondAPI, 2, "hss.default_service_profile", "default"); err != nil {
		t.Fatal(err)
	}
	released.Do(func() { close(release) })

	got := map[string][]string{}
	for _, p := range pprsTo(t, first, scscfs, 2*len(ids), second.Close) {
		got[p.user] = append(got[p.user], p.server)
	}
	for _, id := range ids {
		if want := []string{settingsTrialAS, settingsDefaultAS}; !slices.Equal(got[id], want) {
			t.Errorf("%s got the PPRs of %v, want %v", id, got[id], want)
		}
	}
}

// unconnectedSCSCFs is the Diameter server of an instance that the
// S-CSCF did not connect to: a request to it is not sent (Server.Request).
type unconnectedSCSCFs struct {
	mu    sync.Mutex
	tries int
}

func (u *unconnectedSCSCFs) Request(context.Context, string, *diam.Message) (*diam.Message, error) {
	u.mu.Lock()
	u.tries++
	u.mu.Unlock()
	return nil, diameter.ErrPeerUnavailable
}

// TestAPushGoesThroughTheInstanceTheSCSCFIsConnectedTo: the S-CSCF names
// one instance of the HSS and connects to it alone, and the round of a
// change goes to the other instance. That one cannot send the PPRs, and
// leaves them for the instance with the connection, which sends each
// once.
func TestAPushGoesThroughTheInstanceTheSCSCFIsConnectedTo(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	unconnected, api := settingsInstance(t, store, locks)
	connected, _ := settingsInstance(t, store, locks)
	ids := []string{impi}
	serve(t, unconnected, impi, impu, cx.AssignRegistration)
	for i := range 3 {
		id := fmt.Sprintf("0010100006%05d@%s", i, realm)
		addSubscription(t, unconnected, Subscription{PrivateIdentity: id,
			PublicIdentities: []string{"sip:" + id}, Password: "test-password-connected"})
		registerInStore(t, unconnected, id)
		ids = append(ids, id)
	}
	tried := &unconnectedSCSCFs{}
	unconnected.SetPeers(tried)
	change(t, api, "hss.default_service_profile", "trial")
	// The connected instance takes on nothing before the round has gone
	// through the other one, which then has tried every subscriber.
	roundsDone(t, unconnected)
	tried.mu.Lock()
	if tried.tries < len(ids) {
		t.Errorf("the unconnected instance tried %d PPRs, want %d at least", tried.tries, len(ids))
	}
	tried.mu.Unlock()

	scscfs := &fakeSCSCFs{}
	connected.SetPeers(scscfs)
	pushed := pushedTo(t, connected, scscfs, len(ids), unconnected.Close)
	slices.Sort(ids)
	if got := slices.Sorted(maps.Keys(pushed)); !slices.Equal(got, ids) {
		t.Errorf("the connected instance pushed to %v, want %v", got, ids)
	}
	for id, as := range pushed {
		if as != settingsTrialAS {
			t.Errorf("the PPR for %s carried the criteria of %q, want %q", id, as, settingsTrialAS)
		}
	}
}

// watchedLocks are the locks of locks, and tell when the lock of one
// name is asked for (asked) and given back (released) through them.
type watchedLocks struct {
	locks    *ims.UserLocks
	name     string
	asked    chan struct{}
	released chan struct{}
}

func (w *watchedLocks) Lock(ctx context.Context, user string) (func(), error) {
	if user != w.name {
		return w.locks.Lock(ctx, user)
	}
	w.asked <- struct{}{}
	unlock, err := w.locks.Lock(ctx, user)
	if err != nil {
		return nil, err
	}
	return func() { unlock(); w.released <- struct{}{} }, nil
}

// leftPPR is a round of the push of the default profile that is done,
// and a PPR of it that the round left for another instance, as the store
// holds them once a round has gone to an instance without the
// connection of the S-CSCF of impi.
func leftPPR(t *testing.T, store Store, round uint64) ProfilePushRetry {
	t.Helper()
	ctx := t.Context()
	p, _, err := store.ProfilePush(ctx, ProfileOfOtherUsers)
	if err != nil {
		t.Fatal(err)
	}
	left := ProfilePush{Source: ProfileOfOtherUsers, Name: "trial", Round: round, After: impi, Done: true, Updated: stamp()}
	if swapped, err := store.CompareAndSwapProfilePush(ctx, &p, left); err != nil || !swapped {
		t.Fatalf("CompareAndSwapProfilePush: %v, %v", swapped, err)
	}
	r := ProfilePushRetry{Source: ProfileOfOtherUsers, Round: round, PrivateID: impi}
	if err := store.ScheduleProfilePushRetry(ctx, r, dueNow()); err != nil {
		t.Fatal(err)
	}
	return r
}

// overtake records a round after round as the push of the default
// profile, as a change does that comes after the left PPR.
func overtake(t *testing.T, store Store, round uint64) {
	t.Helper()
	ctx := t.Context()
	p, _, err := store.ProfilePush(ctx, ProfileOfOtherUsers)
	if err != nil {
		t.Fatal(err)
	}
	later := ProfilePush{Source: ProfileOfOtherUsers, Name: "default", Round: round + 1, After: impi, Done: true, Updated: stamp()}
	if swapped, err := store.CompareAndSwapProfilePush(ctx, &p, later); err != nil || !swapped {
		t.Fatalf("CompareAndSwapProfilePush: %v, %v", swapped, err)
	}
}

// TestALeftPPRThatALaterRoundCoversIsNotSent: a PPR that a round left for
// another instance, of a round that a later one has followed, is not
// sent: the later round pushed every subscriber of the source.
func TestALeftPPRThatALaterRoundCoversIsNotSent(t *testing.T) {
	store := &droppingStore{MemoryStore: settingsStore(t), dropped: make(chan ProfilePushRetry, 1)}
	h, _ := settingsInstance(t, store, nil)
	serve(t, h, impi, impu, cx.AssignRegistration)
	leftPPR(t, store, 1)
	overtake(t, store, 1)
	scscfs := &fakeSCSCFs{}
	h.SetPeers(scscfs)
	// The instance takes the left PPR out of the store once it has sent
	// it, or found that it is not to be sent.
	select {
	case <-store.dropped:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the left PPR to leave the store")
	}
	h.Close()
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	if len(scscfs.pprs) != 0 {
		t.Errorf("%d PPRs of the round that a later one covers went out", len(scscfs.pprs))
	}
}

// TestALeftPPRThatALaterRoundOvertakesAtItsLockIsNotSent: the PPR that a
// round left waits for the lock of the pushes of its subscriber, and a
// later round of the source comes while it waits. Under the lock it finds
// the later round, and does not send the profile of its own over the one
// that the later round sends.
func TestALeftPPRThatALaterRoundOvertakesAtItsLockIsNotSent(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	watched := &watchedLocks{locks: locks, name: profilePushes(impi), asked: make(chan struct{}, 1), released: make(chan struct{}, 1)}
	h, _ := settingsInstance(t, store, watched)
	serve(t, h, impi, impu, cx.AssignRegistration)
	held, err := locks.Lock(t.Context(), profilePushes(impi))
	if err != nil {
		t.Fatal(err)
	}
	var unheld sync.Once
	t.Cleanup(func() { unheld.Do(held) })
	leftPPR(t, store, 1)
	scscfs := &fakeSCSCFs{}
	h.SetPeers(scscfs)
	select {
	case <-watched.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the left PPR to ask for the lock")
	}
	overtake(t, store, 1)
	unheld.Do(held)
	select {
	case <-watched.released:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the left PPR to give the lock back")
	}
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	if len(scscfs.pprs) != 0 {
		t.Errorf("%d PPRs of the overtaken round went out", len(scscfs.pprs))
	}
}

// stoppingStore is the store of an instance that stops between leaving
// a PPR for another instance and recording that PPR: once the instance
// has left one, the next record of the push waits until the instance
// gives it up, and fails. left is closed then.
type stoppingStore struct {
	*MemoryStore
	leftOne atomic.Bool
	left    chan struct{}
}

func (s *stoppingStore) ScheduleProfilePushRetry(ctx context.Context, r ProfilePushRetry, at time.Time) error {
	err := s.MemoryStore.ScheduleProfilePushRetry(ctx, r, at)
	if !s.leftOne.Swap(true) {
		close(s.left)
	}
	return err
}

func (s *stoppingStore) CompareAndSwapProfilePush(ctx context.Context, old *ProfilePush, next ProfilePush) (bool, error) {
	if s.leftOne.Load() {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return s.MemoryStore.CompareAndSwapProfilePush(ctx, old, next)
}

// TestAPPRLeftTwiceIsLeftOnce: an instance without the connection of the
// S-CSCF leaves the PPR of the subscriber for another instance, and
// stops before it records that. The instance that claims the round next
// sends the PPR again, cannot either, and leaves it again: the store
// has it once.
func TestAPPRLeftTwiceIsLeftOnce(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	shortLease := func(c *Config) { c.PushLease = 300 * time.Millisecond }
	stopping := &stoppingStore{MemoryStore: store, left: make(chan struct{})}
	first, api := newSettingsInstance(t, stopping, locks, shortLease)
	serve(t, first, impi, impu, cx.AssignRegistration)
	first.SetPeers(&unconnectedSCSCFs{})
	change(t, api, "hss.default_service_profile", "trial")
	select {
	case <-stopping.left:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the first instance to leave the PPR")
	}
	first.Close()

	second, _ := newSettingsInstance(t, store, locks, shortLease)
	second.SetPeers(&unconnectedSCSCFs{})
	roundsDone(t, second)
	second.Close()
	left, err := store.ClaimProfilePushRetries(t.Context(), time.Now().Add(time.Hour), time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].PrivateID != impi {
		t.Errorf("the store has the left PPRs %+v, want the one of %s", left, impi)
	}
}

// droppingStore tells when the first PPR left for another instance was
// left (firstLeft), and when a left PPR leaves the store (dropped).
type droppingStore struct {
	*MemoryStore
	dropped chan ProfilePushRetry
	mu      sync.Mutex
	left    time.Time
}

func (s *droppingStore) ScheduleProfilePushRetry(ctx context.Context, r ProfilePushRetry, at time.Time) error {
	s.mu.Lock()
	if s.left.IsZero() {
		s.left = time.Now()
	}
	s.mu.Unlock()
	return s.MemoryStore.ScheduleProfilePushRetry(ctx, r, at)
}

func (s *droppingStore) firstLeft() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.left
}

func (s *droppingStore) DoneProfilePushRetry(ctx context.Context, c ProfilePushRetryClaim) error {
	err := s.MemoryStore.DoneProfilePushRetry(ctx, c)
	select {
	case s.dropped <- c.ProfilePushRetry:
	default:
	}
	return err
}

// TestAPPRNoInstanceCanSendIsGivenUp: a PPR that the round left, and that
// no instance can send, is tried again until the time of the retries has
// passed since it was left, and then given up. That holds for a round
// that an instance claims long after it began, as when every instance
// was without its connections for a while: the PPR that it leaves first
// has the whole time of the retries too.
func TestAPPRNoInstanceCanSendIsGivenUp(t *testing.T) {
	const retryTime = 300 * time.Millisecond
	for _, tt := range []struct {
		name string
		// claimedAfter is how long the round waits for an instance
		// with peers.
		claimedAfter time.Duration
	}{
		{"a round claimed at once", 0},
		{"a round claimed late", 2 * retryTime},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &droppingStore{MemoryStore: settingsStore(t), dropped: make(chan ProfilePushRetry, 1)}
			h, api := newSettingsInstance(t, store, nil, func(c *Config) { c.PushRetryTime = retryTime })
			serve(t, h, impi, impu, cx.AssignRegistration)
			change(t, api, "hss.default_service_profile", "trial")
			time.Sleep(tt.claimedAfter)
			tried := &unconnectedSCSCFs{}
			h.SetPeers(tried)
			select {
			case <-store.dropped:
				// The times of a round are whole milliseconds (stamp),
				// which may fall up to one before the time it moved.
				if since := time.Since(store.firstLeft()); since < retryTime-time.Millisecond {
					t.Errorf("the PPR was given up %v after it was left, before the %v of the retries", since, retryTime)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("gave up waiting 5 s for the PPR to be given up")
			}
			tried.mu.Lock()
			defer tried.mu.Unlock()
			if tried.tries < 2 {
				t.Errorf("the PPR was tried %d times, want it tried again before it was given up", tried.tries)
			}
		})
	}
}

// TestARoundNoInstanceCanServeIsGivenUp: the only instance whose
// configuration defines the profile video names it for every
// subscription and stops before it sends a PPR of it. The other instance
// cannot send the round, and leaves it to another until the round has
// stood still, since it began, for the time of the retries; then it
// gives the round up.
// So the correction that the operator makes, back to default, which came
// while the round was under way, is pushed.
func TestARoundNoInstanceCanServeIsGivenUp(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	short := func(c *Config) { c.PushRetryTime = 300 * time.Millisecond }
	video, videoAPI := newSettingsInstance(t, store, locks, func(c *Config) {
		short(c)
		c.ServiceProfiles = maps.Clone(c.ServiceProfiles)
		c.ServiceProfiles["video"] = ServiceProfile{Criteria: []ifc.InitialFilterCriteria{profileCriterion("sip:video." + realm + ":5060")}}
	})
	other, otherAPI := newSettingsInstance(t, store, locks, short)
	serve(t, other, impi, impu, cx.AssignRegistration)
	if _, err := changeAt(videoAPI, 1, "hss.default_service_profile", "video"); err != nil {
		t.Fatal(err)
	}
	video.Close()
	scscfs := &fakeSCSCFs{}
	other.SetPeers(scscfs)
	if _, err := changeAt(otherAPI, 2, "hss.default_service_profile", "default"); err != nil {
		t.Fatal(err)
	}
	if pushed := pushedTo(t, other, scscfs, 1); len(pushed) != 1 || pushed[impi] != settingsDefaultAS {
		t.Errorf("the PPRs went to %v, want the ordinary subscriber with the default criteria", pushed)
	}
}

// gatedStore is the store of an instance that claims the rounds of the
// pushes only while open. With claimed, it is the store of an instance
// that claims one round and none after it: claimed is closed then.
type gatedStore struct {
	*MemoryStore
	open    atomic.Bool
	claimed chan struct{}
	once    sync.Once
}

func (s *gatedStore) ClaimProfilePushes(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]ProfilePushClaim, error) {
	if !s.open.Load() {
		return nil, nil
	}
	claims, err := s.MemoryStore.ClaimProfilePushes(ctx, now, lease, limit)
	if len(claims) > 0 && s.claimed != nil {
		s.open.Store(false)
		s.once.Do(func() { close(s.claimed) })
	}
	return claims, err
}

// TestARoundThatOutlastsTheTimeOfTheRetriesGoesOn: the round of a
// profile that one instance alone defines takes longer than the time of
// the retries, as a round of many pages does, or one towards a slow
// S-CSCF. Between two of its pages the instance that does not define the
// profile claims the round. The round has been moving all along, so that
// instance leaves it to the other and does not give it up, and every
// subscriber gets the profile.
func TestARoundThatOutlastsTheTimeOfTheRetriesGoesOn(t *testing.T) {
	const retryTime = 300 * time.Millisecond
	const videoAS = "sip:video." + realm + ":5060"
	memory := settingsStore(t)
	locks := &ims.UserLocks{}
	serving := &gatedStore{MemoryStore: memory}
	serving.open.Store(true)
	lacking := &gatedStore{MemoryStore: memory, claimed: make(chan struct{})}
	short := func(c *Config) { c.PushRetryTime = retryTime }
	video, videoAPI := newSettingsInstance(t, serving, locks, func(c *Config) {
		short(c)
		c.ServiceProfiles = maps.Clone(c.ServiceProfiles)
		c.ServiceProfiles["video"] = ServiceProfile{Criteria: []ifc.InitialFilterCriteria{profileCriterion(videoAS)}}
	})
	other, _ := newSettingsInstance(t, lacking, locks, short)
	serve(t, video, impi, impu, cx.AssignRegistration)
	want := []string{impi}
	for i := range profilePushPage {
		id := fmt.Sprintf("0010100003%05d@%s", i, realm)
		addSubscription(t, video, Subscription{PrivateIdentity: id,
			PublicIdentities: []string{"sip:" + id}, Password: "test-password-long"})
		registerInStore(t, video, id)
		want = append(want, id)
	}
	release := make(chan struct{})
	scscfs := &fakeSCSCFs{beforePPA: func(n int) {
		switch n {
		case 1:
			// The S-CSCF is slow to answer, and the round outlasts the
			// time of the retries.
			<-release
		case profilePushPage:
			// The last PPR of the first page: the next page goes to the
			// instance without the profile.
			serving.open.Store(false)
			lacking.open.Store(true)
		}
	}}
	video.SetPeers(scscfs)
	other.SetPeers(&fakeSCSCFs{})
	if _, err := changeAt(videoAPI, 1, "hss.default_service_profile", "video"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * retryTime)
	close(release)
	select {
	case <-lacking.claimed:
	case <-time.After(5 * time.Second):
		t.Fatal("gave up waiting 5 s for the instance without the profile to claim the round")
	}
	serving.open.Store(true)
	pushed := pushedTo(t, video, scscfs, len(want), other.Close)
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(pushed)); !slices.Equal(got, want) {
		t.Errorf("%d of the %d subscribers of the default profile were pushed", len(got), len(want))
	}
	for id, as := range pushed {
		if as != videoAS {
			t.Fatalf("the PPR for %s carried the criteria of %q, want %q", id, as, videoAS)
		}
	}
}

// TestALockedInstanceLeavesThePushToTheOthers: an instance that the
// operator has taken out of service takes on no new work, the push of a
// changed setting included, even when the change reached it: its
// Diameter connections are closed, and every PPR it sent would fail. An
// instance in service sends them.
func TestALockedInstanceLeavesThePushToTheOthers(t *testing.T) {
	store := settingsStore(t)
	locks := &ims.UserLocks{}
	locked, lockedAPI := settingsInstance(t, store, locks)
	inService, _ := settingsInstance(t, store, locks)
	serve(t, locked, impi, impu, cx.AssignRegistration)
	if _, err := lockedAPI.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{
		AdministrativeState: maintenance.Locked, Operator: "tester"}); err != nil {
		t.Fatal(err)
	}
	// The locked instance still has the peers it was given, so a PPR it
	// sent would show here.
	notReached := &fakeSCSCFs{}
	locked.SetPeers(notReached)
	scscfs := &fakeSCSCFs{}
	inService.SetPeers(scscfs)

	change(t, lockedAPI, "hss.default_service_profile", "trial")
	if pushed := pushedTo(t, inService, scscfs, 1, locked.Close); pushed[impi] != settingsTrialAS {
		t.Errorf("the instance in service pushed %v, want the ordinary subscriber with the trial criteria", pushed)
	}
	notReached.mu.Lock()
	defer notReached.mu.Unlock()
	if len(notReached.pprs) != 0 {
		t.Errorf("the locked instance sent %d PPRs", len(notReached.pprs))
	}
}

// TestAChangeWhoseInstanceStoppedBeforeItPushedIsPushed: an instance
// that changed the settings in the store and stopped before it began the
// push leaves the store with settings that no push has gone out for. An
// instance on the store finds that and pushes them.
func TestAChangeWhoseInstanceStoppedBeforeItPushedIsPushed(t *testing.T) {
	store := settingsStore(t)
	h, _ := settingsInstance(t, store, nil)
	serve(t, h, impi, impu, cx.AssignRegistration)
	scscfs := &fakeSCSCFs{}
	h.SetPeers(scscfs)
	// All that the stopped instance wrote: the change.
	stored, _, err := store.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next := stored
	next.Generation++
	next.DefaultServiceProfile, next.DefaultServiceProfileGeneration = "trial", next.Generation
	if swapped, err := store.CompareAndSwapSettings(t.Context(), &stored, next); err != nil || !swapped {
		t.Fatalf("CompareAndSwapSettings: %v, %v", swapped, err)
	}
	if pushed := pushedTo(t, h, scscfs, 1); len(pushed) != 1 || pushed[impi] != settingsTrialAS {
		t.Errorf("the PPRs went to %v, want the ordinary subscriber with the trial criteria", pushed)
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

// TestASourceWithNoPushAndTheNameOfTheFileIsInStep: the store has the
// settings of a change of the admission policy alone, and no push of
// either source recorded. Neither name has had another value than the
// one of the configuration file, which every S-CSCF has from the SAAs, so
// an instance that starts on the store records both pushes as done and
// sends no PPR.
func TestASourceWithNoPushAndTheNameOfTheFileIsInStep(t *testing.T) {
	ctx := t.Context()
	store := settingsStore(t)
	if swapped, err := store.CompareAndSwapSettings(ctx, nil, StoredSettings{Generation: 1, Admission: AdmitTestUsers,
		DefaultServiceProfile: "default", TestUserServiceProfile: "default"}); err != nil || !swapped {
		t.Fatalf("CompareAndSwapSettings: %v, %v", swapped, err)
	}
	u, ok, err := store.Subscriber(ctx, impi)
	if err != nil || !ok {
		t.Fatalf("Subscriber(%s): %v, %v", impi, ok, err)
	}
	u.State, u.ServerName = Registered, scscf
	u.SCSCF = diameter.Identity{OriginHost: hdr.OriginHost, OriginRealm: hdr.OriginRealm}
	if err := store.Put(ctx, u); err != nil {
		t.Fatalf("Put(%s): %v", impi, err)
	}

	h, _ := settingsInstance(t, store, nil)
	scscfs := &fakeSCSCFs{}
	h.SetPeers(scscfs)
	roundsDone(t, h)
	h.Close()
	scscfs.mu.Lock()
	defer scscfs.mu.Unlock()
	if len(scscfs.pprs) != 0 {
		t.Errorf("%d PPRs went out for names that no change has given another value", len(scscfs.pprs))
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
