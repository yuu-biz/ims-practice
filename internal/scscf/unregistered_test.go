package scscf

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file call the user of the harness before it
// registers: TS 24.229 5.4.3.3 for a served user who is not registered.

const termRoute = "<sip:scscf." + domain + ":5060;lr>"

// callCriterion sends every INVITE of session case sc to server.
func callCriterion(priority int, sc ifc.SessionCase, server string) ifc.InitialFilterCriteria {
	return ifc.InitialFilterCriteria{
		Priority: priority,
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "INVITE"},
			{Group: []int{1}, SessionCase: &sc},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: server},
	}
}

// waitForAssignments waits until the SARs that hss got are want.
func waitForAssignments(t *testing.T, hss *fakeHSS, want ...cx.ServerAssignmentType) {
	t.Helper()
	if !came(3*time.Second, func() bool { return slices.Equal(hss.assignments(), want) }) {
		t.Fatalf("SARs = %v, want %v", hss.assignments(), want)
	}
}

// TestUnregisteredCalleeWithoutServices: the S-CSCF takes the profile
// with a SAR of UNREGISTERED_USER (NOTE 27), no criterion of the
// unregistered state matches, and the call gets 480 (item 3).
func TestUnregisteredCalleeWithoutServices(t *testing.T) {
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{smsCriterion(0, asURI, ifc.SessionContinued)}, nil)
	if r := h.invite(impu, termRoute, ""); r.StatusCode != 480 {
		t.Fatalf("got %d, want 480", r.StatusCode)
	}
	waitForAssignments(t, h.hss, cx.UnregisteredUser)
	none(t, h.pcscf, "the P-CSCF")
}

// TestUnregisteredCalleeServices is the criterion of TERMINATING_
// UNREGISTERED at work: the application server gets the call with
// P-Served-User regstate=unreg. A call it sends back unchanged gets 480
// with no server left, one it retargets goes to the new target, and
// whatever it answers itself reaches the caller.
func TestUnregisteredCalleeServices(t *testing.T) {
	server := "sip:as1." + domain + ":5060"
	criteria := []ifc.InitialFilterCriteria{callCriterion(0, ifc.TerminatingUnregistered, server)}
	t.Run("sent back", func(t *testing.T) {
		proxy := newProxyAS(t, "as1."+domain, "")
		h := newServiceHarness(t, criteria, map[string]netip.AddrPort{"as1." + domain: proxy.stack.LocalAddr()})
		proxy.serve(h)
		if r := h.invite(impu, termRoute, ""); r.StatusCode != 480 {
			t.Fatalf("got %d, want 480", r.StatusCode)
		}
		m := received(t, proxy.got)
		if got, want := m.Get("P-Served-User"), "<"+impu+">;sescase=term;regstate=unreg"; got != want {
			t.Errorf("P-Served-User = %q, want %q", got, want)
		}
		odiOf(t, m)
		none(t, h.pcscf, "the P-CSCF")
	})
	t.Run("retargeted", func(t *testing.T) {
		// Call forwarding when not logged in (CFNL) to subscriber 2.
		proxy := newProxyAS(t, "as1."+domain, "tel:+12025550102")
		h := newServiceHarness(t, criteria, map[string]netip.AddrPort{"as1." + domain: proxy.stack.LocalAddr()})
		proxy.serve(h)
		if r := h.invite(telURI, termRoute, ""); r.StatusCode != 486 {
			t.Fatalf("got %d, want the 486 of the I-CSCF", r.StatusCode)
		}
		received(t, proxy.got)
		m := received(t, h.icscf)
		if m.RequestURI != "sip:001010000000002@"+domain || m.Get("P-Served-User") != "" {
			t.Errorf("I-CSCF got Request-URI %q and P-Served-User %q, want the ENUM result and none", m.RequestURI, m.Get("P-Served-User"))
		}
	})
	t.Run("answered", func(t *testing.T) {
		as, got := fakeAS(t, answers(486))
		h := newServiceHarness(t, criteria, map[string]netip.AddrPort{"as1." + domain: as.LocalAddr()})
		if r := h.invite(impu, termRoute, ""); r.StatusCode != 486 {
			t.Fatalf("got %d, want the 486 of the server", r.StatusCode)
		}
		received(t, got)
	})
}

// TestUnregisteredProfileIsKeptForAWhile: the S-CSCF keeps the profile,
// so a second call needs no SAR. Once the time is up it lets the user
// go with a SAR, and the next call takes the profile again. A user who
// registered in the meantime is not deregistered.
func TestUnregisteredProfileIsKeptForAWhile(t *testing.T) {
	as, _ := fakeAS(t, answers(486))
	criteria := []ifc.InitialFilterCriteria{callCriterion(0, ifc.TerminatingUnregistered, "sip:as1."+domain+":5060")}
	hosts := map[string]netip.AddrPort{"as1." + domain: as.LocalAddr()}
	lifetime := Config{UnregisteredProfileLifetime: 500 * time.Millisecond}

	h := newServiceHarnessWith(t, lifetime, criteria, hosts)
	h.invite(impu, termRoute, "")
	h.invite(telURI, termRoute, "")
	waitForAssignments(t, h.hss, cx.UnregisteredUser)
	waitForAssignments(t, h.hss, cx.UnregisteredUser, cx.TimeoutDeregistration)
	h.invite(impu, termRoute, "")
	waitForAssignments(t, h.hss, cx.UnregisteredUser, cx.TimeoutDeregistration, cx.UnregisteredUser)

	h = newServiceHarnessWith(t, lifetime, criteria, hosts)
	h.invite(impu, termRoute, "")
	h.registerUser(sip.NewCallID())
	// The sweep marks the expiry done once its handler has returned, and
	// a SAR of that handler would have reached the HSS by then.
	if !came(3*time.Second, func() bool { return h.failing.handled(UnregisteredProfileExpiry) == 1 }) {
		t.Fatalf("the profile was not let go within 3 s of a lifetime of %v", lifetime.UnregisteredProfileLifetime)
	}
	if got, want := h.hss.assignments(), []cx.ServerAssignmentType{cx.UnregisteredUser, cx.AssignRegistration}; !slices.Equal(got, want) {
		t.Fatalf("SARs once the profile of the user who registered was let go = %v, want %v", got, want)
	}
	if r := h.invite(impu, termRoute, ""); r.StatusCode != 486 {
		t.Fatalf("call to the registered user got %d, want the 486 of the P-CSCF", r.StatusCode)
	}
	received(t, h.pcscf)
}

// TestUnregisteredCalleeOutsideTheAdmissionPolicy: the HSS admits only
// its test users, so the SAR of UNREGISTERED_USER is refused with
// DIAMETER_AUTHORIZATION_REJECTED. The S-CSCF has no profile to serve
// the call with and answers 500, as it does for any SAA that did not
// succeed; the caller learns nothing of the reason.
func TestUnregisteredCalleeOutsideTheAdmissionPolicy(t *testing.T) {
	criteria := []ifc.InitialFilterCriteria{callCriterion(0, ifc.TerminatingUnregistered, asURI)}
	hssCfg := hssProfile(criteria...)
	hssCfg.Admission = hss.AdmitTestUsers
	h := newHarnessWithHSS(t, Config{}, nil, hssCfg,
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password})
	if r := h.invite(impu, termRoute, ""); r.StatusCode != 500 {
		t.Fatalf("got %d, want 500", r.StatusCode)
	}
	waitForAssignments(t, h.hss, cx.UnregisteredUser)
	// The S-CSCF kept no profile of the refused user, so a second call
	// asks the HSS again instead of serving the call from nothing.
	if r := h.invite(impu, termRoute, ""); r.StatusCode != 500 {
		t.Fatalf("the second call got %d, want 500", r.StatusCode)
	}
	waitForAssignments(t, h.hss, cx.UnregisteredUser, cx.UnregisteredUser)
}

// TestUnregisteredCalleeServedElsewhere: another S-CSCF already holds
// the user, so the SAR gets DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED
// and the S-CSCF answers 305 with that S-CSCF (5.4.3.3).
func TestUnregisteredCalleeServedElsewhere(t *testing.T) {
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{callCriterion(0, ifc.TerminatingUnregistered, asURI)}, nil)
	other := "sip:scscf2." + domain + ":5060"
	// The other S-CSCF has started to authenticate the user.
	if _, err := h.hss.Request(t.Context(), cx.MultimediaAuthRequest{
		Header:   diameter.Header{SessionID: "scscf2;1", OriginHost: "scscf2." + domain, OriginRealm: domain, DestinationRealm: domain},
		UserName: impi, PublicIdentity: impu, SIPAuthenticationScheme: cx.SchemeSIPDigest, SIPNumberAuthItems: 1, ServerName: other,
	}.Message()); err != nil {
		t.Fatal(err)
	}
	r := h.invite(impu, termRoute, "")
	if r.StatusCode != 305 || r.Get("Contact") != "<"+other+">" {
		t.Fatalf("got %d with Contact %q, want 305 with the other S-CSCF", r.StatusCode, r.Get("Contact"))
	}
}

// TestOriginatingServicesAfterRetargeting is option b of TS 24.229
// 5.4.3.3 step 3: the terminating server diverts the call to subscriber
// 2, and since the diverting user has a criterion of ORIGINATING_CDIV,
// that server gets the call next, with P-Served-User orig-cdiv and the
// regstate of the terminating case (RFC 8498 4 and 5). Then the call
// goes to the new target through ENUM and the I-CSCF.
func TestOriginatingServicesAfterRetargeting(t *testing.T) {
	for _, tt := range []struct {
		name       string
		register   bool
		term       ifc.SessionCase
		wantTerm   string
		wantOrigCD string
	}{
		{"registered", true, ifc.TerminatingRegistered, ";sescase=term;regstate=reg", ";orig-cdiv;regstate=reg"},
		{"unregistered", false, ifc.TerminatingUnregistered, ";sescase=term;regstate=unreg", ";orig-cdiv;regstate=unreg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			diverting := newProxyAS(t, "as1."+domain, "tel:+12025550102")
			afterwards := newProxyAS(t, "as2."+domain, "")
			h := newServiceHarness(t, []ifc.InitialFilterCriteria{
				callCriterion(0, tt.term, "sip:as1."+domain+":5060"),
				callCriterion(1, ifc.OriginatingCDIV, "sip:as2."+domain+":5060"),
			}, map[string]netip.AddrPort{"as1." + domain: diverting.stack.LocalAddr(), "as2." + domain: afterwards.stack.LocalAddr()})
			diverting.serve(h)
			afterwards.serve(h)
			if tt.register {
				h.registerUser(sip.NewCallID())
			}
			if r := h.invite(impu, termRoute, ""); r.StatusCode != 486 {
				t.Fatalf("got %d, want the 486 of the I-CSCF", r.StatusCode)
			}
			first, second := received(t, diverting.got), received(t, afterwards.got)
			if got := first.Get("P-Served-User"); got != "<"+impu+">"+tt.wantTerm {
				t.Errorf("terminating server got P-Served-User %q", got)
			}
			if got := second.Get("P-Served-User"); got != "<"+impu+">"+tt.wantOrigCD {
				t.Errorf("server after retargeting got P-Served-User %q, want %q", got, "<"+impu+">"+tt.wantOrigCD)
			}
			if second.RequestURI != "tel:+12025550102" {
				t.Errorf("server after retargeting got Request-URI %q, want the diverted-to number", second.RequestURI)
			}
			if odiOf(t, first) == odiOf(t, second) {
				t.Error("both servers got the same original dialog identifier")
			}
			m := received(t, h.icscf)
			if m.RequestURI != "sip:001010000000002@"+domain || m.Get("P-Served-User") != "" {
				t.Errorf("I-CSCF got Request-URI %q and P-Served-User %q, want the ENUM result and none", m.RequestURI, m.Get("P-Served-User"))
			}
			none(t, h.pcscf, "the P-CSCF")
		})
	}
}
