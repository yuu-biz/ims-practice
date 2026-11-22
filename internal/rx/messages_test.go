package rx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/policy"
)

const (
	realm = "ims.mnc001.mcc001.3gppnetwork.org"
	pcscf = "pcscf." + realm
	pcrf  = "pcrf." + realm
)

var (
	pcrfID  = diameter.Identity{OriginHost: pcrf, OriginRealm: realm}
	pcscfID = diameter.Identity{OriginHost: pcscf, OriginRealm: realm}
	toPCRF  = diameter.Header{SessionID: pcscf + ";1;2", OriginHost: pcscf, OriginRealm: realm, DestinationRealm: realm}
	toPCSCF = diameter.Header{SessionID: pcscf + ";1;2", OriginHost: pcrf, OriginRealm: realm, DestinationHost: pcscf, DestinationRealm: realm}
	ueAddr  = netip.MustParseAddr("172.28.0.110")
	ueHost  = netip.PrefixFrom(ueAddr, 32)
	relay   = netip.MustParsePrefix("172.28.0.50/32")
	initial = InitialRequest
	final   = FinalServiceInformation
)

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

func u32(v uint32) *uint32 { return &v }

// callComponent is the audio component of a call as package policy
// derives it.
var callComponent = policy.MediaComponent{
	Number: 1, Type: policy.Audio, FlowStatus: policy.Enabled,
	MaxRequestedBandwidthUL: u32(64000), MaxRequestedBandwidthDL: u32(80000),
	RRBandwidth: u32(2000), RSBandwidth: u32(600),
	Codecs: []policy.CodecData{
		{Direction: policy.Uplink, Lines: []string{"m=audio 49000 RTP/AVP 0", "a=rtpmap:0 PCMU/8000"}},
		{Direction: policy.Downlink, Answer: true, Lines: []string{"m=audio 40000 RTP/AVP 0", "a=rtpmap:0 PCMU/8000"}},
	},
	SubComponents: []policy.MediaSubComponent{
		{FlowNumber: 1, Flows: []policy.FlowDescription{
			{Direction: policy.Uplink, Protocol: 17, Source: ueHost, Destination: relay, DestinationPort: 40000},
			{Direction: policy.Downlink, Protocol: 17, Source: relay, Destination: ueHost, DestinationPort: 49000},
		}},
		{FlowNumber: 2, Usage: policy.RTCP, Flows: []policy.FlowDescription{
			{Direction: policy.Uplink, Protocol: 17, Source: ueHost, Destination: relay, DestinationPort: 40001},
			{Direction: policy.Downlink, Protocol: 17, Source: relay, Destination: ueHost, DestinationPort: 49001},
		}},
	},
}

func TestAARoundTrip(t *testing.T) {
	for _, aar := range []AARequest{
		{Header: toPCRF, AFApplicationIdentifier: "urn:urn-7:3gpp-service.ims.icsi.mmtel",
			MediaComponents: []policy.MediaComponent{callComponent}, ServiceInfoStatus: &final,
			SpecificActions: []SpecificAction{IndicationOfReleaseOfBearer, IndicationOfSuccessfulResourcesAllocation},
			FramedIPAddress: ueAddr, RxRequestType: &initial},
		// The subscription to the signalling path (TS 29.214 4.4.5).
		{Header: toPCRF, MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
			SpecificActions: []SpecificAction{IndicationOfLossOfBearer, IndicationOfReleaseOfBearer}, FramedIPAddress: ueAddr},
	} {
		req := wire(t, aar.Message())
		got, err := ParseAARequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, aar) {
			t.Errorf("AAR\n got %+v\nwant %+v", got, aar)
		}
		for _, aaa := range []AAAnswer{{Result: diameter.Base(diameter.DiameterSuccess)}, {Result: diameter.Experimental(IPCANSessionNotAvailable)}} {
			ans := wire(t, aaa.Message(req, pcrfID))
			if ans.Header.CommandCode != CommandAA || ans.Header.ApplicationID != diameter.ApplicationIDRx || SessionID(ans) != aar.SessionID {
				t.Errorf("AAA header %+v, Session-Id %q", ans.Header, SessionID(ans))
			}
			gotA, err := ParseAAAnswer(ans)
			if err != nil || gotA != aaa {
				t.Errorf("AAA = %+v, %v; want %+v", gotA, err, aaa)
			}
		}
	}
}

func TestReAuthRoundTrip(t *testing.T) {
	rar := ReAuthRequest{Header: toPCSCF, SpecificActions: []SpecificAction{IndicationOfSuccessfulResourcesAllocation},
		Flows: []Flows{{MediaComponentNumber: 1, FlowNumbers: []uint32{1, 2}}}}
	req := wire(t, rar.Message())
	got, err := ParseReAuthRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rar) {
		t.Errorf("RAR\n got %+v\nwant %+v", got, rar)
	}
	raa := ReAuthAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	if gotA, err := ParseReAuthAnswer(wire(t, raa.Message(req, pcscfID))); err != nil || gotA != raa {
		t.Errorf("RAA = %+v, %v; want %+v", gotA, err, raa)
	}
}

func TestSessionTerminationRoundTrip(t *testing.T) {
	str := SessionTerminationRequest{Header: toPCRF, TerminationCause: DiameterLogout}
	req := wire(t, str.Message())
	if got, err := ParseSessionTerminationRequest(req); err != nil || got != str {
		t.Errorf("STR = %+v, %v; want %+v", got, err, str)
	}
	sta := SessionTerminationAnswer{Result: diameter.Base(diameter.DiameterUnknownSessionID)}
	if got, err := ParseSessionTerminationAnswer(wire(t, sta.Message(req, pcrfID))); err != nil || got != sta {
		t.Errorf("STA = %+v, %v; want %+v", got, err, sta)
	}
}

func TestAbortSessionRoundTrip(t *testing.T) {
	asr := AbortSessionRequest{Header: toPCSCF, AbortCause: BearerReleased}
	req := wire(t, asr.Message())
	if got, err := ParseAbortSessionRequest(req); err != nil || got != asr {
		t.Errorf("ASR = %+v, %v; want %+v", got, err, asr)
	}
	asa := AbortSessionAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	if got, err := ParseAbortSessionAnswer(wire(t, asa.Message(req, pcscfID))); err != nil || got != asa {
		t.Errorf("ASA = %+v, %v; want %+v", got, err, asa)
	}
}

// avpAt reads the AVP header at b[0:]: code, flags, length and vendor
// (RFC 6733 4.1).
func avpAt(b []byte) (code uint32, flags byte, length int, vendor uint32) {
	code = binary.BigEndian.Uint32(b)
	flags = b[4]
	length = int(binary.BigEndian.Uint32(b[4:]) & 0xffffff)
	if flags&0x80 != 0 {
		vendor = binary.BigEndian.Uint32(b[8:])
	}
	return
}

// topLevel returns the AVPs of a serialized message by code.
func topLevel(b []byte) map[uint32][]byte {
	avps := map[uint32][]byte{}
	for off := 20; off < len(b); {
		code, _, length, _ := avpAt(b[off:])
		if _, seen := avps[code]; !seen {
			avps[code] = b[off : off+length]
		}
		off += (length + 3) &^ 3
	}
	return avps
}

// TestAARequestBytes checks the encoding against the numbers of TS
// 29.214 rather than against this package's own decoder: the header of
// 5.6.1, the codes and flags of Table 5.3.0.1, and the Framed-IP-Address
// of NASREQ.
func TestAARequestBytes(t *testing.T) {
	b, err := AARequest{Header: toPCRF, MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
		SpecificActions: []SpecificAction{IndicationOfReleaseOfBearer}, FramedIPAddress: ueAddr, RxRequestType: &initial}.Message().Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], uint32(b[5])<<16|uint32(b[6])<<8|uint32(b[7]), binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 265 || app != 16777236 {
		t.Fatalf("header: flags %#x, command %d, application %d; want R and P set, 265, 16777236", flags, code, app)
	}
	avps := topLevel(b)
	// Auth-Application-Id (258, M) holds 16777236.
	if want := []byte{0, 0, 1, 2, 0x40, 0, 0, 12, 0x01, 0, 0, 0x14}; !bytes.Equal(avps[258], want) {
		t.Errorf("Auth-Application-Id = % x, want % x", avps[258], want)
	}
	// Framed-IP-Address (8, M, no vendor) holds the four octets.
	if want := []byte{0, 0, 0, 8, 0x40, 0, 0, 12, 172, 28, 0, 110}; !bytes.Equal(avps[8], want) {
		t.Errorf("Framed-IP-Address = % x, want % x", avps[8], want)
	}
	// Specific-Action (513, V and M) INDICATION_OF_RELEASE_OF_BEARER (4).
	if want := []byte{0, 0, 2, 1, 0xc0, 0, 0, 16, 0, 0, 0x28, 0xaf, 0, 0, 0, 4}; !bytes.Equal(avps[513], want) {
		t.Errorf("Specific-Action = % x, want % x", avps[513], want)
	}
	// Rx-Request-Type (533) sets V and not M.
	if want := []byte{0, 0, 2, 0x15, 0x80, 0, 0, 16, 0, 0, 0x28, 0xaf, 0, 0, 0, 0}; !bytes.Equal(avps[533], want) {
		t.Errorf("Rx-Request-Type = % x, want % x", avps[533], want)
	}
	// Media-Component-Description (517) holds Media-Component-Number (518)
	// 0, then a Media-Sub-Component (519) with Flow-Number (509) 0 and
	// Flow-Usage (512) AF_SIGNALLING (2).
	mcd := avps[517]
	if code, flags, _, vendor := avpAt(mcd); code != 517 || flags != 0xc0 || vendor != 10415 {
		t.Fatalf("Media-Component-Description header = % x", mcd[:12])
	}
	want := []byte{
		0, 0, 2, 6, 0xc0, 0, 0, 16, 0, 0, 0x28, 0xaf, 0, 0, 0, 0,
		0, 0, 2, 7, 0xc0, 0, 0, 44, 0, 0, 0x28, 0xaf,
		0, 0, 1, 0xfd, 0xc0, 0, 0, 16, 0, 0, 0x28, 0xaf, 0, 0, 0, 0,
		0, 0, 2, 0, 0xc0, 0, 0, 16, 0, 0, 0x28, 0xaf, 0, 0, 0, 2,
	}
	if !bytes.Equal(mcd[12:], want) {
		t.Errorf("Media-Component-Description contents\n got % x\nwant % x", mcd[12:], want)
	}
}

// TS 29.214 Table 5.3.0.1 and 5.6. A code or flag that go-diameter got
// wrong would still round-trip here, but Wireshark and a product PCRF
// would read something else.
func TestRxDictionaryMatchesTS29214(t *testing.T) {
	for code, short := range map[uint32]string{265: "AA", 258: "RA", 275: "ST", 274: "AS"} {
		c, err := dict.Default.FindCommand(diameter.ApplicationIDRx, code)
		if err != nil || c.Short != short {
			t.Errorf("command %d = %+v, %v, want %s", code, c, err, short)
		}
	}
	for _, want := range []struct {
		name string
		code uint32
		must string
		typ  datatype.TypeID
	}{
		{"Abort-Cause", 500, "M,V", datatype.EnumeratedType},
		{"AF-Application-Identifier", 504, "M,V", datatype.OctetStringType},
		{"Flow-Description", 507, "M,V", datatype.IPFilterRuleType},
		{"Flow-Number", 509, "M,V", datatype.Unsigned32Type},
		{"Flows", 510, "M,V", datatype.GroupedType},
		{"Flow-Status", 511, "M,V", datatype.EnumeratedType},
		{"Flow-Usage", 512, "M,V", datatype.EnumeratedType},
		{"Specific-Action", 513, "M,V", datatype.EnumeratedType},
		{"Max-Requested-Bandwidth-DL", 515, "M,V", datatype.Unsigned32Type},
		{"Max-Requested-Bandwidth-UL", 516, "M,V", datatype.Unsigned32Type},
		{"Media-Component-Description", 517, "M,V", datatype.GroupedType},
		{"Media-Component-Number", 518, "M,V", datatype.Unsigned32Type},
		{"Media-Sub-Component", 519, "M,V", datatype.GroupedType},
		{"Media-Type", 520, "M,V", datatype.EnumeratedType},
		{"RR-Bandwidth", 521, "M,V", datatype.Unsigned32Type},
		{"RS-Bandwidth", 522, "M,V", datatype.Unsigned32Type},
		{"Codec-Data", 524, "M,V", datatype.OctetStringType},
		{"Service-Info-Status", 527, "M,V", datatype.EnumeratedType},
		{"Rx-Request-Type", 533, "V", datatype.EnumeratedType},
	} {
		a, err := dict.Default.FindAVPWithVendor(diameter.ApplicationIDRx, want.name, diameter.VendorID3GPP)
		if err != nil {
			t.Errorf("%s: %v", want.name, err)
			continue
		}
		if a.Code != want.code || a.Must != want.must || a.Data.Type != want.typ {
			t.Errorf("%s = code %d, must %q, type %v; want %d, %q, %v", want.name, a.Code, a.Must, a.Data.Type, want.code, want.must, want.typ)
		}
	}
	// Framed-IP-Address and Termination-Cause have no vendor (TS 29.214
	// Table 5.4.0.1, RFC 6733 8.15).
	for name, code := range map[string]uint32{"Framed-IP-Address": 8, "Termination-Cause": 295} {
		a, err := dict.Default.FindAVP(diameter.ApplicationIDRx, name)
		if err != nil || a.Code != code || a.VendorID != 0 || a.Must != "M" {
			t.Errorf("%s = %+v, %v; want code %d, no vendor, M", name, a, err, code)
		}
	}
}

func TestParseAARequestNamesTheFaultInTheServiceInformation(t *testing.T) {
	m := AARequest{Header: toPCRF, FramedIPAddress: ueAddr}.Message()
	m.AddAVP(diameter.VendorAVP(avpMediaComponentDescription, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
		diameter.VendorAVP(avpMediaComponentNumber, mv, datatype.Unsigned32(1)),
		diameter.VendorAVP(avpMediaSubComponent, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
			diameter.VendorAVP(avpFlowNumber, mv, datatype.Unsigned32(1)),
			diameter.VendorAVP(avpFlowDescription, mv, datatype.IPFilterRule("permit in 17 from any to any 40000-40001")),
		}}),
	}}))
	if _, err := ParseAARequest(wire(t, m)); !errors.Is(err, ErrFilterRestrictions) {
		t.Errorf("port range: %v, want ErrFilterRestrictions", err)
	}
	m = AARequest{Header: toPCRF, FramedIPAddress: ueAddr}.Message()
	m.AddAVP(diameter.VendorAVP(avpMediaComponentDescription, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
		diameter.VendorAVP(avpMediaType, mv, datatype.Enumerated(0)),
	}}))
	if _, err := ParseAARequest(wire(t, m)); !errors.Is(err, ErrInvalidServiceInformation) {
		t.Errorf("no Media-Component-Number: %v, want ErrInvalidServiceInformation", err)
	}
}

// The dashboard reads Diameter as the JSON of diameter.EncodeJSON, so the
// Rx commands and AVPs must have their names there, as in Wireshark.
func TestRxMessagesHaveNamesInTheDashboardJSON(t *testing.T) {
	b, err := diameter.EncodeJSON(AARequest{Header: toPCRF, MediaComponents: []policy.MediaComponent{callComponent},
		SpecificActions: []SpecificAction{IndicationOfReleaseOfBearer}, FramedIPAddress: ueAddr}.Message())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"command":"AA"`, `"name":"Media-Component-Description"`, `"name":"Media-Sub-Component"`,
		`"name":"Flow-Description"`, `"name":"Codec-Data"`, `"name":"Specific-Action"`, `"name":"Framed-IP-Address"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON lacks %s: %s", want, b)
		}
	}
}
