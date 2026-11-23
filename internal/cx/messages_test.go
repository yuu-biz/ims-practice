package cx

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
)

const (
	realm  = "ims.mnc001.mcc001.3gppnetwork.org"
	impi   = "001010000000001@" + realm
	impu   = "sip:001010000000001@" + realm
	scscf  = "sip:scscf." + realm + ":5060"
	hssID  = "hss." + realm
	icscfH = "icscf." + realm
)

var hss = diameter.Identity{OriginHost: hssID, OriginRealm: realm}

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

// wire serializes m and reads it back, as a peer would.
func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
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

var header = diameter.Header{SessionID: icscfH + ";1;2", OriginHost: icscfH, OriginRealm: realm, DestinationRealm: realm}

func TestUserAuthorizationRoundTrip(t *testing.T) {
	uar := UserAuthorizationRequest{Header: header, UserName: impi, PublicIdentity: impu,
		VisitedNetworkIdentifier: realm, UserAuthorizationType: DeRegistration}
	req := wire(t, uar.Message())
	got, err := ParseUserAuthorizationRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, uar) {
		t.Errorf("UAR\n got %+v\nwant %+v", got, uar)
	}

	uaa := UserAuthorizationAnswer{Result: diameter.Experimental(DiameterFirstRegistration),
		ServerCapabilities: &ServerCapabilities{MandatoryCapabilities: []uint32{1}, ServerNames: []string{scscf}}}
	ans := wire(t, uaa.Message(req, hss))
	if ans.Header.HopByHopID != req.Header.HopByHopID || ans.Header.CommandFlags&diam.RequestFlag != 0 {
		t.Error("answer does not match its request")
	}
	gotA, err := ParseUserAuthorizationAnswer(ans)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotA, uaa) {
		t.Errorf("UAA\n got %+v\nwant %+v", gotA, uaa)
	}
	// TS 29.229 6.2: an Experimental-Result-Code goes without a
	// Result-Code.
	if _, err := ans.FindAVP(avp.ResultCode, 0); err == nil {
		t.Error("UAA with an Experimental-Result also has a Result-Code")
	}
}

func TestMultimediaAuthRoundTrip(t *testing.T) {
	mar := MultimediaAuthRequest{Header: header, UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeUnknown, SIPNumberAuthItems: 1, ServerName: scscf}
	req := wire(t, mar.Message())
	got, err := ParseMultimediaAuthRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, mar) {
		t.Errorf("MAR\n got %+v\nwant %+v", got, mar)
	}

	maa := MultimediaAuthAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeSIPDigest,
		SIPDigestAuthenticate: &SIPDigestAuthenticate{DigestRealm: realm, DigestAlgorithm: "MD5",
			DigestQoP: "auth", DigestHA1: "939e7578ed9e3c518a452acee763bce9"}}
	gotA, err := ParseMultimediaAuthAnswer(wire(t, maa.Message(req, hss)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotA, maa) {
		t.Errorf("MAA\n got %+v\nwant %+v", gotA, maa)
	}
}

// akaVector is an authentication vector with the outputs of TS 35.208
// test set 1; the cx package only carries it.
var akaVector = aka.AuthenticationVector{
	RAND: [16]byte{0x23, 0x55, 0x3c, 0xbe, 0x96, 0x37, 0xa8, 0x9d, 0x21, 0x8a, 0xe6, 0x4d, 0xae, 0x47, 0xbf, 0x35},
	XRES: []byte{0xa5, 0x42, 0x11, 0xd5, 0xe3, 0xba, 0x50, 0xbf},
	CK:   [16]byte{0xb4, 0x0b, 0xa9, 0xa3, 0xc5, 0x8b, 0x2a, 0x05, 0xbb, 0xf0, 0xd9, 0x87, 0xb2, 0x1b, 0xf8, 0xcb},
	IK:   [16]byte{0xf7, 0x69, 0xbc, 0xd7, 0x51, 0x04, 0x46, 0x04, 0x12, 0x76, 0x72, 0x71, 0x1c, 0x6d, 0x34, 0x41},
	AUTN: [16]byte{0x55, 0xf3, 0x28, 0xb4, 0x35, 0x77, 0xb9, 0xb9, 0x4a, 0x9f, 0xfa, 0xc3, 0x54, 0xdf, 0xaf, 0xb3},
}

func TestMultimediaAuthRoundTripForAKA(t *testing.T) {
	mar := MultimediaAuthRequest{Header: header, UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeDigestAKAv1MD5, SIPNumberAuthItems: 1, ServerName: scscf,
		SIPAuthorization: &Resynchronization{RAND: akaVector.RAND, AUTS: aka.AUTS{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}}}
	req := wire(t, mar.Message())
	got, err := ParseMultimediaAuthRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, mar) {
		t.Errorf("MAR\n got %+v\nwant %+v", got, mar)
	}

	maa := MultimediaAuthAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeDigestAKAv1MD5, AuthenticationVector: &akaVector}
	gotA, err := ParseMultimediaAuthAnswer(wire(t, maa.Message(req, hss)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotA, maa) {
		t.Errorf("MAA\n got %+v\nwant %+v", gotA, maa)
	}
}

// TestAKAItemsOnTheWire checks the AVPs of an IMS-AKA exchange by code,
// flags and length, as another Diameter node reads them: TS 29.229
// Table 6.3.0.1 and 6.3.10, 6.3.11, 6.3.13.
func TestAKAItemsOnTheWire(t *testing.T) {
	resync := &Resynchronization{RAND: akaVector.RAND}
	req := wire(t, MultimediaAuthRequest{Header: header, UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeDigestAKAv1MD5, SIPNumberAuthItems: 1, ServerName: scscf, SIPAuthorization: resync}.Message())
	if got := itemAVPs(t, req); !reflect.DeepEqual(got, []itemAVP{{608, 16}, {610, 30}}) {
		t.Errorf("MAR SIP-Auth-Data-Item = %v, want SIP-Authentication-Scheme and a 30-octet SIP-Authorization", got)
	}

	ans := wire(t, MultimediaAuthAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeDigestAKAv1MD5, AuthenticationVector: &akaVector}.Message(req, hss))
	want := []itemAVP{{613, 4}, {608, 16}, {609, 32}, {610, 8}, {625, 16}, {626, 16}}
	if got := itemAVPs(t, ans); !reflect.DeepEqual(got, want) {
		t.Errorf("MAA SIP-Auth-Data-Item = %v, want %v", got, want)
	}
}

type itemAVP struct {
	code   uint32
	length int
}

// itemAVPs lists the AVPs inside the SIP-Auth-Data-Item of m in order,
// failing on any that is not M and V with the 3GPP Vendor-Id.
func itemAVPs(t *testing.T, m *diam.Message) []itemAVP {
	t.Helper()
	item, err := m.FindAVP(avpSIPAuthDataItem, diameter.VendorID3GPP)
	if err != nil {
		t.Fatal(err)
	}
	var out []itemAVP
	for _, a := range item.Data.(*diam.GroupedAVP).AVP {
		if a.Flags != avp.Mbit|avp.Vbit || a.VendorID != diameter.VendorID3GPP {
			t.Errorf("AVP %d flags %#x vendor %d, want M and V with 10415", a.Code, a.Flags, a.VendorID)
		}
		out = append(out, itemAVP{a.Code, a.Data.Len()})
	}
	return out
}

func TestParseMultimediaAuthRequestRefusesShortSIPAuthorization(t *testing.T) {
	m := diameter.NewRequest(diameter.ApplicationIDCx, CommandMultimediaAuth)
	begin(m, header)
	m.AddAVP(diameter.VendorAVP(avpSIPAuthDataItem, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
		diameter.VendorAVP(avpSIPAuthenticationScheme, mv, datatype.UTF8String(SchemeDigestAKAv1MD5)),
		diameter.VendorAVP(avpSIPAuthorization, mv, datatype.OctetString(make([]byte, 29))),
	}}))
	if _, err := ParseMultimediaAuthRequest(wire(t, m)); err == nil {
		t.Fatal("a 29-octet SIP-Authorization was accepted")
	}
}

func TestServerAssignmentRoundTrip(t *testing.T) {
	sar := ServerAssignmentRequest{Header: header, UserName: impi, PublicIdentities: []string{impu},
		ServerName: scscf, ServerAssignmentType: AssignRegistration}
	req := wire(t, sar.Message())
	got, err := ParseServerAssignmentRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sar) {
		t.Errorf("SAR\n got %+v\nwant %+v", got, sar)
	}

	saa := ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: impi, UserData: []byte("<IMSSubscription/>")}
	gotA, err := ParseServerAssignmentAnswer(wire(t, saa.Message(req, hss)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotA, saa) {
		t.Errorf("SAA\n got %+v\nwant %+v", gotA, saa)
	}
}

func TestAVPFlagsFollowTS29229(t *testing.T) {
	maa := MultimediaAuthAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: impi, PublicIdentity: impu,
		SIPAuthenticationScheme: SchemeSIPDigest,
		SIPDigestAuthenticate:   &SIPDigestAuthenticate{DigestRealm: realm, DigestQoP: "auth", DigestHA1: "00"}}
	m := wire(t, maa.Message(wire(t, MultimediaAuthRequest{Header: header}.Message()), hss))

	pub, err := m.FindAVP(avpPublicIdentity, diameter.VendorID3GPP)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Flags != avp.Mbit|avp.Vbit || pub.VendorID != diameter.VendorID3GPP {
		t.Errorf("Public-Identity flags %#x vendor %d, want M and V with 10415", pub.Flags, pub.VendorID)
	}
	digest, err := m.FindAVP(avpSIPDigestAuthenticate, diameter.VendorID3GPP)
	if err != nil {
		t.Fatal(err)
	}
	// Table 6.3.0.1: SIP-Digest-Authenticate must set V and must not set M.
	if digest.Flags != avp.Vbit {
		t.Errorf("SIP-Digest-Authenticate flags %#x, want V only", digest.Flags)
	}
	realmAVP, err := m.FindAVP(avpDigestRealm, 0)
	if err != nil {
		t.Fatal(err)
	}
	if realmAVP.Flags != avp.Mbit || realmAVP.VendorID != 0 {
		t.Errorf("Digest-Realm flags %#x vendor %d, want M only and no vendor", realmAVP.Flags, realmAVP.VendorID)
	}
}

func TestUserProfileRoundTrip(t *testing.T) {
	want := UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+0010000000001"}}
	got, err := ParseUserProfile(want.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := ParseUserProfile([]byte("<IMSSubscription/>")); err == nil {
		t.Error("a profile without a public identity was accepted")
	}
}

// TestUserProfileBarringIndication checks that a barred identity carries
// BarringIndication before its Identity, as tPublicIdentity of TS 29.228
// Annex E orders them, and that one without it is not barred.
func TestUserProfileBarringIndication(t *testing.T) {
	temporary := "sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org"
	want := UserProfile{PrivateIdentity: impi,
		PublicIdentities: []string{temporary, "sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org", "tel:+12025550110"},
		BarredIdentities: []string{temporary}}
	data := want.Marshal()
	if !regexp.MustCompile(`<PublicIdentity><BarringIndication>true</BarringIndication><Identity>` + regexp.QuoteMeta(temporary) + `</Identity></PublicIdentity><PublicIdentity><Identity>`).Match(data) {
		t.Errorf("User-Data does not bar the temporary identity alone:\n%s", data)
	}
	got, err := ParseUserProfile(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// Written as in the examples of TS 29.228 Annex B, with 1 and 0.
	got, err = ParseUserProfile([]byte(`<IMSSubscription><PrivateID>` + impi + `</PrivateID><ServiceProfile>` +
		`<PublicIdentity><BarringIndication>1</BarringIndication><Identity>sip:a@example.org</Identity></PublicIdentity>` +
		`<PublicIdentity><BarringIndication>0</BarringIndication><Identity>sip:b@example.org</Identity></PublicIdentity>` +
		`</ServiceProfile></IMSSubscription>`))
	if err != nil || !reflect.DeepEqual(got.BarredIdentities, []string{"sip:a@example.org"}) {
		t.Errorf("barred %v, error %v; want only sip:a@example.org", got.BarredIdentities, err)
	}
}

// TestUserProfileWithInitialFilterCriteria checks that the criteria sit
// inside ServiceProfile, after the public identities, as tServiceProfile
// of TS 29.228 Annex E orders them.
func TestUserProfileWithInitialFilterCriteria(t *testing.T) {
	want := UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu}, InitialFilterCriteria: []ifc.InitialFilterCriteria{{
		Priority:          0,
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "REGISTER"}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:as.example.org", ServiceInfo: "12025550101"},
	}}}
	data := want.Marshal()
	if !regexp.MustCompile(`<ServiceProfile><PublicIdentity>.*</PublicIdentity><InitialFilterCriteria><Priority>0</Priority>`).Match(data) {
		t.Errorf("User-Data has the criteria out of place:\n%s", data)
	}
	got, err := ParseUserProfile(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	broken := UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu},
		InitialFilterCriteria: []ifc.InitialFilterCriteria{want.InitialFilterCriteria[0], want.InitialFilterCriteria[0]}}
	if _, err := ParseUserProfile(broken.Marshal()); err == nil {
		t.Error("a profile with two criteria of the same priority was accepted")
	}
}

// TestUserProfileOfAPSIUser checks that a public service identity that
// matches a wildcarded PSI carries IdentityType 2 (WILDCARDED_PSI) and
// the wildcarded PSI in an Extension after its Identity, as
// tPublicIdentity and tPublicIdentityExtension of TS 29.228 Table E.2
// order them, and that a public user identity has no Extension, which
// makes it a distinct one (B.2.1).
func TestUserProfileOfAPSIUser(t *testing.T) {
	want := UserProfile{PrivateIdentity: "conference@" + realm,
		PublicIdentities: []string{"sip:conf-7f3a@conf." + realm},
		WildcardedPSI:    "sip:!.*!@conf." + realm}
	data := want.Marshal()
	if !regexp.MustCompile(`<PublicIdentity><Identity>sip:conf-7f3a@conf\.[^<]*</Identity><Extension><IdentityType>2</IdentityType><WildcardedPSI>sip:!\.\*!@conf\.[^<]*</WildcardedPSI></Extension></PublicIdentity>`).Match(data) {
		t.Errorf("User-Data does not type the identity as one of a wildcarded PSI:\n%s", data)
	}
	got, err := ParseUserProfile(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	user := UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu}}.Marshal()
	if strings.Contains(string(user), "Extension") {
		t.Errorf("a public user identity has an Extension:\n%s", user)
	}
	if got, err := ParseUserProfile(user); err != nil || got.WildcardedPSI != "" {
		t.Errorf("a public user identity came back with wildcarded PSI %q, error %v", got.WildcardedPSI, err)
	}
}

func TestLocationInfoRoundTrip(t *testing.T) {
	lir := LocationInfoRequest{Header: header, PublicIdentity: impu}
	req := wire(t, lir.Message())
	got, err := ParseLocationInfoRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, lir) {
		t.Errorf("LIR\n got %+v\nwant %+v", got, lir)
	}
	lia := LocationInfoAnswer{Result: diameter.Base(diameter.DiameterSuccess), ServerName: scscf}
	gotA, err := ParseLocationInfoAnswer(wire(t, lia.Message(req, hss)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotA, lia) {
		t.Errorf("LIA\n got %+v\nwant %+v", gotA, lia)
	}
}

func TestRegistrationTerminationRoundTrip(t *testing.T) {
	rtr := RegistrationTerminationRequest{
		Header:               diameter.Header{SessionID: hssID + ";1;3", OriginHost: hssID, OriginRealm: realm, DestinationHost: "scscf." + realm, DestinationRealm: realm},
		UserName:             impi,
		PublicIdentities:     []string{impu, "tel:+12025550101"},
		DeregistrationReason: DeregistrationReason{ReasonCode: PermanentTermination, ReasonInfo: "subscription removed"},
	}
	req := wire(t, rtr.Message())
	if req.Header.CommandCode != 304 || req.Header.CommandFlags&diam.RequestFlag == 0 {
		t.Errorf("header %+v, want an RTR (304 with R set, TS 29.229 6.1.9)", req.Header)
	}
	got, err := ParseRegistrationTerminationRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rtr) {
		t.Errorf("RTR\n got %+v\nwant %+v", got, rtr)
	}
	reason, err := req.FindAVP(avpDeregistrationReason, diameter.VendorID3GPP)
	if err != nil {
		t.Fatal(err)
	}
	if reason.Flags != avp.Mbit|avp.Vbit {
		t.Errorf("Deregistration-Reason flags %#x, want M and V", reason.Flags)
	}

	// No Public-Identity: all of those of User-Name.
	whole := rtr
	whole.PublicIdentities, whole.DeregistrationReason.ReasonInfo = nil, ""
	if got, err := ParseRegistrationTerminationRequest(wire(t, whole.Message())); err != nil || !reflect.DeepEqual(got, whole) {
		t.Errorf("RTR without Public-Identity: %+v, %v", got, err)
	}

	rta := RegistrationTerminationAnswer{Result: diameter.Experimental(DiameterErrorUserUnknown)}
	ans := wire(t, rta.Message(req, diameter.Identity{OriginHost: "scscf." + realm, OriginRealm: realm}))
	if ans.Header.HopByHopID != req.Header.HopByHopID || ans.Header.CommandFlags&diam.RequestFlag != 0 {
		t.Errorf("RTA header %+v does not answer the RTR", ans.Header)
	}
	if got, err := ParseRegistrationTerminationAnswer(ans); err != nil || got != rta {
		t.Errorf("RTA = %+v, %v; want %+v", got, err, rta)
	}
}

func TestPushProfileRoundTrip(t *testing.T) {
	profile := UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+12025550101"}}
	ppr := PushProfileRequest{
		Header:   diameter.Header{SessionID: hssID + ";1;4", OriginHost: hssID, OriginRealm: realm, DestinationHost: "scscf." + realm, DestinationRealm: realm},
		UserName: impi,
		UserData: profile.Marshal(),
	}
	req := wire(t, ppr.Message())
	if req.Header.CommandCode != 305 || req.Header.CommandFlags&diam.RequestFlag == 0 {
		t.Errorf("header %+v, want a PPR (305 with R set, TS 29.229 6.1.11)", req.Header)
	}
	got, err := ParsePushProfileRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, ppr) {
		t.Errorf("PPR\n got %+v\nwant %+v", got, ppr)
	}
	// TS 29.228 Table 6.2.2.1: Destination-Host names the S-CSCF that
	// last stored the server name, so a PPR is never routed by realm
	// alone.
	if _, err := req.FindAVP(avp.DestinationHost, 0); err != nil {
		t.Error("PPR without Destination-Host")
	}
	if back, err := ParseUserProfile(got.UserData); err != nil || !reflect.DeepEqual(back, profile) {
		t.Errorf("User-Data of the PPR = %+v, %v; want %+v", back, err, profile)
	}

	ppa := PushProfileAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	ans := wire(t, ppa.Message(req, diameter.Identity{OriginHost: "scscf." + realm, OriginRealm: realm}))
	if ans.Header.HopByHopID != req.Header.HopByHopID || ans.Header.CommandFlags&diam.RequestFlag != 0 {
		t.Errorf("PPA header %+v does not answer the PPR", ans.Header)
	}
	if got, err := ParsePushProfileAnswer(ans); err != nil || got != ppa {
		t.Errorf("PPA = %+v, %v; want %+v", got, err, ppa)
	}

	// Table 6.2.2.1.1: the errors that the S-CSCF returns are Cx errors,
	// so they travel in Experimental-Result (TS 29.229 6.2).
	refused := PushProfileAnswer{Result: diameter.Experimental(DiameterErrorNotSupportedUserData)}
	if got, err := ParsePushProfileAnswer(wire(t, refused.Message(req, hss))); err != nil || got != refused {
		t.Errorf("PPA = %+v, %v; want %+v", got, err, refused)
	}
}
