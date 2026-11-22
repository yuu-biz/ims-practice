package scscf

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the requests that an application server originates
// on behalf of a served user who has no registration here (TS 24.229
// 5.7.3, and the paragraph of 5.4.3.2 on an AS acting on behalf of an
// unregistered user): a PSI that the AS hosts, or a user who is not
// registered.

// The private service identity of the PSI user of the conference URIs,
// and one conference URI of its range.
const (
	conferencePrivate = "conference@" + domain
	conferenceURI     = "sip:conf-7f3a@conf." + domain
)

// onBehalfRoute is the S-CSCF's URI with "orig", which an application
// server puts on top of Route for a request on behalf of a user or a
// PSI (TS 24.229 5.7.3).
const onBehalfRoute = "<sip:scscf." + domain + ":5060;lr;orig>"

// requestOnBehalf is an initial INVITE that an application server sends on
// behalf of a served user: with the identity that the callee is to see
// in P-Asserted-Identity, and the served user in P-Served-User when it
// is someone else (TS 24.229 5.7.3).
func requestOnBehalf(requestURI, asserted, served string) *sip.Message {
	m := newRequest(sip.INVITE, requestURI, onBehalfRoute, asserted)
	m.Set("From", "<"+asserted+">;tag="+sip.NewTag())
	m.Add("Contact", "<sip:as@192.0.2.20:5060>")
	if served != "" {
		m.Add("P-Served-User", "<"+served+">")
	}
	return m
}

// unregisteredAssignments counts the SARs of UNREGISTERED_USER.
func unregisteredAssignments(h *harness) int {
	n := 0
	for _, a := range h.hss.assignments() {
		if a == cx.UnregisteredUser {
			n++
		}
	}
	return n
}

// TestRequestOnBehalfOfAPSILeavesThroughTheIBCF: the AS that hosts a
// PSI sends a request on its behalf, as a conference focus invites a
// user (TS 24.147 5.3.2.5.4). The S-CSCF serves the PSI in the
// originating case with the profile of its PSI user, which it takes
// from the HSS once (TS 23.228 5.4.12.5 c), and routes the request as
// it routes one of a user: ENUM, then the IBCF for a number of another
// network (TS 24.229 5.4.3.2 steps 10 to 13).
func TestRequestOnBehalfOfAPSILeavesThroughTheIBCF(t *testing.T) {
	ibcfStack, toIBCF := nextHop(t)
	resolver := fakeResolver{
		hosts: map[string]netip.AddrPort{"ibcf." + domain: ibcfStack.LocalAddr()},
		enum:  map[string]string{"+12025550151": "sip:+12025550151@" + peerDomain + ";user=phone"},
	}
	ibcf, err := sip.ParseURI("sip:ibcf." + domain + ":5060")
	if err != nil {
		t.Fatal(err)
	}
	psi, err := hss.ParsePSIUser(conferencePrivate, "sip:!.*!@conf."+domain)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithHSS(t, Config{IBCF: ibcf}, resolver, hss.Config{PSIUsers: []hss.PSIUser{psi}},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password})

	for i := range 2 {
		if r := h.send(requestOnBehalf("tel:+12025550151", conferenceURI, "")); r.StatusCode != 486 {
			t.Fatalf("INVITE %d on behalf of the PSI got %d, want the 486 of the IBCF", i+1, r.StatusCode)
		}
		got := received(t, toIBCF)
		if want := "sip:+12025550151@" + peerDomain + ";user=phone"; got.RequestURI != want {
			t.Errorf("Request-URI = %q, want the ENUM result %q", got.RequestURI, want)
		}
		if got.Get("Route") != "<sip:ibcf."+domain+":5060;lr>" {
			t.Errorf("Route = %q, want the IBCF alone", got.Get("Route"))
		}
		if got.Get("Record-Route") != "<sip:scscf."+domain+":5060;lr>" {
			t.Errorf("Record-Route = %q", got.Get("Record-Route"))
		}
		if pai := got.GetAll("P-Asserted-Identity"); !slices.Equal(pai, []string{"<" + conferenceURI + ">"}) {
			t.Errorf("P-Asserted-Identity = %q, want the PSI alone", pai)
		}
		if got.Get("P-Served-User") != "" {
			t.Errorf("P-Served-User %q left the S-CSCF (step 14A)", got.Get("P-Served-User"))
		}
	}
	// The S-CSCF keeps the profile of the PSI user for the second INVITE.
	if n := unregisteredAssignments(h); n != 1 {
		t.Errorf("%d SARs of UNREGISTERED_USER, want 1", n)
	}
}

// TestRequestOnBehalfOfAnUnregisteredUserMeetsItsCriteria: an AS sends
// a request on behalf of a user who is not registered. The S-CSCF takes
// the user's profile from the HSS (TS 24.229 5.4.3.2 NOTE 24) and
// evaluates the criteria of ORIGINATING_UNREGISTERED, telling the next
// server so in P-Served-User (step 5 b). A request on behalf of an
// identity that the HSS does not know, and one of a UE that is not
// registered, get 403.
func TestRequestOnBehalfOfAnUnregisteredUserMeetsItsCriteria(t *testing.T) {
	as, got := fakeAS(t, answers(486))
	unregistered := ifc.OriginatingUnregistered
	criterion := ifc.InitialFilterCriteria{
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "INVITE"},
			{Group: []int{1}, SessionCase: &unregistered},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: asURI},
	}
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{criterion}, map[string]netip.AddrPort{asHost: as.LocalAddr()})

	if r := h.send(requestOnBehalf("tel:+12025550102", "sip:announcement@"+domain, impu)); r.StatusCode != 486 {
		t.Fatalf("INVITE on behalf of the unregistered user got %d, want the 486 of the AS", r.StatusCode)
	}
	m := received(t, got)
	if want := "<" + impu + ">;sescase=orig;regstate=unreg"; m.Get("P-Served-User") != want {
		t.Errorf("the AS got P-Served-User %q, want %q", m.Get("P-Served-User"), want)
	}
	if n := unregisteredAssignments(h.harness); n != 1 {
		t.Errorf("%d SARs of UNREGISTERED_USER, want 1", n)
	}

	if r := h.send(requestOnBehalf("tel:+12025550102", "sip:nobody@"+domain, "")); r.StatusCode != 403 {
		t.Errorf("INVITE on behalf of an unknown identity got %d, want 403", r.StatusCode)
	}
	// The Service-Route: only a UE that has registered has it.
	if r := h.invite("tel:+12025550102", orig, impu); r.StatusCode != 403 {
		t.Errorf("INVITE of the unregistered user along the Service-Route got %d, want 403", r.StatusCode)
	}
}
