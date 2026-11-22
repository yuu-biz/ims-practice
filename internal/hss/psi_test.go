package hss

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// The PSI user of the conference URIs of the test network: every PSI
// of the subdomain conf.
const (
	conferencePrivate    = "conference@" + realm
	conferenceWildcarded = "sip:!.*!@conf." + realm
	conferenceURI        = "sip:conf-7f3a@conf." + realm
)

func TestPSIUserMatchesItsRange(t *testing.T) {
	p, err := ParsePSIUser(conferencePrivate, "sip:conf-!.*!@conf."+realm)
	if err != nil {
		t.Fatal(err)
	}
	for psi, want := range map[string]bool{
		conferenceURI:                          true,
		"sip:conf-7f3a@CONF." + realm:          true, // the host has no case (RFC 3261 19.1.4)
		"sip:conf-7f3a@conf." + realm + ";x=y": true,
		"sip:conf-@conf." + realm:              true,
		// TS 23.003 13.5: an exclamation mark on the interface is a
		// character of the PSI.
		"sip:conf-!1@conf." + realm:    true,
		"sip:chat-7f3a@conf." + realm:  false,
		"sip:conf-7f3a@" + realm:       false,
		"sip:conf-7f3a@xconf." + realm: false,
		"tel:+12025550100":             false,
		"not a URI":                    false,
	} {
		if got := p.Matches(psi); got != want {
			t.Errorf("Matches(%q) = %v, want %v", psi, got, want)
		}
	}
}

func TestParsePSIUserRefusesWhatIsNoRange(t *testing.T) {
	for _, tc := range []struct{ private, wildcarded string }{
		{"", conferenceWildcarded},
		{conferencePrivate, "sip:conf-7f3a@conf." + realm}, // a distinct PSI
		{conferencePrivate, "sip:conf-!.*@conf." + realm},  // one delimiter
		{conferencePrivate, "tel:+1202555!.*!"},
		{conferencePrivate, "sip:!.*!@"},
		{conferencePrivate, "sip:!.*!@conf." + realm + ":5060"},
		{conferencePrivate, "sip:!(!@conf." + realm},
	} {
		if _, err := ParsePSIUser(tc.private, tc.wildcarded); err == nil {
			t.Errorf("ParsePSIUser(%q, %q) was accepted", tc.private, tc.wildcarded)
		}
	}
}

func newHSSWithPSIUser(t *testing.T) *HSS {
	t.Helper()
	h := newTestHSS()
	p, err := ParsePSIUser(conferencePrivate, conferenceWildcarded)
	if err != nil {
		t.Fatal(err)
	}
	h.config.PSIUsers = []PSIUser{p}
	return h
}

func psiSAR(t *testing.T, h *HSS, userName, psi string, typ cx.ServerAssignmentType) cx.ServerAssignmentAnswer {
	t.Helper()
	a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
		UserName: userName, PublicIdentities: []string{psi}, ServerName: scscf, ServerAssignmentType: typ}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestServerAssignmentOfAPSI is TS 29.228 6.1.2.1 for a PSI that the
// HSS stores as a wildcarded PSI: the S-CSCF gets the profile of the
// PSI user for a request on behalf of the PSI, and lets it go; step 4
// refuses what only a public user identity does.
func TestServerAssignmentOfAPSI(t *testing.T) {
	h := newHSSWithPSIUser(t)

	// The S-CSCF knows no private identity of the PSI (Table 6.1.2.1).
	a := psiSAR(t, h, "", conferenceURI, cx.UnregisteredUser)
	if a.Result != diameter.Base(diameter.DiameterSuccess) || a.UserName != conferencePrivate {
		t.Fatalf("SAA for UNREGISTERED_USER = %+v, want success with the private service identity", a)
	}
	p, err := cx.ParseUserProfile(a.UserData)
	if err != nil {
		t.Fatalf("SAA has no user profile: %v", err)
	}
	if p.PrivateIdentity != conferencePrivate || len(p.PublicIdentities) != 1 || p.PublicIdentities[0] != conferenceURI ||
		p.WildcardedPSI != conferenceWildcarded || len(p.InitialFilterCriteria) != 0 {
		t.Errorf("profile = %+v, want the PSI that matched %s, without criteria", p, conferenceWildcarded)
	}

	if a := psiSAR(t, h, conferencePrivate, conferenceURI, cx.TimeoutDeregistration); a.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("SAA for TIMEOUT_DEREGISTRATION = %+v, want success", a)
	}
	for _, typ := range []cx.ServerAssignmentType{cx.AssignRegistration, cx.ReRegistration, cx.UserDeregistration, cx.AuthenticationFailure} {
		if a := psiSAR(t, h, "", conferenceURI, typ); a.Result != diameter.Experimental(cx.DiameterErrorInAssignmentType) {
			t.Errorf("SAA for %v = %+v, want DIAMETER_ERROR_IN_ASSIGNMENT_TYPE", typ, a)
		}
	}
	// The private identity of a subscriber does not go with the PSI.
	if a := psiSAR(t, h, impi, conferenceURI, cx.UnregisteredUser); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("SAA for the PSI with a subscriber's private identity = %+v, want DIAMETER_ERROR_USER_UNKNOWN", a)
	}
	if a := psiSAR(t, h, "", "sip:conf-7f3a@conference."+realm, cx.UnregisteredUser); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("SAA for an identity outside every range = %+v, want DIAMETER_ERROR_USER_UNKNOWN", a)
	}
}

// TestSubscriberInTheRangeOfAPSIUser: a subscription whose identity is
// in the range of a wildcarded PSI keeps its own profile (TS 29.228
// 6.1.2.1 NOTE 5).
func TestSubscriberInTheRangeOfAPSIUser(t *testing.T) {
	h := newTestHSS()
	p, err := ParsePSIUser(conferencePrivate, "sip:!.*!@"+realm)
	if err != nil {
		t.Fatal(err)
	}
	h.config.PSIUsers = []PSIUser{p}
	a := psiSAR(t, h, "", impu, cx.UnregisteredUser)
	if a.Result != diameter.Base(diameter.DiameterSuccess) || a.UserName != impi {
		t.Fatalf("SAA = %+v, want the subscription of %s", a, impu)
	}
	if profile, err := cx.ParseUserProfile(a.UserData); err != nil || profile.WildcardedPSI != "" {
		t.Errorf("profile %+v (error %v) is not that of a public user identity", profile, err)
	}
}
