package hss

import (
	"bytes"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sgd"
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
	if err := sgd.LoadDictionary(); err != nil {
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

// withProfiles gives h the service profiles of a configuration file and
// the names in force for the two kinds of subscription, which a test
// does in one call because the HSS reads the names from the settings an
// operator may change and not from the file it started on (Settings).
func withProfiles(h *HSS, profiles map[string]ServiceProfile, defaultName, testUserName string) {
	h.config.ServiceProfiles = profiles
	h.changeSettings(func(s *settings) {
		s.defaultServiceProfile, s.testUserServiceProfile = defaultName, testUserName
	})
}

// changeSettings puts in force the settings that change makes of the
// ones in force, as a change of the maintenance API does, at the
// generation after theirs.
func (h *HSS) changeSettings(change func(*settings)) {
	h.beginSettings()
	h.setting(change)
	if err := h.commitSettings(h.settings.Load().generation + 1); err != nil {
		panic(err)
	}
}

var hdr = diameter.Header{SessionID: "s;1;1", OriginHost: "icscf." + realm, OriginRealm: realm, DestinationRealm: realm}

// from is the header of a request of the S-CSCF named server. scscf2 is
// another Diameter node than scscf, so that a test sees which S-CSCF the
// HSS holds by the Diameter identity it stored as well as by the name.
func from(server string) diameter.Header {
	if server != scscf2 {
		return hdr
	}
	h := hdr
	h.SessionID, h.OriginHost = "scscf2;1;1", "scscf2."+realm
	return h
}

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
	a, err := cx.ParseMultimediaAuthAnswer(exchange(t, h, cx.MultimediaAuthRequest{Header: from(server),
		UserName: impi, PublicIdentity: impu, SIPAuthenticationScheme: cx.SchemeUnknown, SIPNumberAuthItems: 1, ServerName: server}.Message()))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func sar(t *testing.T, h *HSS, server string, typ cx.ServerAssignmentType) cx.ServerAssignmentAnswer {
	t.Helper()
	a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: from(server),
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

// TestServerAssignmentFromAnotherSCSCF is TS 29.228 8.1.2: a SAR from
// an S-CSCF other than the one whose name the HSS holds changes nothing,
// whatever its Server-Assignment-Type, and the answer names the S-CSCF
// that the HSS holds (Table 6.1.2.2). The first state is the case of
// 8.1.1: the user registered again at scscf2, whose MAR took the user
// over, and scscf still has the registration that it then ends.
func TestServerAssignmentFromAnotherSCSCF(t *testing.T) {
	alreadyRegistered := diameter.Experimental(cx.DiameterErrorIdentityAlreadyRegistered)
	types := []struct {
		typ  cx.ServerAssignmentType
		want diameter.Result
	}{
		{cx.AssignRegistration, alreadyRegistered},
		{cx.ReRegistration, alreadyRegistered},
		{cx.UnregisteredUser, alreadyRegistered},
		{cx.TimeoutDeregistration, alreadyRegistered},
		{cx.UserDeregistration, alreadyRegistered},
		{cx.AdministrativeDeregistration, alreadyRegistered},
		{cx.AuthenticationFailure, alreadyRegistered},
		{cx.AuthenticationTimeout, alreadyRegistered},
		{cx.NoAssignment, diameter.Base(diameter.DiameterUnableToComply)},
	}
	// Each state leaves the HSS with the name of scscf2.
	states := []struct {
		name  string
		enter func(t *testing.T, h *HSS)
	}{
		{"registered at scscf2 after scscf", func(t *testing.T, h *HSS) {
			mar(t, h, scscf)
			sar(t, h, scscf, cx.AssignRegistration)
			mar(t, h, scscf2)
			sar(t, h, scscf2, cx.AssignRegistration)
		}},
		{"authentication pending at scscf2", func(t *testing.T, h *HSS) {
			mar(t, h, scscf2)
		}},
		{"unregistered at scscf2", func(t *testing.T, h *HSS) {
			sar(t, h, scscf2, cx.UnregisteredUser)
		}},
	}
	stored := func(t *testing.T, h *HSS) Subscriber {
		t.Helper()
		u, ok, err := h.store.Subscriber(t.Context(), impi)
		if err != nil || !ok {
			t.Fatalf("the subscriber is not in the store: %v", err)
		}
		return u
	}
	for _, state := range states {
		for _, tt := range types {
			t.Run(state.name+", "+tt.typ.String(), func(t *testing.T) {
				h := newTestHSS()
				state.enter(t, h)
				before, located := stored(t, h), lir(t, h, impu)
				if before.ServerName != scscf2 {
					t.Fatalf("the HSS holds %q, want %q", before.ServerName, scscf2)
				}
				if a := sar(t, h, scscf, tt.typ); a.Result != tt.want || a.ServerName != scscf2 {
					t.Errorf("SAA = %+v, want %v with %s", a, tt.want, scscf2)
				}
				if after := stored(t, h); !reflect.DeepEqual(after, before) {
					t.Errorf("the subscriber is %+v after the SAR, want it as it was, %+v", after, before)
				}
				if a := lir(t, h, impu); !reflect.DeepEqual(a, located) {
					t.Errorf("LIA after the SAR = %+v, want %+v as before it", a, located)
				}
			})
		}
	}
}

// TestOnePublicIdentityPerAssignment is TS 29.228 6.1.2.1 step 3 with
// Table 6.1.2.1: a SAR names one and only one Public-Identity unless it
// deregisters, and one that names more gets
// DIAMETER_AVP_OCCURS_TOO_MANY_TIMES with no user information. The step
// comes before the check of the S-CSCF name (8.1.2), so the answer is
// the same when the SAR comes from an S-CSCF other than the one the HSS
// holds.
func TestOnePublicIdentityPerAssignment(t *testing.T) {
	const tel = "tel:+12025550101"
	newHSS := func(t *testing.T) *HSS {
		t.Helper()
		store, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "test-password-1"})
		if err != nil {
			t.Fatal(err)
		}
		node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
		h := mustHSS(Config{Realm: realm, VisitedNetworks: []string{realm}}, store, node)
		mar(t, h, scscf)
		sar(t, h, scscf, cx.AssignRegistration)
		return h
	}
	both := func(t *testing.T, h *HSS, server string, typ cx.ServerAssignmentType) cx.ServerAssignmentAnswer {
		t.Helper()
		a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: from(server),
			UserName: impi, PublicIdentities: []string{impu, tel}, ServerName: server, ServerAssignmentType: typ}.Message()))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	stored := func(t *testing.T, h *HSS) Subscriber {
		t.Helper()
		u, ok, err := h.store.Subscriber(t.Context(), impi)
		if err != nil || !ok {
			t.Fatalf("the subscriber is not in the store: %v", err)
		}
		return u
	}
	tooMany := diameter.Base(diameter.DiameterAVPOccursTooManyTimes)
	for _, typ := range []cx.ServerAssignmentType{cx.NoAssignment, cx.AssignRegistration, cx.ReRegistration,
		cx.UnregisteredUser, cx.AuthenticationFailure, cx.AuthenticationTimeout} {
		for _, server := range []string{scscf, scscf2} {
			t.Run(typ.String()+" from "+server, func(t *testing.T) {
				h := newHSS(t)
				before := stored(t, h)
				if a := both(t, h, server, typ); a.Result != tooMany || a.UserName != "" || len(a.UserData) != 0 || a.ServerName != "" {
					t.Errorf("SAA = %+v, want %v and nothing else", a, tooMany)
				}
				if after := stored(t, h); !reflect.DeepEqual(after, before) {
					t.Errorf("the subscriber is %+v after the SAR, want it as it was, %+v", after, before)
				}
			})
		}
	}
	for _, typ := range []cx.ServerAssignmentType{cx.TimeoutDeregistration, cx.UserDeregistration, cx.AdministrativeDeregistration} {
		t.Run(typ.String(), func(t *testing.T) {
			if a := both(t, newHSS(t), scscf, typ); a.Result != diameter.Base(diameter.DiameterSuccess) {
				t.Errorf("SAA = %+v, want DIAMETER_SUCCESS for a deregistration of two identities", a)
			}
		})
	}
	t.Run("PSI", func(t *testing.T) {
		h := newHSSWithPSIUser(t)
		a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr, PublicIdentities: []string{conferenceURI, conferenceURI},
			ServerName: scscf, ServerAssignmentType: cx.UnregisteredUser}.Message()))
		if err != nil || a.Result != tooMany || len(a.UserData) != 0 {
			t.Errorf("SAA for a PSI named twice = %+v (%v), want %v with no profile", a, err, tooMany)
		}
	})
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
	withProfiles(h, map[string]ServiceProfile{"default": {Criteria: []ifc.InitialFilterCriteria{{
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, SessionCase: &unregistered}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:mmtel." + realm + ":5060"},
	}}}}, "default", "")
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

// The admission policy and the procedures it decides.

const (
	testIMPI = "001010000000009@" + realm
	testIMPU = "sip:001010000000009@" + realm
)

// admissionHSS is an HSS on the given policy with two subscribers: impi,
// an ordinary one, and testIMPI, one marked as a test user. Both have a
// criterion for the terminating unregistered session case, so that a
// call to either reaches the services of the unregistered state.
func admissionHSS(t *testing.T, policy Admission) *HSS {
	t.Helper()
	store, err := NewMemoryStore(
		Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"},
		Subscription{PrivateIdentity: testIMPI, PublicIdentities: []string{testIMPU}, Password: "test-password-9", TestUser: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	unregistered := ifc.TerminatingUnregistered
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	return mustHSS(Config{
		Realm:                  realm,
		VisitedNetworks:        []string{realm},
		ServerCapabilities:     cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}},
		Admission:              policy,
		DefaultServiceProfile:  "default",
		TestUserServiceProfile: "default",
		ServiceProfiles: map[string]ServiceProfile{"default": {Criteria: []ifc.InitialFilterCriteria{{
			TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, SessionCase: &unregistered}}},
			ApplicationServer: ifc.ApplicationServer{ServerName: "sip:mmtel." + realm + ":5060"},
		}}}},
	}, store, node)
}

// TestAdmissionRefusesRegistration is TS 29.228 6.1.1.1 step 5 for a
// subscriber that the policy does not admit: the registration and its
// refresh are refused with DIAMETER_AUTHORIZATION_REJECTED, while the
// branches that step 5 does not authorize go through. A UAR without a
// User-Authorization-Type reads as REGISTRATION (cx.Registration is 0),
// so that row of the procedure is this one.
func TestAdmissionRefusesRegistration(t *testing.T) {
	rejected := diameter.Base(diameter.DiameterAuthorizationRejected)
	for _, policy := range Admissions {
		for _, user := range []struct {
			name            string
			private, public string
		}{
			{"an ordinary subscriber", impi, impu},
			{"a test user", testIMPI, testIMPU},
		} {
			admitted := policy == AdmitAll || user.private == testIMPI
			t.Run(string(policy)+", "+user.name, func(t *testing.T) {
				h := admissionHSS(t, policy)
				want := diameter.Experimental(cx.DiameterFirstRegistration)
				if !admitted {
					want = rejected
				}
				if a := uar(t, h, user.private, user.public, cx.Registration); a.Result != want {
					t.Errorf("UAA for REGISTRATION = %+v, want %+v", a.Result, want)
				}
				want = diameter.Base(diameter.DiameterSuccess)
				if !admitted {
					want = rejected
				}
				if a := uar(t, h, user.private, user.public, cx.RegistrationAndCapabilities); a.Result != want {
					t.Errorf("UAA for REGISTRATION_AND_CAPABILITIES = %+v, want %+v", a.Result, want)
				}
				// Step 5 asks nothing of DE_REGISTRATION: refusing it would
				// leave the user unable to end a registration that the S-CSCF
				// and the P-CSCF still hold. This subscriber has no
				// registration, so it gets the answer of step 6 either way.
				if a := uar(t, h, user.private, user.public, cx.DeRegistration); a.Result != diameter.Experimental(cx.DiameterErrorIdentityNotRegistered) {
					t.Errorf("UAA for DE_REGISTRATION = %+v, want IDENTITY_NOT_REGISTERED whatever the policy", a.Result)
				}
				// 6.1.4.1 step 3 answers for a subscriber whose profile has a
				// criterion of the unregistered state, whatever the policy, so
				// that a call to a user whose registration is still valid is
				// not cut off by the policy. The SAR that follows is where a
				// subscriber the policy does not admit is stopped.
				if a := lir(t, h, user.public); a.Result != diameter.Experimental(cx.DiameterUnregisteredService) {
					t.Errorf("LIA = %+v, want UNREGISTERED_SERVICE whatever the policy", a.Result)
				}
			})
		}
	}
}

// TestAdmissionRefusesTheUnregisteredState is the check that this
// project adds to TS 29.228 6.1.2.1 step 5: a subscriber the policy does
// not admit takes no S-CSCF for the services of the unregistered state,
// while every other assignment type goes through, so that the HSS is
// left with no half-finished registration.
func TestAdmissionRefusesTheUnregisteredState(t *testing.T) {
	rejected := diameter.Base(diameter.DiameterAuthorizationRejected)
	success := diameter.Base(diameter.DiameterSuccess)
	// Each type starts from a subscriber that is not registered, so each
	// case gets an HSS of its own.
	tests := []struct {
		typ            cx.ServerAssignmentType
		notAdmittedGet diameter.Result
	}{
		{cx.UnregisteredUser, rejected},
		// The S-CSCF never sends these for a subscriber whose UAR was
		// refused; they are here because refusing them would leave the
		// registration state and the authentication mark behind.
		{cx.AssignRegistration, success},
		{cx.ReRegistration, success},
		{cx.TimeoutDeregistration, success},
		{cx.UserDeregistration, success},
		{cx.AdministrativeDeregistration, success},
		{cx.AuthenticationFailure, success},
		{cx.AuthenticationTimeout, success},
	}
	for _, tt := range tests {
		for _, policy := range Admissions {
			t.Run(tt.typ.String()+", "+string(policy), func(t *testing.T) {
				h := admissionHSS(t, policy)
				// NO_ASSIGNMENT is left out: it answers the user profile only
				// to the S-CSCF already assigned, and no S-CSCF is here.
				want := success
				if policy == AdmitTestUsers {
					want = tt.notAdmittedGet
				}
				if a := sar(t, h, scscf, tt.typ); a.Result != want {
					t.Errorf("SAA = %+v, want %+v", a.Result, want)
				}
				// A test user is admitted by either policy.
				a, err := cx.ParseServerAssignmentAnswer(exchange(t, h, cx.ServerAssignmentRequest{Header: hdr,
					UserName: testIMPI, PublicIdentities: []string{testIMPU}, ServerName: scscf, ServerAssignmentType: tt.typ}.Message()))
				if err != nil {
					t.Fatal(err)
				}
				if a.Result != success {
					t.Errorf("SAA for the test user = %+v, want DIAMETER_SUCCESS", a.Result)
				}
			})
		}
	}
}

// TestAdmissionLeavesValidRegistrations: the policy of the moment
// decides the requests that arrive from then on, and nothing more. A
// registration that is valid when the policy narrows stays valid: the
// HSS sends no Registration-Termination-Request, calls to the user
// still find its S-CSCF, and the user drops out when the S-CSCF lets
// the registration expire.
func TestAdmissionLeavesValidRegistrations(t *testing.T) {
	h := admissionHSS(t, AdmitAll)
	mar(t, h, scscf)
	if a := sar(t, h, scscf, cx.AssignRegistration); !a.Result.Success() {
		t.Fatalf("SAA of the registration = %+v", a.Result)
	}
	h.changeSettings(func(s *settings) { s.admission = AdmitTestUsers })

	u, _, ok := h.find(t.Context(), impi, "")
	if !ok || u.State != Registered {
		t.Fatalf("the subscriber is %+v, want it registered still", u)
	}
	// The refresh is refused, so the registration ends when its time is
	// up. The S-CSCF holds the expiry; the HSS neither counts it down nor
	// tells the S-CSCF to end it.
	if a := uar(t, h, impi, impu, cx.Registration); a.Result != diameter.Base(diameter.DiameterAuthorizationRejected) {
		t.Errorf("UAA for the refresh = %+v, want DIAMETER_AUTHORIZATION_REJECTED", a.Result)
	}
	if a := lir(t, h, impu); a.Result != diameter.Base(diameter.DiameterSuccess) || a.ServerName != scscf {
		t.Errorf("LIA while the registration is valid = %+v, want %s", a, scscf)
	}
	// A terminating request for a subscriber the HSS holds as registered
	// is answered, whatever the policy: it is that valid registration
	// that the request belongs to (6.1.2.1 step 5 then has the HSS keep
	// the profile and set the state to unregistered).
	if a := sar(t, h, scscf, cx.UnregisteredUser); !a.Result.Success() {
		t.Errorf("SAA for UNREGISTERED_USER of a registered subscriber = %+v, want DIAMETER_SUCCESS", a.Result)
	}
	// The deregistration goes through, and from then on the subscriber
	// has no valid registration: the next UNREGISTERED_USER is refused,
	// although a server name was stored for it until now.
	if a := sar(t, h, scscf, cx.UserDeregistration); !a.Result.Success() {
		t.Errorf("SAA of the deregistration = %+v", a.Result)
	}
	if a := sar(t, h, scscf, cx.UnregisteredUser); a.Result != diameter.Base(diameter.DiameterAuthorizationRejected) {
		t.Errorf("SAA for UNREGISTERED_USER once the registration is gone = %+v, want DIAMETER_AUTHORIZATION_REJECTED", a.Result)
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
