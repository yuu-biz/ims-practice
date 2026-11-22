package scscf

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the S-CSCF on a Store that fails. The S-CSCF answers
// a request whose state it cannot read or write with 500 (RFC 3261
// 21.5.1), and an RTR with DIAMETER_UNABLE_TO_COMPLY, rather than take
// the failure for state that is not there or for a write that went
// through.

// failingStore is a MemoryStore whose operations fail while a test says
// so, as a store outside the process does once the S-CSCF cannot reach
// it.
type failingStore struct {
	*MemoryStore
	mu      sync.Mutex
	all     bool
	methods map[string]bool
}

var errUnreachable = errors.New("the store cannot be reached")

// fail makes the operations named fail, or all of them when none is
// named.
func (s *failingStore) fail(methods ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.all = len(methods) == 0
	s.methods = map[string]bool{}
	for _, m := range methods {
		s.methods[m] = true
	}
}

// repair ends what fail began.
func (s *failingStore) repair() { s.fail("none") }

// down reports whether method fails.
func (s *failingStore) down(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.all || s.methods[method]
}

func (s *failingStore) Challenge(ctx context.Context, privateID string) (Challenge, bool, error) {
	if s.down("Challenge") {
		return Challenge{}, false, errUnreachable
	}
	return s.MemoryStore.Challenge(ctx, privateID)
}

func (s *failingStore) PutChallenge(ctx context.Context, c Challenge) error {
	if s.down("PutChallenge") {
		return errUnreachable
	}
	return s.MemoryStore.PutChallenge(ctx, c)
}

func (s *failingStore) DeleteChallenge(ctx context.Context, privateID string) error {
	if s.down("DeleteChallenge") {
		return errUnreachable
	}
	return s.MemoryStore.DeleteChallenge(ctx, privateID)
}

func (s *failingStore) Registration(ctx context.Context, privateID string) (Registration, bool, error) {
	if s.down("Registration") {
		return Registration{}, false, errUnreachable
	}
	return s.MemoryStore.Registration(ctx, privateID)
}

func (s *failingStore) RegistrationOf(ctx context.Context, publicID string) (Registration, bool, error) {
	if s.down("RegistrationOf") {
		return Registration{}, false, errUnreachable
	}
	return s.MemoryStore.RegistrationOf(ctx, publicID)
}

func (s *failingStore) PutRegistration(ctx context.Context, r Registration) error {
	if s.down("PutRegistration") {
		return errUnreachable
	}
	return s.MemoryStore.PutRegistration(ctx, r)
}

func (s *failingStore) DeleteRegistration(ctx context.Context, privateID string) error {
	if s.down("DeleteRegistration") {
		return errUnreachable
	}
	return s.MemoryStore.DeleteRegistration(ctx, privateID)
}

func (s *failingStore) Subscription(ctx context.Context, id sip.DialogID) (Subscription, bool, error) {
	if s.down("Subscription") {
		return Subscription{}, false, errUnreachable
	}
	return s.MemoryStore.Subscription(ctx, id)
}

func (s *failingStore) SubscriptionsOf(ctx context.Context, privateID string) ([]Subscription, error) {
	if s.down("SubscriptionsOf") {
		return nil, errUnreachable
	}
	return s.MemoryStore.SubscriptionsOf(ctx, privateID)
}

func (s *failingStore) PutSubscription(ctx context.Context, sub Subscription) error {
	if s.down("PutSubscription") {
		return errUnreachable
	}
	return s.MemoryStore.PutSubscription(ctx, sub)
}

func (s *failingStore) DeleteSubscription(ctx context.Context, id sip.DialogID) error {
	if s.down("DeleteSubscription") {
		return errUnreachable
	}
	return s.MemoryStore.DeleteSubscription(ctx, id)
}

func (s *failingStore) OriginalDialog(ctx context.Context, id string) (OriginalDialog, bool, error) {
	if s.down("OriginalDialog") {
		return OriginalDialog{}, false, errUnreachable
	}
	return s.MemoryStore.OriginalDialog(ctx, id)
}

func (s *failingStore) PutOriginalDialog(ctx context.Context, d OriginalDialog) error {
	if s.down("PutOriginalDialog") {
		return errUnreachable
	}
	return s.MemoryStore.PutOriginalDialog(ctx, d)
}

func (s *failingStore) UnregisteredProfileOf(ctx context.Context, publicID string) (UnregisteredProfile, bool, error) {
	if s.down("UnregisteredProfileOf") {
		return UnregisteredProfile{}, false, errUnreachable
	}
	return s.MemoryStore.UnregisteredProfileOf(ctx, publicID)
}

func (s *failingStore) PutUnregisteredProfile(ctx context.Context, p UnregisteredProfile) error {
	if s.down("PutUnregisteredProfile") {
		return errUnreachable
	}
	return s.MemoryStore.PutUnregisteredProfile(ctx, p)
}

func (s *failingStore) DeleteUnregisteredProfile(ctx context.Context, privateID string, expires time.Time) (bool, error) {
	if s.down("DeleteUnregisteredProfile") {
		return false, errUnreachable
	}
	return s.MemoryStore.DeleteUnregisteredProfile(ctx, privateID, expires)
}

// TestRegisterWhenTheStoreFails: an initial REGISTER whose challenge
// the S-CSCF cannot read gets 500 rather than a challenge. When the
// S-CSCF cannot store the registration that the HSS accepted, the UE
// gets 500 rather than a 200 that no S-CSCF instance could serve, and
// may not originate a call. Once the store is back, the UE registers.
func TestRegisterWhenTheStoreFails(t *testing.T) {
	h := newHarness(t, Config{})
	callID := sip.NewCallID()
	h.failing.fail()
	if r := h.register(callID, 1, 600000, initial); r.StatusCode != 500 {
		t.Fatalf("REGISTER with the store down got %d, want 500", r.StatusCode)
	}
	h.failing.repair()
	r := h.register(callID, 2, 600000, initial)
	if r.StatusCode != 401 {
		t.Fatalf("REGISTER after the store came back got %d, want 401", r.StatusCode)
	}
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	h.failing.fail("PutRegistration")
	if r := h.register(callID, 3, 600000, auth); r.StatusCode != 500 {
		t.Fatalf("REGISTER whose registration cannot be stored got %d, want 500", r.StatusCode)
	}
	h.failing.repair()
	if r := h.invite("sip:001010000000002@"+domain, orig, impu); r.StatusCode != 403 {
		t.Errorf("INVITE of the user whose registration was not stored got %d, want 403", r.StatusCode)
	}
	h.registerUser(sip.NewCallID())
}

// TestRefreshWhenTheStoreFails: a refresh whose registration the S-CSCF
// cannot store gets 500, and the registration stays as it was, so that
// a call still reaches the UE.
func TestRefreshWhenTheStoreFails(t *testing.T) {
	h, _, pcscf := registeredHarness(t)
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	if r.StatusCode != 401 {
		t.Fatalf("refresh got %d, want 401", r.StatusCode)
	}
	auth, _ := answer(t, r, password, "ip-assoc-yes")
	h.failing.fail("PutRegistration")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 500 {
		t.Fatalf("refresh whose registration cannot be stored got %d, want 500", r.StatusCode)
	}
	h.failing.repair()
	h.invite(impu, "<sip:scscf."+domain+":5060;lr>", "")
	if got := received(t, pcscf); got.RequestURI != ueContact {
		t.Errorf("Request-URI = %q, want the registered contact", got.RequestURI)
	}
}

// TestInitialRequestsWhenTheStoreFails: an initial INVITE whose served
// user the S-CSCF cannot look up gets 500. The caller is not taken for
// a user who is not registered (403), and the callee not for one whose
// profile the S-CSCF has to take from the HSS.
func TestInitialRequestsWhenTheStoreFails(t *testing.T) {
	h, icscf, pcscf := registeredHarness(t)
	h.failing.fail()
	if r := h.invite("sip:001010000000002@"+domain, orig, impu); r.StatusCode != 500 {
		t.Errorf("originating INVITE got %d, want 500", r.StatusCode)
	}
	none(t, icscf, "the I-CSCF")
	if r := h.invite(impu, "<sip:scscf."+domain+":5060;lr>", ""); r.StatusCode != 500 {
		t.Errorf("terminating INVITE got %d, want 500", r.StatusCode)
	}
	none(t, pcscf, "the P-CSCF")
	if got := h.hss.assignments(); !slices.Equal(got, []cx.ServerAssignmentType{cx.AssignRegistration}) {
		t.Errorf("SARs = %v, want only the registration's", got)
	}
}

// TestApplicationServerWhenTheStoreFails: a request whose original
// dialog identifier the S-CSCF cannot store gets 500, and goes to no
// application server, which could not send it back.
func TestApplicationServerWhenTheStoreFails(t *testing.T) {
	as, got := fakeAS(t, answers(200))
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{smsCriterion(0, asURI, ifc.SessionContinued)},
		map[string]netip.AddrPort{asHost: as.LocalAddr()})
	h.registerUser(sip.NewCallID())
	h.failing.fail("PutOriginalDialog")
	if r := h.send(sms("sip:001010000000002@"+domain, orig, impu)); r.StatusCode != 500 {
		t.Errorf("MESSAGE got %d, want 500", r.StatusCode)
	}
	none(t, got, "the application server")
}

// TestRegEventSubscribeWhenTheStoreFails: a SUBSCRIBE to the reg event
// package whose registration or subscription the S-CSCF cannot read
// gets 500, as does one whose subscription it cannot store, which gets
// no NOTIFY.
func TestRegEventSubscribeWhenTheStoreFails(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.registerUser(sip.NewCallID())
	refusedWith500 := func(name string, sub *sip.Subscription, got <-chan sip.Notification) {
		t.Helper()
		select {
		case <-sub.Done():
			var refused *sip.SubscribeRefused
			if err := sub.Err(); !errors.As(err, &refused) || refused.StatusCode != 500 {
				t.Errorf("%s: subscription ended with %v, want a 500", name, err)
			}
		case n := <-got:
			t.Errorf("%s: NOTIFY %+v, want a 500", name, n.State)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the SUBSCRIBE was not refused", name)
		}
	}
	h.failing.fail()
	sub, got := h.subscribe(pcscfPath, "")
	refusedWith500("store down", sub, got)
	h.failing.fail("PutSubscription")
	sub, got = h.subscribe(pcscfPath, "")
	refusedWith500("subscription not stored", sub, got)
	h.failing.repair()

	// A SUBSCRIBE within a subscription that the S-CSCF holds.
	_, got = h.subscribe(pcscfPath, "")
	n, _ := nextNotify(t, got)
	h.failing.fail()
	if r := h.subscribeWithin(n.Request, 3600); r.StatusCode != 500 {
		t.Errorf("SUBSCRIBE within the subscription got %d, want 500", r.StatusCode)
	}
}

// TestRegistrationTerminationWhenTheStoreFails: an RTR whose
// registration the S-CSCF cannot read gets DIAMETER_UNABLE_TO_COMPLY,
// not DIAMETER_ERROR_USER_UNKNOWN, and leaves the user registered: once
// the store is back, the next RTR deregisters the user.
func TestRegistrationTerminationWhenTheStoreFails(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.registerUser(sip.NewCallID())
	_, got := h.subscribe(pcscfPath, "")
	nextNotify(t, got)
	h.failing.fail()
	if r := rtaResult(t, h.rtr(impi, cx.ServerChange)); r != cx.Base(cx.DiameterUnableToComply) {
		t.Errorf("RTA with the store down = %s, want DIAMETER_UNABLE_TO_COMPLY", r)
	}
	h.failing.repair()
	if r := rtaResult(t, h.rtr(impi, cx.ServerChange)); r != cx.Base(cx.DiameterSuccess) {
		t.Errorf("RTA after the store came back = %s, want DIAMETER_SUCCESS", r)
	}
	_, doc := nextNotify(t, got)
	contactState(t, doc, 1, reginfo.Terminated, reginfo.Deactivated)
}

// TestChallengeThatCannotBeRemoved: a right response whose challenge the
// S-CSCF cannot remove gets 500, and the challenge stays with its
// reg-await-auth timer (TS 24.229 5.4.1.2.3). When the timer runs out,
// the HSS hears that the authentication timed out, as for a UE that
// never answered.
func TestChallengeThatCannotBeRemoved(t *testing.T) {
	h := newHarness(t, Config{RegAwaitAuth: 300 * time.Millisecond})
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	if r.StatusCode != 401 {
		t.Fatalf("REGISTER got %d, want 401", r.StatusCode)
	}
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	h.failing.fail("DeleteChallenge")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 500 {
		t.Fatalf("answer whose challenge cannot be removed got %d, want 500", r.StatusCode)
	}
	h.failing.repair()
	waitForAssignments(t, h.hss, cx.AuthenticationTimeout)
}

// TestStaleAKAResponseWhenTheStoreFails: a response to an IMS AKA
// challenge with a later nonce count asks for a new challenge (TS
// 24.229 5.4.1.2.3B). When the S-CSCF cannot store the new one, the UE
// gets 500, and the challenge it had keeps its reg-await-auth timer.
func TestStaleAKAResponseWhenTheStoreFails(t *testing.T) {
	h := newHarnessWithHSS(t, Config{RegAwaitAuth: 300 * time.Millisecond}, nil, hss.Config{},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: testSet1}})
	usim := aka.NewUSIM(testSet1, 0)
	callID := sip.NewCallID()
	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	res, err := authenticate(t, usim, ch)
	if err != nil {
		t.Fatal(err)
	}
	creds := akaAnswer(ch, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes)
	creds.NC = "00000002"
	creds.Response, _ = sip.DigestResponse(sip.HA1(impi, ch.Realm, aka.DigestPassword(res.RES)), sip.REGISTER, creds)
	h.failing.fail("PutChallenge")
	if r := h.register(callID, 2, 600000, creds.String()); r.StatusCode != 500 {
		t.Fatalf("stale response whose new challenge cannot be stored got %d, want 500", r.StatusCode)
	}
	h.failing.repair()
	waitForAssignments(t, h.hss, cx.AuthenticationTimeout)
}
