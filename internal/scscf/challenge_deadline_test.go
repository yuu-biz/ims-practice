package scscf

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The sweep that handles reg-await-auth comes round once an hour in
// these tests, so the Store still holds a challenge after its Expires,
// as it does when claiming the expiry is late. A REGISTER that comes
// then must not find the authentication still under way (TS 24.229
// 5.4.1.2.3: when reg-await-auth runs out, authentication has failed).

// heldChallenge returns the challenge of impi, which the Store holds.
func heldChallenge(t *testing.T, h *harness) Challenge {
	t.Helper()
	ch, ok, err := h.store.Challenge(t.Context(), impi)
	if err != nil || !ok {
		t.Fatalf("challenge: %v %v", ok, err)
	}
	return ch
}

// passDeadline waits until the challenge is past its Expires, and still
// within the time that the Store keeps it.
func passDeadline(t *testing.T, ch Challenge) {
	t.Helper()
	<-time.After(time.Until(ch.Expires) + 5*time.Millisecond)
	if !time.Now().Before(ch.KeepUntil) {
		t.Fatalf("the Store forgot the challenge before the REGISTER: Expires %v, KeepUntil %v", ch.Expires, ch.KeepUntil)
	}
}

// A response to a challenge past its reg-await-auth is not accepted. The
// REGISTER is treated as one with no challenge: a new 401 with stale=true
// (5.4.1.2.2A step 1), and the HSS is told of the authentication timeout
// once. The answer to the new challenge registers the user.
func TestRegistrationRejectsExpiredChallengeBeforeSweep(t *testing.T) {
	h := newHarness(t, Config{RegAwaitAuth: 200 * time.Millisecond, SweepInterval: time.Hour})
	callID := sip.NewCallID()
	r := h.register(callID, 1, 3600, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	old := heldChallenge(t, h)
	passDeadline(t, old)

	r = h.register(callID, 2, 3600, auth)
	if r.StatusCode != 401 {
		t.Fatalf("answer to the expired challenge got %d, want 401 (overdue %v)", r.StatusCode, time.Since(old.Expires))
	}
	fresh, err := sip.ParseChallenge(r.Get("WWW-Authenticate"))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Nonce == old.Nonce || !fresh.Stale {
		t.Errorf("WWW-Authenticate = %s, want a new nonce with stale=true", r.Get("WWW-Authenticate"))
	}
	if _, ok, err := h.store.Registration(t.Context(), impi); err != nil || ok {
		t.Errorf("registration after the refused answer: %v %v, want none", ok, err)
	}

	// The expiry that was claimed late finds the new challenge, which is
	// not its own, and leaves it alone.
	if err := h.role.regAwaitAuthExpired(t.Context(), impi, old.Nonce); err != nil {
		t.Fatal(err)
	}
	if got := heldChallenge(t, h); got.Nonce != fresh.Nonce {
		t.Errorf("challenge after the old expiry = %s, want the new one %s", got.Nonce, fresh.Nonce)
	}

	auth, _ = answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 3, 3600, auth); r.StatusCode != 200 {
		t.Fatalf("answer to the new challenge got %d, want 200", r.StatusCode)
	}
	waitForAssignments(t, h.hss, cx.AuthenticationTimeout, cx.AssignRegistration)
}

// The refusal of a late answer is no deregistration: TS 24.229 5.4.1.2.3
// NOTE 2 keeps the registration of a user who was registered.
func TestExpiredChallengeKeepsTheRegistration(t *testing.T) {
	h := newHarness(t, Config{RegAwaitAuth: 200 * time.Millisecond, SweepInterval: time.Hour})
	callID := sip.NewCallID()
	auth, _ := answer(t, h.register(callID, 1, 3600, initial), password, "ip-assoc-pending")
	if r := h.register(callID, 2, 3600, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want 200", r.StatusCode)
	}
	before, ok, err := h.store.Registration(t.Context(), impi)
	if err != nil || !ok {
		t.Fatalf("registration: %v %v", ok, err)
	}

	// An initial REGISTER while registered starts a new authentication.
	callID = sip.NewCallID()
	r := h.register(callID, 1, 3600, initial)
	auth, _ = answer(t, r, password, "ip-assoc-pending")
	passDeadline(t, heldChallenge(t, h))
	if r := h.register(callID, 2, 3600, auth); r.StatusCode != 401 {
		t.Fatalf("answer to the expired challenge got %d, want 401", r.StatusCode)
	}
	after, ok, err := h.store.Registration(t.Context(), impi)
	if err != nil || !ok || !after.Expires.Equal(before.Expires) {
		t.Errorf("registration = %+v (%v, %v), want it as it was, %+v", after, ok, err, before)
	}
}

// The same holds for IMS AKA, whose protected REGISTER would otherwise
// be checked against the XRES of the expired challenge.
func TestIMSAKARejectsExpiredChallengeBeforeSweep(t *testing.T) {
	h := newHarnessWithHSS(t, Config{RegAwaitAuth: 200 * time.Millisecond, SweepInterval: time.Hour}, nil, hss.Config{},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: testSet1}})
	callID := sip.NewCallID()
	ch := akaChallenge(t, h.register(callID, 1, 3600, akaInitial))
	res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
	if err != nil {
		t.Fatal(err)
	}
	passDeadline(t, heldChallenge(t, h))

	creds := akaAnswer(ch, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes)
	if r := h.register(callID, 2, 3600, creds.String()); r.StatusCode == 200 {
		t.Fatal("REGISTER accepted after reg-await-auth expired")
	}
	if _, ok, err := h.store.Registration(t.Context(), impi); err != nil || ok {
		t.Errorf("registration after the refused answer: %v %v, want none", ok, err)
	}
	waitForAssignments(t, h.hss, cx.AuthenticationTimeout)
}
