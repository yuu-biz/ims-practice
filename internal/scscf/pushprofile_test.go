package scscf

import (
	"context"
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file are TS 29.228 6.2.2.1: the HSS pushes user
// information to the S-CSCF that serves a user, and the S-CSCF takes
// the profile in place of the one it holds.

const (
	firstAS  = "as1." + domain
	secondAS = "as2." + domain
)

// ppr sends the S-CSCF a PPR with data in User-Data, through the wire
// format, as the HSS would, and returns the result of the PPA.
func (h *harness) ppr(user string, data []byte) diameter.Result {
	h.t.Helper()
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	m := cx.PushProfileRequest{
		Header:   diameter.NewHeader(hssNode, "scscf."+domain, domain),
		UserName: user,
		UserData: data,
	}.Message()
	answer, err := cx.ParsePushProfileAnswer(wire(h.t, h.role.Handle(wire(h.t, m))))
	if err != nil {
		h.t.Fatal(err)
	}
	return answer.Result
}

// userProfile is the implicit registration set of the harness's user
// with criteria of its own, as the HSS would build it for a PPR
// (TS 29.228 6.5.2.1).
func userProfile(criteria ...ifc.InitialFilterCriteria) []byte {
	return cx.UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI},
		InitialFilterCriteria: criteria}.Marshal()
}

// TestPushedProfileServesTheNextRequest: a registered user whose
// profile the HSS pushes is served by the new criteria at once, with no
// new registration (TS 29.228 6.6.1). Here the application server of the
// user's short messages changes.
func TestPushedProfileServesTheNextRequest(t *testing.T) {
	first, firstGot := fakeAS(t, answers(202))
	second, secondGot := fakeAS(t, answers(202))
	hosts := map[string]netip.AddrPort{firstAS: first.LocalAddr(), secondAS: second.LocalAddr()}
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{smsCriterion(0, "sip:"+firstAS+":5060", ifc.SessionContinued)}, hosts)
	h.registerUser(sip.NewCallID())
	if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 202 {
		t.Fatalf("SMS got %d, want the 202 of the first server", r.StatusCode)
	}
	received(t, firstGot)

	result := h.ppr(impi, userProfile(smsCriterion(0, "sip:"+secondAS+":5060", ifc.SessionContinued)))
	if result != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("PPA = %s, want DIAMETER_SUCCESS", result)
	}
	if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 202 {
		t.Fatalf("SMS after the push got %d, want the 202 of the second server", r.StatusCode)
	}
	received(t, secondGot)
	none(t, firstGot, "the server of the old profile")
}

// TestPushedProfileServesAnUnregisteredUser: the S-CSCF keeps the
// profile of a user who is not registered too, and a push replaces that
// one as well. The HSS has such a user in the unregistered state, which
// is one of the two states it pushes for (TS 29.228 6.6.1).
func TestPushedProfileServesAnUnregisteredUser(t *testing.T) {
	first := newProxyAS(t, firstAS, "")
	second := newProxyAS(t, secondAS, "")
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{callCriterion(0, ifc.TerminatingUnregistered, "sip:"+firstAS+":5060")},
		map[string]netip.AddrPort{firstAS: first.stack.LocalAddr(), secondAS: second.stack.LocalAddr()})
	first.serve(h)
	second.serve(h)
	if r := h.invite(impu, termRoute, ""); r.StatusCode != 480 {
		t.Fatalf("call got %d, want 480", r.StatusCode)
	}
	received(t, first.got)
	waitForAssignments(t, h.hss, cx.UnregisteredUser)

	result := h.ppr(impi, userProfile(callCriterion(0, ifc.TerminatingUnregistered, "sip:"+secondAS+":5060")))
	if result != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("PPA = %s, want DIAMETER_SUCCESS", result)
	}
	if r := h.invite(impu, termRoute, ""); r.StatusCode != 480 {
		t.Fatalf("call after the push got %d, want 480", r.StatusCode)
	}
	received(t, second.got)
	none(t, first.got, "the server of the old profile")
	// The push is no new assignment: the S-CSCF already serves the user,
	// and it sends no SAR of its own for a profile it was given.
	waitForAssignments(t, h.hss, cx.UnregisteredUser)
}

// TestPushedProfileThatTheSCSCFRefuses: the answers of TS 29.228 Table
// 6.2.2.1.1, and the profile the S-CSCF keeps meanwhile.
func TestPushedProfileThatTheSCSCFRefuses(t *testing.T) {
	criteria := []ifc.InitialFilterCriteria{smsCriterion(0, asURI, ifc.SessionContinued)}
	tests := []struct {
		name   string
		user   string
		data   []byte
		result diameter.Result
	}{
		{"a user that this S-CSCF does not serve", "001010000000009@" + domain, userProfile(criteria...),
			diameter.Experimental(cx.DiameterErrorUserUnknown)},
		{"user information that it cannot read", impi, []byte("<IMSSubscription>"),
			diameter.Experimental(cx.DiameterErrorNotSupportedUserData)},
		{"a profile without a public identity", impi, []byte("<IMSSubscription><PrivateID>" + impi + "</PrivateID></IMSSubscription>"),
			diameter.Experimental(cx.DiameterErrorNotSupportedUserData)},
		// 6.5.1.1: the default public identity of a registered user is
		// the first one that is not barred, and 6.5.2.1 keeps a PPR from
		// changing it. A profile that bars them all has none.
		{"a profile that bars every identity of a registered user", impi,
			cx.UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI}, BarredIdentities: []string{impu, telURI},
				InitialFilterCriteria: []ifc.InitialFilterCriteria{smsCriterion(0, "sip:"+secondAS+":5060", ifc.SessionContinued)}}.Marshal(),
			diameter.Experimental(cx.DiameterErrorNotSupportedUserData)},
		// 6.2.2.1: a PPR carries at least one piece of user information.
		{"nothing to update", impi, nil, diameter.Base(diameter.DiameterUnableToComply)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newServiceHarness(t, criteria, nil)
			h.registerUser(sip.NewCallID())
			if result := h.ppr(tt.user, tt.data); result != tt.result {
				t.Errorf("PPA = %s, want %s", result, tt.result)
			}
			// 6.2.2.1: the S-CSCF does not overwrite the data it needs to
			// serve the subscription.
			reg, ok, err := h.store.Registration(context.Background(), impi)
			if err != nil || !ok {
				t.Fatalf("registration = %v, %v", ok, err)
			}
			if len(reg.InitialFilterCriteria) != 1 || reg.InitialFilterCriteria[0].ApplicationServer.ServerName != asURI {
				t.Errorf("criteria after the refused push = %+v, want the one of the registration", reg.InitialFilterCriteria)
			}
		})
	}
}

// TestCxCommandThatTheSCSCFDoesNotServe: of the Cx commands only the
// RTR and the PPR come to the S-CSCF; it sends the other four itself.
// One of them arriving here is answered with
// DIAMETER_COMMAND_UNSUPPORTED (RFC 6733 7.1.3).
func TestCxCommandThatTheSCSCFDoesNotServe(t *testing.T) {
	h := newServiceHarness(t, nil, nil)
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	uar := cx.UserAuthorizationRequest{Header: diameter.NewHeader(hssNode, "scscf."+domain, domain), UserName: impi, PublicIdentity: impu}
	answer, err := cx.ParseUserAuthorizationAnswer(wire(t, h.role.Handle(wire(t, uar.Message()))))
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != diameter.Base(diameter.DiameterCommandUnsupported) {
		t.Errorf("result = %s, want DIAMETER_COMMAND_UNSUPPORTED", answer.Result)
	}
}

// TestDeregistrationAfterAPushThatBarsEveryIdentity: the HSS deregisters
// a user after it pushed a profile that bars every identity. The
// S-CSCF refused that profile, so the third-party REGISTER of the
// deregistration still names the default public identity (TS 24.229
// 5.4.1.7 item c).
func TestDeregistrationAfterAPushThatBarsEveryIdentity(t *testing.T) {
	as, got := fakeAS(t, answers(200))
	criterion := registerCriterion(0, asURI, ifc.SessionContinued)
	h := newHarnessWithHSS(t, Config{}, fakeResolver{hosts: map[string]netip.AddrPort{asHost: as.LocalAddr()}},
		hssProfile(criterion), hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI}, Password: password})
	h.registerUser(sip.NewCallID())
	received(t, got)

	h.ppr(impi, cx.UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI}, BarredIdentities: []string{impu, telURI},
		InitialFilterCriteria: []ifc.InitialFilterCriteria{criterion}}.Marshal())
	rta, err := cx.ParseRegistrationTerminationAnswer(h.rtr(impi, cx.PermanentTermination))
	if err != nil || !rta.Result.Success() {
		t.Fatalf("RTA = %+v, %v; want DIAMETER_SUCCESS", rta, err)
	}
	if m := received(t, got); m.Get("To") != "<"+impu+">" {
		t.Errorf("third-party REGISTER To = %q, want the default public identity", m.Get("To"))
	}
}
