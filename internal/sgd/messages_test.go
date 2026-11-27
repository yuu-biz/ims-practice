package sgd

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

// All identities are fictitious: PLMN 001/01 and numbers from +1 202 555
// 0100 to 0199.
const (
	realm     = "epc.mnc001.mcc001.3gppnetwork.org"
	scHost    = "sc." + realm
	mmeHost   = "mme." + realm
	imsi      = "001010000000001"
	msisdn    = "12025550112"
	scAddress = "12025550100"
	mmeNumber = "12025550190"
)

// TPDUs of TS 23.040, written out by hand.
const (
	// SMS-SUBMIT (9.2.2.2) to +1 202 555 0123 with the text "hello".
	smsSubmit = "01 2A 0B 91 21 20 55 05 21 F3 00 00 05 E8 32 9B FD 06"
	// SMS-DELIVER (9.2.2.1) from +1 202 555 0112 with the text "hello".
	smsDeliver = "04 0B 91 21 20 55 05 11 F2 00 00 62 11 02 60 83 13 00 05 E8 32 9B FD 06"
	// SMS-SUBMIT-REPORT for RP-ACK (9.2.2.2a): TP-MTI, TP-PI, TP-SCTS.
	smsSubmitReport = "01 00 62 11 02 60 83 13 00"
	// SMS-DELIVER-REPORT for RP-ACK (9.2.2.1a): TP-MTI, TP-PI.
	smsDeliverReport = "00 00"
)

var (
	sc  = diameter.Identity{OriginHost: scHost, OriginRealm: realm}
	mme = diameter.Identity{OriginHost: mmeHost, OriginRealm: realm}
)

func init() {
	if err := LoadDictionary(); err != nil {
		panic(err)
	}
}

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
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

func TestMOForwardShortMessageRoundTrip(t *testing.T) {
	ofr := MOForwardShortMessageRequest{
		Header:         diameter.Header{SessionID: mmeHost + ";1;1", OriginHost: mmeHost, OriginRealm: realm, DestinationRealm: realm},
		SCAddress:      scAddress,
		UserIdentifier: UserIdentifier{UserName: imsi, MSISDN: msisdn},
		SMRPUI:         mustHex(t, smsSubmit),
	}
	req := wire(t, ofr.Message())
	if h := req.Header; h.ApplicationID != ApplicationIDSGd || h.CommandCode != CommandMOForwardShortMessage ||
		h.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
		t.Errorf("OFR header = %+v", h)
	}
	// The peer that sends the request gives it its End-to-End
	// Identifier, which the receiver reads back.
	req.Header.EndToEndID = 0x5eed
	ofr.EndToEndID = 0x5eed
	got, err := ParseMOForwardShortMessageRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, ofr) {
		t.Errorf("OFR\n got %+v\nwant %+v", got, ofr)
	}

	for _, ofa := range []MOForwardShortMessageAnswer{
		{Result: diameter.Base(diameter.DiameterSuccess), SMRPUI: mustHex(t, smsSubmitReport)},
		{Result: diameter.Base(diameter.DiameterSuccess)},
		{Result: diameter.Experimental(DiameterErrorSMDeliveryFailure),
			SMDeliveryFailureCause: &SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: UnknownServiceCentre}},
		{Result: diameter.Experimental(DiameterErrorSMDeliveryFailure),
			SMDeliveryFailureCause: &SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: InvalidSMEAddress, SMDiagnosticInfo: []byte{0xC3}}},
		{Result: diameter.Experimental(DiameterErrorFacilityNotSupported)},
	} {
		ans := wire(t, ofa.Message(req, sc))
		if h := ans.Header; h.ApplicationID != ApplicationIDSGd || h.CommandCode != CommandMOForwardShortMessage ||
			h.CommandFlags != diam.ProxiableFlag || h.HopByHopID != req.Header.HopByHopID || h.EndToEndID != req.Header.EndToEndID {
			t.Errorf("OFA header = %+v", h)
		}
		gotA, err := ParseMOForwardShortMessageAnswer(ans)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotA, ofa) {
			t.Errorf("OFA\n got %+v\nwant %+v", gotA, ofa)
		}
	}
}

func TestMOForwardShortMessageUserIdentifier(t *testing.T) {
	// TS 29.338 Table 6.2.1.1/1: the IMSI if it is available, the MSISDN
	// when it exists. An IP-SM-GW may know only the MSISDN.
	for _, user := range []UserIdentifier{{UserName: imsi}, {MSISDN: msisdn}} {
		ofr := MOForwardShortMessageRequest{
			Header:         diameter.Header{SessionID: "s", OriginHost: mmeHost, OriginRealm: realm, DestinationHost: scHost, DestinationRealm: realm},
			SCAddress:      scAddress,
			UserIdentifier: user,
			SMRPUI:         mustHex(t, smsSubmit),
		}
		m := ofr.Message()
		ofr.EndToEndID = m.Header.EndToEndID
		got, err := ParseMOForwardShortMessageRequest(wire(t, m))
		if err != nil || !reflect.DeepEqual(got, ofr) {
			t.Errorf("got %+v, %v\nwant %+v", got, err, ofr)
		}
	}
}

func TestMTForwardShortMessageRoundTrip(t *testing.T) {
	for _, tfr := range []MTForwardShortMessageRequest{
		{ // to an MME
			Header:            diameter.Header{SessionID: scHost + ";1;2", OriginHost: scHost, OriginRealm: realm, DestinationHost: mmeHost, DestinationRealm: realm},
			UserName:          imsi,
			SCAddress:         scAddress,
			SMRPUI:            mustHex(t, smsDeliver),
			MMENumberForMTSMS: mmeNumber,
		},
		{ // to an SMSF or an IP-SM-GW
			Header:    diameter.Header{SessionID: scHost + ";1;3", OriginHost: scHost, OriginRealm: realm, DestinationHost: "smsf." + realm, DestinationRealm: realm},
			UserName:  imsi,
			SCAddress: scAddress,
			SMRPUI:    mustHex(t, smsDeliver),
		},
	} {
		req := wire(t, tfr.Message())
		if h := req.Header; h.ApplicationID != ApplicationIDSGd || h.CommandCode != CommandMTForwardShortMessage ||
			h.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
			t.Errorf("TFR header = %+v", h)
		}
		req.Header.EndToEndID = 0x5eed
		tfr.EndToEndID = 0x5eed
		got, err := ParseMTForwardShortMessageRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tfr) {
			t.Errorf("TFR\n got %+v\nwant %+v", got, tfr)
		}

		for _, tfa := range []MTForwardShortMessageAnswer{
			{Result: diameter.Base(diameter.DiameterSuccess), SMRPUI: mustHex(t, smsDeliverReport)},
			{Result: diameter.Base(diameter.DiameterSuccess)},
			{Result: diameter.Experimental(DiameterErrorAbsentUser)},
			{Result: diameter.Experimental(DiameterErrorSMDeliveryFailure),
				SMDeliveryFailureCause: &SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: MemoryCapacityExceeded}},
		} {
			ans := wire(t, tfa.Message(req, mme))
			if h := ans.Header; h.ApplicationID != ApplicationIDSGd || h.CommandCode != CommandMTForwardShortMessage ||
				h.CommandFlags != diam.ProxiableFlag {
				t.Errorf("TFA header = %+v", h)
			}
			gotA, err := ParseMTForwardShortMessageAnswer(ans)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotA, tfa) {
				t.Errorf("TFA\n got %+v\nwant %+v", gotA, tfa)
			}
		}
	}
}

// rawAVP is one AVP of a serialized message: the fields of the AVP
// header of RFC 6733 4.1 and the data.
type rawAVP struct {
	code   uint32
	flags  byte
	vendor uint32
	data   []byte
}

// rawAVPs splits b, the AVPs of a message or the data of a Grouped AVP,
// without the help of a dictionary.
func rawAVPs(t *testing.T, b []byte) []rawAVP {
	t.Helper()
	var avps []rawAVP
	for len(b) > 0 {
		if len(b) < 8 {
			t.Fatalf("AVP header cut short: % X", b)
		}
		a := rawAVP{code: binary.BigEndian.Uint32(b), flags: b[4]}
		length := int(binary.BigEndian.Uint32(b[4:]) & 0xffffff)
		header := 8
		if a.flags&0x80 != 0 {
			a.vendor = binary.BigEndian.Uint32(b[8:])
			header = 12
		}
		a.data = b[header:length]
		avps = append(avps, a)
		b = b[min((length+3)&^3, len(b)):]
	}
	return avps
}

// find returns the one AVP with code and vendor.
func find(t *testing.T, avps []rawAVP, code, vendor uint32) rawAVP {
	t.Helper()
	var found []rawAVP
	for _, a := range avps {
		if a.code == code && a.vendor == vendor {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d AVPs with code %d and vendor %d, want 1", len(found), code, vendor)
	}
	return found[0]
}

// TestMOForwardShortMessageRequestBytes checks the encoding against the
// numbers of the specifications rather than against this package's own
// decoder: the header of TS 29.338 6.3.2.3, the codes and flags of Table
// 6.3.3.1/1, the User-Identifier of TS 29.336 6.4.2, and the TBCD digits
// of TS 29.338 6.3.3.2 and TS 29.329 6.3.2.
func TestMOForwardShortMessageRequestBytes(t *testing.T) {
	b, err := MOForwardShortMessageRequest{
		Header:         diameter.Header{SessionID: mmeHost + ";1;1", OriginHost: mmeHost, OriginRealm: realm, DestinationRealm: realm},
		SCAddress:      scAddress,
		UserIdentifier: UserIdentifier{UserName: imsi, MSISDN: msisdn},
		SMRPUI:         mustHex(t, smsSubmit),
	}.Message().Serialize()
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6733 3: version, length, flags, command code, application.
	if b[0] != 1 || int(binary.BigEndian.Uint32(b)&0xffffff) != len(b) {
		t.Fatalf("version %d, length % X in a message of %d octets", b[0], b[1:4], len(b))
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 8388645 || app != 16777313 {
		t.Fatalf("header: flags %#x, command %d, application %d; want R and P set, 8388645, 16777313", flags, code, app)
	}
	avps := rawAVPs(t, b[20:])

	var order []uint32
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Auth-Session-State, Origin-Host, Origin-Realm,
	// Destination-Realm, SC-Address, User-Identifier, SM-RP-UI: the
	// order of the command format, without Destination-Host and without
	// Vendor-Specific-Application-Id (260).
	if want := []uint32{263, 277, 264, 296, 283, 3300, 3102, 3301}; !reflect.DeepEqual(order, want) {
		t.Errorf("AVP codes = %v, want %v", order, want)
	}
	// Auth-Session-State is NO_STATE_MAINTAINED (TS 29.338 4.5).
	if a := find(t, avps, 277, 0); !bytes.Equal(a.data, []byte{0, 0, 0, 1}) {
		t.Errorf("Auth-Session-State = % X", a.data)
	}
	// 12025550100 as a TBCD-string: digits swapped in pairs, and 1111
	// in the last high half.
	if a := find(t, avps, 3300, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 01 F0")) {
		t.Errorf("SC-Address: flags %#x, data % X", a.flags, a.data)
	}
	if a := find(t, avps, 3301, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, smsSubmit)) {
		t.Errorf("SM-RP-UI: flags %#x, data % X", a.flags, a.data)
	}
	user := find(t, avps, 3102, 10415)
	if user.flags != 0xc0 {
		t.Errorf("User-Identifier: flags %#x", user.flags)
	}
	inner := rawAVPs(t, user.data)
	// User-Name (1) is an IETF AVP: M without V.
	if a := find(t, inner, 1, 0); a.flags != 0x40 || string(a.data) != imsi {
		t.Errorf("User-Name: flags %#x, data %q", a.flags, a.data)
	}
	if a := find(t, inner, 701, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 11 F2")) {
		t.Errorf("MSISDN: flags %#x, data % X", a.flags, a.data)
	}
}

// TestMTForwardShortMessageBytes checks the TFR and a failed TFA the
// same way: the flags of MME-Number-for-MT-SMS (TS 29.272 Table
// 7.3.1/1), and the Experimental-Result and SM-Delivery-Failure-Cause of
// TS 29.338 7.3.1 and 6.3.3.5.
func TestMTForwardShortMessageBytes(t *testing.T) {
	tfr := MTForwardShortMessageRequest{
		Header:            diameter.Header{SessionID: scHost + ";1;2", OriginHost: scHost, OriginRealm: realm, DestinationHost: mmeHost, DestinationRealm: realm},
		UserName:          imsi,
		SCAddress:         scAddress,
		SMRPUI:            mustHex(t, smsDeliver),
		MMENumberForMTSMS: mmeNumber,
	}.Message()
	b, err := tfr.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 8388646 || app != 16777313 {
		t.Fatalf("TFR header: flags %#x, command %d, application %d", flags, code, app)
	}
	avps := rawAVPs(t, b[20:])
	var order []uint32
	for _, a := range avps {
		order = append(order, a.code)
	}
	if want := []uint32{263, 277, 264, 296, 293, 283, 1, 3300, 3301, 1645}; !reflect.DeepEqual(order, want) {
		t.Errorf("TFR AVP codes = %v, want %v", order, want)
	}
	// V without M.
	if a := find(t, avps, 1645, 10415); a.flags != 0x80 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 91 F0")) {
		t.Errorf("MME-Number-for-MT-SMS: flags %#x, data % X", a.flags, a.data)
	}

	b, err = MTForwardShortMessageAnswer{
		Result:                 diameter.Experimental(DiameterErrorSMDeliveryFailure),
		SMDeliveryFailureCause: &SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: MemoryCapacityExceeded, SMDiagnosticInfo: []byte{0xD3}},
	}.Message(wire(t, tfr), mme).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff; flags != 0x40 || code != 8388646 {
		t.Fatalf("TFA header: flags %#x, command %d", flags, code)
	}
	avps = rawAVPs(t, b[20:])
	order = nil
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Experimental-Result, Auth-Session-State, Origin-Host,
	// Origin-Realm, SM-Delivery-Failure-Cause. No Result-Code (268).
	if want := []uint32{263, 297, 277, 264, 296, 3303}; !reflect.DeepEqual(order, want) {
		t.Errorf("TFA AVP codes = %v, want %v", order, want)
	}
	result := rawAVPs(t, find(t, avps, 297, 0).data)
	if a := find(t, result, 266, 0); binary.BigEndian.Uint32(a.data) != 10415 {
		t.Errorf("Vendor-Id = % X", a.data)
	}
	if a := find(t, result, 298, 0); binary.BigEndian.Uint32(a.data) != 5555 {
		t.Errorf("Experimental-Result-Code = % X", a.data)
	}
	cause := rawAVPs(t, find(t, avps, 3303, 10415).data)
	if a := find(t, cause, 3304, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, []byte{0, 0, 0, 0}) {
		t.Errorf("SM-Enumerated-Delivery-Failure-Cause: flags %#x, data % X", a.flags, a.data)
	}
	if a := find(t, cause, 3305, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, []byte{0xD3}) {
		t.Errorf("SM-Diagnostic-Info: flags %#x, data % X", a.flags, a.data)
	}
}

// request returns an SGd request with only the routing AVPs, for the
// tests that leave AVPs out.
func request(command uint32) *diam.Message {
	m := diameter.NewRequest(ApplicationIDSGd, command)
	begin(m, diameter.Header{SessionID: "s", OriginHost: mmeHost, OriginRealm: realm, DestinationHost: scHost, DestinationRealm: realm})
	return m
}

func TestParseForwardShortMessageErrors(t *testing.T) {
	scAVP := diameter.VendorAVP(avpSCAddress, mv, encodeTBCD(scAddress))
	tpdu := diameter.VendorAVP(avpSMRPUI, mv, datatype.OctetString(mustHex(t, smsSubmit)))
	user := UserIdentifier{UserName: imsi}.avp()

	ofr := map[string][]*diam.AVP{
		"no SC-Address":                  {user, tpdu},
		"no User-Identifier":             {scAVP, tpdu},
		"no SM-RP-UI":                    {scAVP, user},
		"User-Identifier without a user": {scAVP, UserIdentifier{}.avp(), tpdu},
		"SC-Address that is not TBCD":    {diameter.VendorAVP(avpSCAddress, mv, datatype.OctetString([]byte{0x21, 0xA0})), user, tpdu},
		"filler before the last digit":   {diameter.VendorAVP(avpSCAddress, mv, datatype.OctetString([]byte{0x2F, 0x10})), user, tpdu},
		"MSISDN that is not TBCD": {scAVP, diameter.VendorAVP(avpUserIdentifier, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
			diameter.VendorAVP(avpMSISDN, mv, datatype.OctetString([]byte{0xFF})),
		}}), tpdu},
	}
	for name, avps := range ofr {
		m := request(CommandMOForwardShortMessage)
		for _, a := range avps {
			m.AddAVP(a)
		}
		if got, err := ParseMOForwardShortMessageRequest(wire(t, m)); err == nil {
			t.Errorf("OFR, %s: got %+v, want an error", name, got)
		}
	}

	userName := diam.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(imsi))
	tfr := map[string][]*diam.AVP{
		"no User-Name":  {scAVP, tpdu},
		"no SC-Address": {userName, tpdu},
		"no SM-RP-UI":   {userName, scAVP},
		"MME-Number-for-MT-SMS that is not TBCD": {userName, scAVP, tpdu,
			diameter.VendorAVP(avpMMENumberForMTSMS, v, datatype.OctetString([]byte{0x0A}))},
	}
	for name, avps := range tfr {
		m := request(CommandMTForwardShortMessage)
		for _, a := range avps {
			m.AddAVP(a)
		}
		if got, err := ParseMTForwardShortMessageRequest(wire(t, m)); err == nil {
			t.Errorf("TFR, %s: got %+v, want an error", name, got)
		}
	}

	// An answer without Result-Code and without Experimental-Result.
	req := wire(t, request(CommandMOForwardShortMessage))
	bare := diam.NewMessage(req.Header.CommandCode, diam.ProxiableFlag, req.Header.ApplicationID, 0, 0, dict.Default)
	bare.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("s"))
	if got, err := ParseMOForwardShortMessageAnswer(wire(t, bare)); err == nil {
		t.Errorf("OFA without a result: got %+v, want an error", got)
	}
	if got, err := ParseMTForwardShortMessageAnswer(wire(t, bare)); err == nil {
		t.Errorf("TFA without a result: got %+v, want an error", got)
	}
}

func TestTBCD(t *testing.T) {
	for digits, want := range map[string]string{
		"12025550100":     "21 20 55 05 01 F0", // odd count: filler in the last high half
		"1202555019":      "21 20 55 05 91",
		"1":               "F1",
		"123456789012345": "21 43 65 87 09 21 43 F5",
	} {
		got := []byte(encodeTBCD(digits))
		if !bytes.Equal(got, mustHex(t, want)) {
			t.Errorf("encodeTBCD(%q) = % X, want %s", digits, got, want)
		}
		if back, err := decodeTBCD(got); err != nil || back != digits {
			t.Errorf("decodeTBCD(% X) = %q, %v, want %q", got, back, err, digits)
		}
	}
	for _, bad := range []string{"1A", "F1 21", "FF"} {
		if got, err := decodeTBCD(mustHex(t, bad)); err == nil {
			t.Errorf("decodeTBCD(%s) = %q, want an error", bad, got)
		}
	}
}

func TestValidE164(t *testing.T) {
	for s, want := range map[string]bool{
		"12025550100":      true,
		"1":                true,
		"123456789012345":  true,
		"":                 false,
		"1234567890123456": false,
		"+12025550100":     false,
		"1202555010a":      false,
	} {
		if got := ValidE164(s); got != want {
			t.Errorf("ValidE164(%q) = %t, want %t", s, got, want)
		}
	}
}

func TestNames(t *testing.T) {
	for got, want := range map[string]string{
		diameter.Base(diameter.DiameterSuccess).String():        "Result-Code 2001",
		diameter.Experimental(DiameterErrorAbsentUser).String(): "Experimental-Result-Code 5550",
		MemoryCapacityExceeded.String():                         "MEMORY_CAPACITY_EXCEEDED",
		UserNotSCUser.String():                                  "USER_NOT_SC-USER",
		SMEnumeratedDeliveryFailureCause(9).String():            "SM-Enumerated-Delivery-Failure-Cause 9",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if !diameter.Base(diameter.DiameterSuccess).Success() || diameter.Experimental(DiameterErrorUserUnknown).Success() {
		t.Error("Success() is wrong")
	}
}
