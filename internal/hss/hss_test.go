package hss

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
)

const (
	realm    = "ims.mnc001.mcc001.3gppnetwork.org"
	impi     = "001010000000001@" + realm
	impu     = "sip:001010000000001@" + realm
	otherImp = "001010000000002@" + realm
	scscf    = "sip:scscf." + realm + ":5060"
	scscf2   = "sip:scscf2." + realm + ":5060"
)

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// mustHSS is New with a registry of the HSS's own, which fails only
// when that registry holds the measurements of another HSS.
func mustHSS(cfg Config, store Store, node *diameter.Node) *HSS {
	h, err := New(cfg, store, node, quiet())
	if err != nil {
		panic(err)
	}
	return h
}

func newTestHSS() *HSS {
	store, err := NewMemoryStore(
		Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"},
		Subscription{PrivateIdentity: otherImp, PublicIdentities: []string{"sip:001010000000002@" + realm}, Password: "test-password-2"},
	)
	if err != nil {
		panic(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	return mustHSS(Config{
		Realm:              realm,
		VisitedNetworks:    []string{realm},
		ServerCapabilities: cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}},
	}, store, node)
}

var hdr = diameter.Header{SessionID: "s;1;1", OriginHost: "icscf." + realm, OriginRealm: realm, DestinationRealm: realm}

// exchange sends a request through the wire format and the handler, as
// the Diameter peer would.
func exchange(t *testing.T, h *HSS, m *diam.Message) *diam.Message {
	t.Helper()
	roundTrip := func(m *diam.Message) *diam.Message {
		b, err := m.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		got, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	return roundTrip(h.Handle(roundTrip(m)))
}

func uar(t *testing.T, h *HSS, private, public string, typ cx.UserAuthorizationType) cx.UserAuthorizationAnswer {
	t.Helper()
	a, err := cx.ParseUserAuthorizationAnswer(exchange(t, h, cx.UserAuthorizationRequest{Header: hdr,
		UserName: private, PublicIdentity: public, VisitedNetworkIdentifier: realm, UserAuthorizationType: typ}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mar(t *testing.T, h *HSS, server string) cx.MultimediaAuthAnswer {
	t.Helper()
	a, err := cx.ParseMultimediaAuthAnswer(exchange(t, h, cx.MultimediaAuthRequest{Header: hdr,
		UserName: impi, PublicIdentity: impu, SIPAuthenticationScheme: cx.SchemeUnknown, SIPNumberAuthItems: 1, ServerName: server}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func sar(t *testing.T, h *HSS, server string, typ cx.ServerAssignmentType) cx.ServerAssignmentAnswer {
	t.Helper()
	a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
		UserName: impi, PublicIdentities: []string{impu}, ServerName: server, ServerAssignmentType: typ}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestRegistrationSequence walks the Cx side of one registration as
// TS 24.229 runs it, then the deregistration.
func TestRegistrationSequence(t *testing.T) {
	h := newTestHSS()

	// First REGISTER: nobody serves the user yet, so the I-CSCF gets
	// capabilities to choose an S-CSCF with.
	a := uar(t, h, impi, impu, cx.Registration)
	if a.Result != diameter.Experimental(cx.DiameterFirstRegistration) || a.ServerName != "" || a.ServerCapabilities == nil {
		t.Fatalf("first UAA = %+v, want FIRST_REGISTRATION with capabilities", a)
	}

	m := mar(t, h, scscf)
	if m.Result != diameter.Base(diameter.DiameterSuccess) || m.SIPAuthenticationScheme != cx.SchemeSIPDigest {
		t.Fatalf("MAA = %+v", m)
	}
	d := m.SIPDigestAuthenticate
	if d == nil || d.DigestRealm != realm || d.DigestQoP != "auth" || d.DigestHA1 != HA1(impi, realm, "test-password-1") {
		t.Fatalf("SIP-Digest-Authenticate = %+v", d)
	}

	// Second REGISTER: the MAR stored the S-CSCF while authentication is
	// pending, so the I-CSCF is sent back to it.
	a = uar(t, h, impi, impu, cx.Registration)
	if a.Result != diameter.Experimental(cx.DiameterSubsequentRegistration) || a.ServerName != scscf {
		t.Fatalf("second UAA = %+v, want SUBSEQUENT_REGISTRATION with %s", a, scscf)
	}

	s := sar(t, h, scscf, cx.AssignRegistration)
	if s.Result != diameter.Base(diameter.DiameterSuccess) || !strings.Contains(string(s.UserData), "<Identity>"+impu+"</Identity>") {
		t.Fatalf("SAA = %+v / %s", s, s.UserData)
	}

	// Another S-CSCF cannot take over a registered user.
	if s := sar(t, h, scscf2, cx.AssignRegistration); s.Result != diameter.Experimental(cx.DiameterErrorIdentityAlreadyRegistered) || s.ServerName != scscf {
		t.Errorf("SAR from another S-CSCF = %+v", s)
	}

	// Deregistration.
	if a := uar(t, h, impi, impu, cx.DeRegistration); a.Result != diameter.Base(diameter.DiameterSuccess) || a.ServerName != scscf {
		t.Errorf("UAA for deregistration = %+v", a)
	}
	if s := sar(t, h, scscf, cx.UserDeregistration); s.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("SAA for deregistration = %+v", s)
	}
	if a := uar(t, h, impi, impu, cx.DeRegistration); a.Result != diameter.Experimental(cx.DiameterErrorIdentityNotRegistered) {
		t.Errorf("UAA for deregistration of a deregistered user = %+v", a)
	}
}

func TestAuthenticationFailureForgetsTheServer(t *testing.T) {
	h := newTestHSS()
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AuthenticationFailure)
	if a := uar(t, h, impi, impu, cx.Registration); a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
		t.Errorf("UAA after a failed authentication = %+v, want FIRST_REGISTRATION", a)
	}
}

func TestIdentityChecks(t *testing.T) {
	h := newTestHSS()
	if a := uar(t, h, impi, "sip:unknown@"+realm, cx.Registration); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("unknown public identity: %+v", a)
	}
	if a := uar(t, h, otherImp, impu, cx.Registration); a.Result != diameter.Experimental(cx.DiameterErrorIdentitiesDontMatch) {
		t.Errorf("identities of two users: %+v", a)
	}
	if a := uar(t, h, impi, "SIP:001010000000001@IMS.mnc001.mcc001.3gppnetwork.org", cx.Registration); !a.Result.Success() {
		t.Errorf("scheme and host differ only in case: %+v", a)
	}
}

func TestRoamingCheck(t *testing.T) {
	h := newTestHSS()
	a, err := cx.ParseUserAuthorizationAnswer(exchange(t, h, cx.UserAuthorizationRequest{Header: hdr,
		UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: "visited.example"}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	if a.Result != diameter.Experimental(cx.DiameterErrorRoamingNotAllowed) {
		t.Errorf("UAA from an unknown visited network = %+v", a)
	}
}

func TestHA1MatchesRFC2617Example(t *testing.T) {
	// RFC 2617 3.5 uses username Mufasa, realm testrealm@host.com and
	// password "Circle Of Life".
	if got := HA1("Mufasa", "testrealm@host.com", "Circle Of Life"); got != "939e7578ed9e3c518a452acee763bce9" {
		t.Errorf("HA1 = %s", got)
	}
}

func lir(t *testing.T, h *HSS, public string) cx.LocationInfoAnswer {
	t.Helper()
	a, err := cx.ParseLocationInfoAnswer(exchange(t, h, cx.LocationInfoRequest{Header: hdr, PublicIdentity: public}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLocationInfo(t *testing.T) {
	h := newTestHSS()
	if a := lir(t, h, "sip:nobody@"+realm); a.Result != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("unknown callee: %+v", a)
	}
	if a := lir(t, h, impu); a.Result != diameter.Experimental(cx.DiameterErrorIdentityNotRegistered) {
		t.Errorf("callee not registered: %+v", a)
	}
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	if a := lir(t, h, impu); a.Result != diameter.Base(diameter.DiameterSuccess) || a.ServerName != scscf {
		t.Errorf("registered callee: %+v", a)
	}
}

// TestLocationInfoForUnregisteredServices is TS 29.228 6.1.4.1 step 3
// and 6.1.2.1 for a user who has a criterion of the unregistered state:
// the I-CSCF gets capabilities to pick an S-CSCF with, the S-CSCF takes
// the profile with a SAR of UNREGISTERED_USER, and calls go to that
// S-CSCF until it lets the user go.
func TestLocationInfoForUnregisteredServices(t *testing.T) {
	h := newTestHSS()
	unregistered := ifc.TerminatingUnregistered
	h.config.InitialFilterCriteria = []ifc.InitialFilterCriteria{{
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, SessionCase: &unregistered}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:mmtel." + realm + ":5060"},
	}}
	unregisteredUser := func(server string) cx.ServerAssignmentAnswer {
		t.Helper()
		// No User-Name: the S-CSCF does not know the private identity of a
		// user who has not registered (Table 6.1.2.1).
		a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
			PublicIdentities: []string{impu}, ServerName: server, ServerAssignmentType: cx.UnregisteredUser}.Message()))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	a := lir(t, h, impu)
	if a.Result != diameter.Experimental(cx.DiameterUnregisteredService) || a.ServerName != "" || a.ServerCapabilities == nil ||
		len(a.ServerCapabilities.MandatoryCapabilities) != 1 {
		t.Fatalf("LIA before any S-CSCF = %+v, want DIAMETER_UNREGISTERED_SERVICE with the capabilities", a)
	}
	s := unregisteredUser(scscf)
	if s.Result != diameter.Base(diameter.DiameterSuccess) || s.UserName != impi || !strings.Contains(string(s.UserData), "mmtel."+realm) {
		t.Fatalf("SAA for UNREGISTERED_USER = %+v / %s", s, s.UserData)
	}
	if a := lir(t, h, impu); a.Result != diameter.Base(diameter.DiameterSuccess) || a.ServerName != scscf {
		t.Errorf("LIA in the unregistered state = %+v, want the S-CSCF", a)
	}
	if s := unregisteredUser(scscf2); s.Result != diameter.Experimental(cx.DiameterErrorIdentityAlreadyRegistered) || s.ServerName != scscf {
		t.Errorf("SAA for another S-CSCF = %+v, want DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED with %s", s, scscf)
	}
	if a := uar(t, h, impi, impu, cx.Registration); a.Result != diameter.Experimental(cx.DiameterSubsequentRegistration) || a.ServerName != scscf {
		t.Errorf("UAA in the unregistered state = %+v, want SUBSEQUENT_REGISTRATION with %s", a, scscf)
	}
	sar(t, h, scscf, cx.TimeoutDeregistration)
	if a := lir(t, h, impu); a.Result != diameter.Experimental(cx.DiameterUnregisteredService) {
		t.Errorf("LIA once the S-CSCF let the user go = %+v", a)
	}
	// A MAR stores the S-CSCF while the authentication is pending.
	mar(t, h, scscf2)
	if a := lir(t, h, impu); a.Result != diameter.Base(diameter.DiameterSuccess) || a.ServerName != scscf2 {
		t.Errorf("LIA while an authentication is pending = %+v, want %s", a, scscf2)
	}
}

// TestBarredIdentity checks the barring of TS 29.228: a barred identity
// registers when its implicit registration set has one that is not
// barred, as the temporary public identity of a UE without an ISIM does
// (6.1.1.1 step 4, TS 23.228 E.3.1), and the SAA marks it with
// BarringIndication (B.2.1).
func TestBarredIdentity(t *testing.T) {
	const (
		volteIMPI = "001010000000010@" + realm
		temporary = "sip:001010000000010@" + realm
		onlyIMPI  = "001010000000019@" + realm
		only      = "sip:001010000000019@" + realm
	)
	store, err := NewMemoryStore(
		Subscription{PrivateIdentity: volteIMPI, PublicIdentities: []string{temporary, "sip:+12025550110@" + realm, "tel:+12025550110"},
			BarredIdentities: []string{temporary}, Password: "test-password-10"},
		Subscription{PrivateIdentity: onlyIMPI, PublicIdentities: []string{only}, BarredIdentities: []string{only}, Password: "test-password-19"},
	)
	if err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h := mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm}, ServerCapabilities: cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}}, store, node)

	if a := uar(t, h, volteIMPI, temporary, cx.Registration); a.Result != diameter.Experimental(cx.DiameterFirstRegistration) {
		t.Errorf("UAA for the temporary identity = %+v, want FIRST_REGISTRATION", a.Result)
	}
	if a := uar(t, h, onlyIMPI, only, cx.Registration); a.Result != diameter.Base(diameter.DiameterAuthorizationRejected) {
		t.Errorf("UAA for a set with no identity that is not barred = %+v, want DIAMETER_AUTHORIZATION_REJECTED", a.Result)
	}

	s, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
		UserName: volteIMPI, PublicIdentities: []string{temporary}, ServerName: scscf, ServerAssignmentType: cx.AssignRegistration}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := cx.ParseUserProfile(s.UserData)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.PublicIdentities) != 3 || len(profile.BarredIdentities) != 1 || profile.BarredIdentities[0] != temporary {
		t.Errorf("profile %+v, want the three identities with only %s barred", profile, temporary)
	}

	bad := Subscription{PrivateIdentity: onlyIMPI, PublicIdentities: []string{only}, BarredIdentities: []string{temporary}, Password: "x"}
	if err := bad.Validate(); err == nil {
		t.Error("a barred identity that is not one of the public identities was accepted")
	}
}
