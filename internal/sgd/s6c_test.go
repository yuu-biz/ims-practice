package sgd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

const (
	hssHost      = "hss." + realm
	smsfHost     = "smsf." + realm
	smsfNumber   = "12025550191"
	ipsmgwHost   = "ipsmgw.ims.mnc001.mcc001.3gppnetwork.org"
	ipsmgwRealm  = "ims.mnc001.mcc001.3gppnetwork.org"
	ipsmgwNumber = "12025550192"
)

var hss = diameter.Identity{OriginHost: hssHost, OriginRealm: realm}

func TestSendRoutingInfoForSMRoundTrip(t *testing.T) {
	for _, srr := range []SendRoutingInfoForSMRequest{
		{
			Header:            diameter.Header{SessionID: scHost + ";2;1", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm},
			MSISDN:            msisdn,
			SupportedFeatures: SMSFSupport,
			SCAddress:         scAddress,
		},
		{ // without Supported-Features, to a known HSS
			Header:    diameter.Header{SessionID: scHost + ";2;2", OriginHost: scHost, OriginRealm: realm, DestinationHost: hssHost, DestinationRealm: realm},
			MSISDN:    msisdn,
			SCAddress: scAddress,
		},
		{ // a short message that gets one delivery attempt
			Header:    diameter.Header{SessionID: scHost + ";2;3", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm},
			MSISDN:    msisdn,
			SCAddress: scAddress,
			SRRFlags:  SRRSingleAttemptDelivery,
		},
	} {
		req := wire(t, srr.Message())
		if h := req.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandSendRoutingInfoForSM ||
			h.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
			t.Errorf("SRR header = %+v", h)
		}
		got, err := ParseSendRoutingInfoForSMRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, srr) {
			t.Errorf("SRR\n got %+v\nwant %+v", got, srr)
		}

		for _, sra := range []SendRoutingInfoForSMAnswer{
			{ // an MME and an SMSF serve the user
				Result:            diameter.Base(diameter.DiameterSuccess),
				UserName:          imsi,
				SupportedFeatures: SMSFSupport,
				ServingNode:       &ServingNode{MMEName: mmeHost, MMERealm: realm, MMENumberForMTSMS: mmeNumber},
				SMSF3GPPAddress:   &SMSF3GPPAddress{SMSF3GPPNumber: smsfNumber, SMSF3GPPName: smsfHost, SMSF3GPPRealm: realm},
			},
			{ // only an MME
				Result:      diameter.Base(diameter.DiameterSuccess),
				UserName:    imsi,
				ServingNode: &ServingNode{MMEName: mmeHost, MMERealm: realm, MMENumberForMTSMS: mmeNumber},
			},
			{ // only an SMSF
				Result:            diameter.Base(diameter.DiameterSuccess),
				UserName:          imsi,
				SupportedFeatures: SMSFSupport,
				SMSF3GPPAddress:   &SMSF3GPPAddress{SMSF3GPPNumber: smsfNumber, SMSF3GPPName: smsfHost, SMSF3GPPRealm: realm},
			},
			{ // the IP-SM-GW answers with itself and an MT Correlation ID
				Result:      diameter.Base(diameter.DiameterSuccess),
				UserName:    "001019876543210",
				ServingNode: &ServingNode{IPSMGWNumber: ipsmgwNumber, IPSMGWName: ipsmgwHost, IPSMGWRealm: ipsmgwRealm},
			},
			{ // the HSS holds the SC address, and MCEF, for the user
				Result:      diameter.Base(diameter.DiameterSuccess),
				UserName:    imsi,
				ServingNode: &ServingNode{IPSMGWNumber: ipsmgwNumber, IPSMGWName: ipsmgwHost, IPSMGWRealm: ipsmgwRealm},
				MWDStatus:   new(MWDMCEFSet),
			},
			{ // the HSS holds the SC address and no flag: an MWD-Status of 0
				Result:      diameter.Base(diameter.DiameterSuccess),
				UserName:    imsi,
				ServingNode: &ServingNode{MMEName: mmeHost, MMERealm: realm, MMENumberForMTSMS: mmeNumber},
				MWDStatus:   new(MWDStatus(0)),
			},
			{Result: diameter.Experimental(DiameterErrorUserUnknown)},
			{Result: diameter.Experimental(DiameterErrorAbsentUser), SupportedFeatures: SMSFSupport},
		} {
			ans := wire(t, sra.Message(req, hss))
			if h := ans.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandSendRoutingInfoForSM ||
				h.CommandFlags != diam.ProxiableFlag || h.HopByHopID != req.Header.HopByHopID || h.EndToEndID != req.Header.EndToEndID {
				t.Errorf("SRA header = %+v", h)
			}
			gotA, err := ParseSendRoutingInfoForSMAnswer(ans)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotA, sra) {
				t.Errorf("SRA\n got %+v\nwant %+v", gotA, sra)
			}
		}
	}
}

// TestSendRoutingInfoForSMBytes checks the encoding against the numbers
// of the specifications rather than against this package's own decoder:
// the header of TS 29.338 5.3.2.3 and 5.3.2.4, the Supported-Features of
// TS 29.229 6.3.29, the Serving-Node of TS 29.338 5.3.3.6 with the flags
// of Table 5.3.3.1/2, and the SMSF-3GPP-Address of 5.3.3.35 and the
// MWD-Status of 5.3.3.8 with the flags of Table 5.3.3.1/1.
func TestSendRoutingInfoForSMBytes(t *testing.T) {
	srr := SendRoutingInfoForSMRequest{
		Header:            diameter.Header{SessionID: scHost + ";2;1", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm},
		MSISDN:            msisdn,
		SupportedFeatures: SMSFSupport,
		SCAddress:         scAddress,
	}.Message()
	b, err := srr.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 8388647 || app != 16777312 {
		t.Fatalf("SRR header: flags %#x, command %d, application %d; want R and P set, 8388647, 16777312", flags, code, app)
	}
	avps := rawAVPs(t, b[20:])
	var order []uint32
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Auth-Session-State, Origin-Host, Origin-Realm,
	// Destination-Realm, MSISDN, Supported-Features, SC-Address.
	if want := []uint32{263, 277, 264, 296, 283, 701, 628, 3300}; !reflect.DeepEqual(order, want) {
		t.Errorf("SRR AVP codes = %v, want %v", order, want)
	}
	if a := find(t, avps, 701, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 11 F2")) {
		t.Errorf("MSISDN: flags %#x, data % X", a.flags, a.data)
	}
	features := find(t, avps, 628, 10415)
	if features.flags != 0x80 {
		t.Errorf("Supported-Features: flags %#x, want V without M", features.flags)
	}
	inner := rawAVPs(t, features.data)
	if a := find(t, inner, 266, 0); binary.BigEndian.Uint32(a.data) != 10415 {
		t.Errorf("Vendor-Id = % X", a.data)
	}
	if a := find(t, inner, 629, 10415); a.flags != 0x80 || binary.BigEndian.Uint32(a.data) != 1 {
		t.Errorf("Feature-List-ID: flags %#x, data % X", a.flags, a.data)
	}
	// Bit 0 is SMSF-Support.
	if a := find(t, inner, 630, 10415); a.flags != 0x80 || binary.BigEndian.Uint32(a.data) != 1 {
		t.Errorf("Feature-List: flags %#x, data % X", a.flags, a.data)
	}

	b, err = SendRoutingInfoForSMAnswer{
		Result:            diameter.Base(diameter.DiameterSuccess),
		UserName:          imsi,
		SupportedFeatures: SMSFSupport,
		ServingNode:       &ServingNode{MMEName: mmeHost, MMERealm: realm, MMENumberForMTSMS: mmeNumber},
		SMSF3GPPAddress:   &SMSF3GPPAddress{SMSF3GPPNumber: smsfNumber, SMSF3GPPName: smsfHost, SMSF3GPPRealm: realm},
		MWDStatus:         new(MWDSCAddressNotIncluded | MWDMCEFSet),
	}.Message(wire(t, srr), hss).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0x40 || code != 8388647 || app != 16777312 {
		t.Fatalf("SRA header: flags %#x, command %d, application %d", flags, code, app)
	}
	avps = rawAVPs(t, b[20:])
	order = nil
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Result-Code, Auth-Session-State, Origin-Host,
	// Origin-Realm, User-Name, Supported-Features, Serving-Node,
	// SMSF-3GPP-Address, MWD-Status.
	if want := []uint32{263, 268, 277, 264, 296, 1, 628, 2401, 3344, 3312}; !reflect.DeepEqual(order, want) {
		t.Errorf("SRA AVP codes = %v, want %v", order, want)
	}
	// Bits 0 and 2 of Table 5.3.3.8/1: SC-Address Not included and
	// MCEF-Set.
	if a := find(t, avps, 3312, 10415); a.flags != 0xc0 || binary.BigEndian.Uint32(a.data) != 0b101 {
		t.Errorf("MWD-Status: flags %#x, data % X", a.flags, a.data)
	}
	if a := find(t, avps, 268, 0); binary.BigEndian.Uint32(a.data) != 2001 {
		t.Errorf("Result-Code = % X", a.data)
	}
	node := find(t, avps, 2401, 10415)
	if node.flags != 0xc0 {
		t.Errorf("Serving-Node: flags %#x", node.flags)
	}
	inner = rawAVPs(t, node.data)
	if a := find(t, inner, 2402, 10415); a.flags != 0xc0 || string(a.data) != mmeHost {
		t.Errorf("MME-Name: flags %#x, data %q", a.flags, a.data)
	}
	if a := find(t, inner, 2408, 10415); a.flags != 0xc0 || string(a.data) != realm {
		t.Errorf("MME-Realm: flags %#x, data %q", a.flags, a.data)
	}
	if a := find(t, inner, 1645, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 91 F0")) {
		t.Errorf("MME-Number-for-MT-SMS: flags %#x, data % X", a.flags, a.data)
	}
	smsf := find(t, avps, 3344, 10415)
	if smsf.flags != 0x80 {
		t.Errorf("SMSF-3GPP-Address: flags %#x, want V without M", smsf.flags)
	}
	inner = rawAVPs(t, smsf.data)
	if a := find(t, inner, 3338, 10415); a.flags != 0x80 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 91 F1")) {
		t.Errorf("SMSF-3GPP-Number: flags %#x, data % X", a.flags, a.data)
	}
	if a := find(t, inner, 3340, 10415); a.flags != 0x80 || string(a.data) != smsfHost {
		t.Errorf("SMSF-3GPP-Name: flags %#x, data %q", a.flags, a.data)
	}
	if a := find(t, inner, 3342, 10415); a.flags != 0x80 || string(a.data) != realm {
		t.Errorf("SMSF-3GPP-Realm: flags %#x, data %q", a.flags, a.data)
	}
}

func TestSendRoutingInfoForSMServingNodeIPSMGW(t *testing.T) {
	srr := SendRoutingInfoForSMRequest{
		Header: diameter.Header{SessionID: "s", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm},
		MSISDN: msisdn, SCAddress: scAddress,
	}.Message()
	b, err := SendRoutingInfoForSMAnswer{
		Result:      diameter.Base(diameter.DiameterSuccess),
		UserName:    imsi,
		ServingNode: &ServingNode{IPSMGWNumber: ipsmgwNumber, IPSMGWName: ipsmgwHost, IPSMGWRealm: ipsmgwRealm},
	}.Message(wire(t, srr), diameter.Identity{OriginHost: ipsmgwHost, OriginRealm: ipsmgwRealm}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	inner := rawAVPs(t, find(t, rawAVPs(t, b[20:]), 2401, 10415).data)
	if a := find(t, inner, 3100, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 91 F2")) {
		t.Errorf("IP-SM-GW-Number: flags %#x, data % X", a.flags, a.data)
	}
	if a := find(t, inner, 3101, 10415); a.flags != 0xc0 || string(a.data) != ipsmgwHost {
		t.Errorf("IP-SM-GW-Name: flags %#x, data %q", a.flags, a.data)
	}
	if a := find(t, inner, 3112, 10415); a.flags != 0xc0 || string(a.data) != ipsmgwRealm {
		t.Errorf("IP-SM-GW-Realm: flags %#x, data %q", a.flags, a.data)
	}
}

// TestSupportedFeaturesOfOthers checks that only the feature list that
// S6c defines counts: Vendor-Id 10415 and Feature-List-ID 1.
func TestSupportedFeaturesOfOthers(t *testing.T) {
	features := func(vendor, id, list uint32) *diam.AVP {
		return diameter.VendorAVP(avpSupportedFeatures, v, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(vendor)),
			diameter.VendorAVP(avpFeatureListID, v, datatype.Unsigned32(id)),
			diameter.VendorAVP(avpFeatureList, v, datatype.Unsigned32(list)),
		}})
	}
	m := diameter.NewRequest(ApplicationIDS6c, CommandSendRoutingInfoForSM)
	begin(m, diameter.Header{SessionID: "s", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm})
	m.AddAVP(diameter.VendorAVP(avpMSISDN, mv, encodeTBCD(msisdn)))
	m.AddAVP(features(10415, 2, 0xff))
	m.AddAVP(features(99999, 1, 0xff))
	got, err := ParseSendRoutingInfoForSMRequest(wire(t, m))
	if err != nil || got.SupportedFeatures != 0 {
		t.Errorf("lists of others: got %#x, %v, want none", got.SupportedFeatures, err)
	}
	m.AddAVP(features(10415, 1, 1))
	got, err = ParseSendRoutingInfoForSMRequest(wire(t, m))
	if err != nil || got.SupportedFeatures != SMSFSupport {
		t.Errorf("with the list of S6c: got %#x, %v, want SMSFSupport", got.SupportedFeatures, err)
	}
}

func TestParseSendRoutingInfoForSMErrors(t *testing.T) {
	srr := func(avps ...*diam.AVP) *diam.Message {
		m := diameter.NewRequest(ApplicationIDS6c, CommandSendRoutingInfoForSM)
		begin(m, diameter.Header{SessionID: "s", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm})
		for _, a := range avps {
			m.AddAVP(a)
		}
		return m
	}
	notTBCD := datatype.OctetString([]byte{0x0A})
	for name, m := range map[string]*diam.Message{
		"MSISDN that is not TBCD":     srr(diameter.VendorAVP(avpMSISDN, mv, notTBCD)),
		"SC-Address that is not TBCD": srr(diameter.VendorAVP(avpMSISDN, mv, encodeTBCD(msisdn)), diameter.VendorAVP(avpSCAddress, mv, notTBCD)),
	} {
		if got, err := ParseSendRoutingInfoForSMRequest(wire(t, m)); err == nil {
			t.Errorf("SRR, %s: got %+v, want an error", name, got)
		}
	}
	// The command format requires neither MSISDN nor SC-Address.
	if got, err := ParseSendRoutingInfoForSMRequest(wire(t, srr())); err != nil || got.MSISDN != "" || got.SCAddress != "" {
		t.Errorf("SRR with routing AVPs only: got %+v, %v", got, err)
	}

	req := wire(t, srr())
	sra := func(avps ...*diam.AVP) *diam.Message {
		m := newAnswer(req, hss, diameter.Base(diameter.DiameterSuccess))
		for _, a := range avps {
			m.AddAVP(a)
		}
		return m
	}
	for name, m := range map[string]*diam.Message{
		"MME-Number-for-MT-SMS that is not TBCD": sra(diameter.VendorAVP(avpServingNode, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
			diameter.VendorAVP(avpMMENumberForMTSMS, mv, notTBCD),
		}})),
		"IP-SM-GW-Number that is not TBCD": sra(diameter.VendorAVP(avpServingNode, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
			diameter.VendorAVP(avpIPSMGWNumber, mv, notTBCD),
		}})),
		"SMSF-3GPP-Number that is not TBCD": sra(diameter.VendorAVP(avpSMSF3GPPAddress, v, &diam.GroupedAVP{AVP: []*diam.AVP{
			diameter.VendorAVP(avpSMSF3GPPNumber, v, notTBCD),
		}})),
	} {
		if got, err := ParseSendRoutingInfoForSMAnswer(wire(t, m)); err == nil {
			t.Errorf("SRA, %s: got %+v, want an error", name, got)
		}
	}
	bare := diam.NewMessage(req.Header.CommandCode, diam.ProxiableFlag, req.Header.ApplicationID, 0, 0, req.Dictionary())
	bare.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("s"))
	if got, err := ParseSendRoutingInfoForSMAnswer(wire(t, bare)); err == nil {
		t.Errorf("SRA without a result: got %+v, want an error", got)
	}
}

// ExampleSendRoutingInfoForSMRequest shows a mobile terminated short
// message: the SC asks the HSS which node serves the recipient on S6c,
// and forwards the TPDU to that node on SGd.
func ExampleSendRoutingInfoForSMRequest() {
	if err := LoadDictionary(); err != nil {
		panic(err)
	}
	const realm = "epc.mnc001.mcc001.3gppnetwork.org"
	sc := diameter.Identity{OriginHost: "sc." + realm, OriginRealm: realm}
	smsDeliver := []byte{0x04, 0x0B, 0x91, 0x21, 0x20, 0x55, 0x05, 0x11, 0xF2, 0x00, 0x00,
		0x62, 0x11, 0x02, 0x60, 0x83, 0x13, 0x00, 0x05, 0xE8, 0x32, 0x9B, 0xFD, 0x06}

	// The SC asks for the routing information of the recipient.
	srr := SendRoutingInfoForSMRequest{
		Header:            diameter.Header{SessionID: sc.OriginHost + ";2;1", OriginHost: sc.OriginHost, OriginRealm: sc.OriginRealm, DestinationRealm: realm},
		MSISDN:            "12025550123",
		SupportedFeatures: SMSFSupport,
		SCAddress:         "12025550100",
	}.Message()

	// The HSS: an MME is registered for MT SMS for this user.
	request := overTheWire(srr)
	sra := SendRoutingInfoForSMAnswer{
		Result:            diameter.Base(diameter.DiameterSuccess),
		UserName:          "001010000000002",
		SupportedFeatures: SMSFSupport,
		ServingNode:       &ServingNode{MMEName: "mme." + realm, MMERealm: realm, MMENumberForMTSMS: "12025550190"},
	}.Message(request, diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm})

	// The SC: the answer names the node to send the TFR to.
	routing, err := ParseSendRoutingInfoForSMAnswer(overTheWire(sra))
	if err != nil {
		panic(err)
	}
	node := routing.ServingNode
	fmt.Printf("%s: IMSI %s is served by %s\n", routing.Result, routing.UserName, node.MMEName)
	tfr := MTForwardShortMessageRequest{
		Header:            diameter.Header{SessionID: sc.OriginHost + ";1;7", OriginHost: sc.OriginHost, OriginRealm: sc.OriginRealm, DestinationHost: node.MMEName, DestinationRealm: node.MMERealm},
		UserName:          routing.UserName,
		SCAddress:         "12025550100",
		SMRPUI:            smsDeliver,
		MMENumberForMTSMS: node.MMENumberForMTSMS,
	}.Message()

	// The MME: the UE is not attached.
	delivery := overTheWire(tfr)
	tfa := MTForwardShortMessageAnswer{Result: diameter.Experimental(DiameterErrorAbsentUser)}.
		Message(delivery, diameter.Identity{OriginHost: node.MMEName, OriginRealm: node.MMERealm})

	answer, err := ParseMTForwardShortMessageAnswer(overTheWire(tfa))
	if err != nil {
		panic(err)
	}
	fmt.Println(answer.Result, answer.Result.Success())
	// Output:
	// Result-Code 2001: IMSI 001010000000002 is served by mme.epc.mnc001.mcc001.3gppnetwork.org
	// Experimental-Result-Code 5550 false
}
