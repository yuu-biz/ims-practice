package scscf

import (
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The sweep that ends the registrations comes round once an hour in
// these tests, so the Store still holds a binding after its Expires, as
// it does when claiming the expiry is late. A REGISTER that comes then
// must find no binding: a registration is soft state that runs out
// unless it is refreshed (RFC 3261 10.2.2), and TS 24.229 5.4.1.2.2A
// NOTE 13 counts as a previous registration only one that has neither
// been deregistered nor expired.

// briefBinding is the configuration of these tests: a binding of one
// second, and no sweep to end it.
func briefBinding() Config {
	return Config{MaxExpires: time.Second, SweepInterval: time.Hour}
}

// heldRegistration is the registration of impi that the Store holds,
// expired or not.
func heldRegistration(t *testing.T, h *harness) Registration {
	t.Helper()
	reg, ok, err := h.store.Registration(t.Context(), impi)
	if err != nil || !ok {
		t.Fatalf("registration: %v %v", ok, err)
	}
	return reg
}

// registerWithDigest registers the user with SIP Digest under callID and
// returns the registration the Store holds.
func registerWithDigest(t *testing.T, h *harness, callID string) Registration {
	t.Helper()
	auth, _ := answer(t, h.register(callID, 1, 600000, initial), password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want 200", r.StatusCode)
	}
	return heldRegistration(t, h)
}

// passExpiry waits until the binding is past its Expires.
func passExpiry(t *testing.T, reg Registration) {
	t.Helper()
	<-time.After(time.Until(reg.Expires) + 5*time.Millisecond)
}

// A REGISTER that comes after the binding expired starts a registration
// afresh: its SAR says REGISTRATION, the type of a first registration,
// and not RE_REGISTRATION (TS 29.229 6.3.15), and the binding it makes
// is a new one, with a contact id of its own and the event registered
// (TS 24.229 5.4.2.1.2). The expired binding takes no part in it, so the
// Call-ID and the CSeq of the REGISTER are not compared with the ones it
// holds: RFC 3261 10.3 step 7 compares them only with a binding that
// exists.
func TestRegisterAfterTheBindingExpiredStartsAfresh(t *testing.T) {
	for _, tt := range []struct {
		name       string
		sameCallID bool
	}{
		{"with a new Call-ID", false},
		{"with the Call-ID of the expired binding and no higher CSeq", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, briefBinding())
			first := sip.NewCallID()
			expired := registerWithDigest(t, h, first)
			passExpiry(t, expired)

			callID := sip.NewCallID()
			if tt.sameCallID {
				callID = first
			}
			auth, _ := answer(t, h.register(callID, 1, 600000, initial), password, "ip-assoc-pending")
			if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
				t.Fatalf("REGISTER after the expiry got %d, want 200 (overdue %v)", r.StatusCode, time.Since(expired.Expires))
			}
			want := []cx.ServerAssignmentType{cx.AssignRegistration, cx.AssignRegistration}
			if got := h.hss.assignments(); !slices.Equal(got, want) {
				t.Errorf("SARs = %v, want %v", got, want)
			}
			reg := heldRegistration(t, h)
			if reg.CallID != callID || reg.CSeq != 2 || !reg.Expires.After(expired.Expires) {
				t.Errorf("registration = %+v, want the one of Call-ID %s with CSeq 2 and a later expiry", reg, callID)
			}
			if reg.ContactID == expired.ContactID || reg.ContactEvent != reginfo.Registered {
				t.Errorf("the contact is %s with the event %s, want a new id with the event %s",
					reg.ContactID, reg.ContactEvent, reginfo.Registered)
			}
		})
	}
}

// A REGISTER that asks for no interval after the binding expired has no
// contact to remove: it gets 481 before any authentication, so neither a
// MAR nor a SAR goes to the HSS (TS 24.229 5.4.1.4.1 item 1). The
// expired binding stays in the Store, for the instance that handles its
// expiry.
func TestDeregisterAfterTheBindingExpiredGets481(t *testing.T) {
	for _, integrity := range []string{"ip-assoc-yes", "ip-assoc-pending"} {
		t.Run(integrity, func(t *testing.T) {
			h := newHarness(t, briefBinding())
			callID := sip.NewCallID()
			expired := registerWithDigest(t, h, callID)
			passExpiry(t, expired)

			dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain +
				`", response="", integrity-protected="` + integrity + `"`
			if r := h.register(callID, 3, 0, dereg); r.StatusCode != 481 {
				t.Fatalf("deregistration after the expiry got %d, want 481 (overdue %v)", r.StatusCode, time.Since(expired.Expires))
			}
			want := []routedTo{{"MAR", ""}, {"SAR", "hss." + domain}}
			if got := h.hss.requestRouting(); !slices.Equal(got, want) {
				t.Errorf("the Cx requests went to %+v, want %+v: the deregistration must send none", got, want)
			}
			if reg := heldRegistration(t, h); !reg.Expires.Equal(expired.Expires) {
				t.Errorf("registration = %+v, want the expired one left for the sweep, %+v", reg, expired)
			}
		})
	}
}

// The expiry of a binding that a REGISTER has replaced changes nothing.
// The instance that claimed it before the REGISTER came finds the new
// registration under the lock of the user and leaves it in the Store, so
// the NOTIFY of the expiry and the SAR of TIMEOUT_DEREGISTRATION, which
// follow the removal, go unsent. Storing the new registration ended the
// claim, and the expiry in the Store is the new binding's own.
func TestTheExpiryOfAReplacedBindingLeavesTheNewRegistration(t *testing.T) {
	h := newHarness(t, briefBinding())
	expired := registerWithDigest(t, h, sip.NewCallID())
	passExpiry(t, expired)

	claimed, err := h.store.ClaimDue(t.Context(), RegistrationExpiry, time.Now(), time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed %+v (%v), want the expiry of the binding", claimed, err)
	}
	callID := sip.NewCallID()
	auth, _ := answer(t, h.register(callID, 1, 600000, initial), password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("REGISTER after the expiry got %d, want 200", r.StatusCode)
	}
	reg := heldRegistration(t, h)

	if err := h.role.handleExpiry(t.Context(), claimed[0]); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Done(t.Context(), claimed[0]); err != nil {
		t.Fatal(err)
	}
	if got := heldRegistration(t, h); got.CallID != callID || !got.Expires.Equal(reg.Expires) {
		t.Errorf("registration after the expiry = %+v, want the new one %+v", got, reg)
	}
	want := []cx.ServerAssignmentType{cx.AssignRegistration, cx.AssignRegistration}
	if got := h.hss.assignments(); !slices.Equal(got, want) {
		t.Errorf("SARs = %v, want %v: the expiry of a binding that was replaced tells the HSS nothing", got, want)
	}
	due, err := h.store.ClaimDue(t.Context(), RegistrationExpiry, reg.Expires.Add(-time.Millisecond), time.Minute, 10)
	if err != nil || len(due) != 0 {
		t.Errorf("claimed %+v (%v) before the new binding expires, want none", due, err)
	}
	if due, err = h.store.ClaimDue(t.Context(), RegistrationExpiry, reg.Expires, time.Minute, 10); err != nil || len(due) != 1 {
		t.Errorf("claimed %+v (%v) once the new binding expired, want its expiry", due, err)
	}
}

// A protected REGISTER that comes after the binding expired is no
// re-registration either: no user is registered by that name any more,
// and this S-CSCF has no restoration procedures, so it gets 500 (TS
// 24.229 5.4.1.2.3A). It registers nothing without a new authentication,
// and the expired binding stays in the Store for its expiry.
func TestProtectedRegisterAfterTheBindingExpiredIsRefused(t *testing.T) {
	h := newHarnessWithHSS(t, briefBinding(), nil, hss.Config{},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: testSet1}})
	callID := sip.NewCallID()
	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
	if err != nil {
		t.Fatal(err)
	}
	creds := akaAnswer(ch, akaPassword(ch, res), sip.IntegrityProtectedYes)
	if r := h.register(callID, 2, 600000, creds.String()); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want 200", r.StatusCode)
	}
	expired := heldRegistration(t, h)
	passExpiry(t, expired)

	if r := h.register(callID, 3, 600000, creds.String()); r.StatusCode != 500 {
		t.Fatalf("protected REGISTER after the expiry got %d, want 500 (overdue %v)", r.StatusCode, time.Since(expired.Expires))
	}
	if got := h.hss.assignments(); !slices.Equal(got, []cx.ServerAssignmentType{cx.AssignRegistration}) {
		t.Errorf("SARs = %v, want the one of the registration alone", got)
	}
	if reg := heldRegistration(t, h); !reg.Expires.Equal(expired.Expires) {
		t.Errorf("registration = %+v, want the expired one left for the sweep, %+v", reg, expired)
	}
}
