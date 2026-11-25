package scscf

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file run two instances of one S-CSCF on one store,
// as its nodes do, each under a Server-Name of its own. The HSS holds a
// user under the name of the instance whose MAR or SAR assigned the
// user, and takes a SAR under no other name (TS 29.228 8.1.2), so the
// instance that goes on with the user names the S-CSCF that the HSS
// assigned, whichever instance that is.

// assigned is the Server-Name of the first instance, the one of the
// harness.
const assigned = "sip:scscf." + domain + ":5060"

// anotherInstance starts a second instance of the S-CSCF of h, on the
// store, the HSS and the user locks of h, with a Server-Name, a
// Diameter identity and a SIP stack of its own. It returns h with that
// instance in place of the first.
func anotherInstance(t *testing.T, h *harness) *harness {
	t.Helper()
	const host = "scscf-2." + domain
	cfg := h.role.cfg
	cfg.Host, cfg.ServerName, cfg.Metrics = host, "sip:"+host+":5060", nil
	node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	s, err := New(cfg, h.hss, node, h.failing, h.role.resolver, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	stack := newStack(t, s.OnRequest)
	s.SetStack(stack)
	other := *h
	other.scscf, other.role = stack, s
	return &other
}

// heldBy is the Server-Name of the S-CSCF that the HSS holds for the
// user, as the UAA of a REGISTER gives it to the I-CSCF: "" when the
// HSS holds none and answers DIAMETER_FIRST_REGISTRATION (TS 29.228
// 6.1.1.1).
func heldBy(t *testing.T, f *fakeHSS) string {
	t.Helper()
	answer, err := f.Request(context.Background(), cx.UserAuthorizationRequest{
		Header:   diameter.Header{SessionID: "icscf;1;1", OriginHost: "icscf." + domain, OriginRealm: domain, DestinationRealm: domain},
		UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: domain, UserAuthorizationType: cx.Registration,
	}.Message())
	if err != nil {
		t.Fatal(err)
	}
	uaa, err := cx.ParseUserAuthorizationAnswer(answer)
	if err != nil {
		t.Fatal(err)
	}
	return uaa.ServerName
}

// waitForHeldBy waits until the HSS holds want for the user. A SAR that
// waitForAssignments has seen arrive may still be on its way through the
// HSS.
func waitForHeldBy(t *testing.T, f *fakeHSS, want, after string) {
	t.Helper()
	var got string
	if !came(3*time.Second, func() bool { got = heldBy(t, f); return got == want }) {
		t.Errorf("the HSS holds %q %s, want %q", got, after, want)
	}
}

func TestAnotherInstanceNamesTheAssignedSCSCF(t *testing.T) {
	protected := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`

	t.Run("registration expiry", func(t *testing.T) {
		h := newHarness(t, Config{MinExpires: time.Second, MaxExpires: time.Second})
		h.registerUser(sip.NewCallID())
		// The first instance stops, so the second one ends the
		// registration when it expires.
		h.role.Close()
		anotherInstance(t, h)
		waitForAssignments(t, h.hss, cx.AssignRegistration, cx.TimeoutDeregistration)
		waitForHeldBy(t, h.hss, "", "after the expiry")
	})
	t.Run("re-registration and deregistration", func(t *testing.T) {
		h := newHarness(t, Config{})
		callID := sip.NewCallID()
		h.registerUser(callID)
		other := anotherInstance(t, h)
		r := other.register(callID, 3, 600000, protected)
		auth, _ := answer(t, r, password, "ip-assoc-yes")
		if r := other.register(callID, 4, 600000, auth); r.StatusCode != 200 {
			t.Fatalf("re-registration at the other instance got %d, want 200", r.StatusCode)
		}
		if got := heldBy(t, h.hss); got != assigned {
			t.Errorf("the HSS holds %s after the re-registration, want %s", got, assigned)
		}
		if r := h.register(callID, 5, 0, protected); r.StatusCode != 200 {
			t.Fatalf("deregistration got %d, want 200", r.StatusCode)
		}
		waitForAssignments(t, h.hss, cx.AssignRegistration, cx.ReRegistration, cx.UserDeregistration)
		if got := heldBy(t, h.hss); got != "" {
			t.Errorf("the HSS holds %s after the deregistration, want no S-CSCF", got)
		}
	})
	t.Run("challenge answered at the other instance", func(t *testing.T) {
		h := newHarness(t, Config{})
		callID := sip.NewCallID()
		r := h.register(callID, 1, 600000, initial)
		auth, _ := answer(t, r, password, "ip-assoc-pending")
		if r := anotherInstance(t, h).register(callID, 2, 600000, auth); r.StatusCode != 200 {
			t.Fatalf("the answer to the challenge got %d, want 200", r.StatusCode)
		}
		if got := heldBy(t, h.hss); got != assigned {
			t.Errorf("the HSS holds %s, want %s", got, assigned)
		}
	})
	t.Run("authentication failure at the other instance", func(t *testing.T) {
		h := newHarness(t, Config{})
		callID := sip.NewCallID()
		r := h.register(callID, 1, 600000, initial)
		auth, _ := answer(t, r, "wrong-password", "ip-assoc-pending")
		if r := anotherInstance(t, h).register(callID, 2, 600000, auth); r.StatusCode != 403 {
			t.Fatalf("a wrong answer got %d, want 403", r.StatusCode)
		}
		waitForAssignments(t, h.hss, cx.AuthenticationFailure)
		if got := heldBy(t, h.hss); got != "" {
			t.Errorf("the HSS holds %s after the failure, want no S-CSCF", got)
		}
	})
	t.Run("reg-await-auth at the other instance", func(t *testing.T) {
		h := newHarness(t, Config{RegAwaitAuth: 100 * time.Millisecond})
		h.register(sip.NewCallID(), 1, 600000, initial)
		h.role.Close()
		anotherInstance(t, h)
		waitForAssignments(t, h.hss, cx.AuthenticationTimeout)
		waitForHeldBy(t, h.hss, "", "after reg-await-auth")
	})
	t.Run("profile of an unregistered user", func(t *testing.T) {
		h := newServiceHarnessWith(t, Config{UnregisteredProfileLifetime: 300 * time.Millisecond},
			[]ifc.InitialFilterCriteria{smsCriterion(0, asURI, ifc.SessionContinued)}, nil)
		if r := h.invite(impu, termRoute, ""); r.StatusCode != 480 {
			t.Fatalf("the call got %d, want 480", r.StatusCode)
		}
		h.role.Close()
		anotherInstance(t, h.harness)
		waitForAssignments(t, h.hss, cx.UnregisteredUser, cx.TimeoutDeregistration)
		waitForHeldBy(t, h.hss, "", "after the profile went")
	})
}

// TestAnotherInstanceNamesTheAssignedSCSCFWithIMSAKA is the same with
// IMS AKA. A protected re-registration sends a SAR with no MAR before
// it (TS 24.229 5.4.1.2.2), and the HSS takes the AUTS of a
// re-synchronisation only under the name of the MAR whose challenge the
// AUTS answers (TS 29.228 6.3.1 step 4).
func TestAnotherInstanceNamesTheAssignedSCSCFWithIMSAKA(t *testing.T) {
	t.Run("protected re-registration", func(t *testing.T) {
		h := newAKAHarness(t)
		callID := sip.NewCallID()
		ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
		res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
		if err != nil {
			t.Fatal(err)
		}
		creds := akaAnswer(ch, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes)
		if r := h.register(callID, 2, 600000, creds.String()); r.StatusCode != 200 {
			t.Fatalf("answer got %d, want 200", r.StatusCode)
		}
		if r := anotherInstance(t, h).register(callID, 3, 600000, creds.String()); r.StatusCode != 200 {
			t.Fatalf("protected re-registration at the other instance got %d, want 200", r.StatusCode)
		}
		if got := heldBy(t, h.hss); got != assigned {
			t.Errorf("the HSS holds %s after the re-registration, want %s", got, assigned)
		}
	})
	t.Run("re-synchronisation", func(t *testing.T) {
		h := newAKAHarness(t)
		usim := aka.NewUSIM(testSet1, aka.NewSQN(5000, 1))
		callID := sip.NewCallID()
		ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
		_, err := authenticate(t, usim, ch)
		var failure aka.SynchronisationFailure
		if !errors.As(err, &failure) {
			t.Fatalf("USIM: %v, want a synchronisation failure", err)
		}
		resync := akaAnswer(ch, "", sip.IntegrityProtectedNo)
		resync.SetAUTS(aka.EncodeAUTS(failure.AUTS))
		if r := anotherInstance(t, h).register(callID, 2, 600000, resync.String()); r.StatusCode != 401 {
			t.Fatalf("the re-synchronisation at the other instance got %d, want the 401 of a new challenge", r.StatusCode)
		}
	})
}
