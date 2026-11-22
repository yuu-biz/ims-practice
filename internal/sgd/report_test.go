package sgd

import (
	"bytes"
	"encoding/binary"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

func TestReportSMDeliveryStatusRoundTrip(t *testing.T) {
	noResponseViaIPSMGW := uint32(12) // TS 23.040 Table 1a
	for _, rdr := range []ReportSMDeliveryStatusRequest{
		{ // the MME did not reach the UE
			Header:         diameter.Header{SessionID: scHost + ";4;1", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm},
			UserIdentifier: UserIdentifier{MSISDN: msisdn},
			SCAddress:      scAddress,
			SMDeliveryOutcome: SMDeliveryOutcome{
				MMESMDeliveryOutcome: &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseAbsentUser},
			},
		},
		{ // the IP-SM-GW and the SMSF both failed
			Header:         diameter.Header{SessionID: scHost + ";4;2", OriginHost: scHost, OriginRealm: realm, DestinationHost: hssHost, DestinationRealm: realm},
			UserIdentifier: UserIdentifier{MSISDN: msisdn},
			SCAddress:      scAddress,
			SMDeliveryOutcome: SMDeliveryOutcome{
				IPSMGWSMDeliveryOutcome:   &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseAbsentUser, AbsentUserDiagnosticSM: &noResponseViaIPSMGW},
				SMSF3GPPSMDeliveryOutcome: &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseUEMemoryCapacityExceeded},
			},
		},
	} {
		req := wire(t, rdr.Message())
		if h := req.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandReportSMDeliveryStatus ||
			h.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
			t.Errorf("RDR header = %+v", h)
		}
		got, err := ParseReportSMDeliveryStatusRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, rdr) {
			t.Errorf("RDR\n got %+v\nwant %+v", got, rdr)
		}

		for _, rda := range []ReportSMDeliveryStatusAnswer{
			{Result: diameter.Base(diameter.DiameterSuccess)},
			{Result: diameter.Base(diameter.DiameterSuccess), UserIdentifier: &UserIdentifier{MSISDN: "12025550113"}},
			{Result: diameter.Experimental(DiameterErrorMWDListFull)},
			{Result: diameter.Experimental(DiameterErrorUserUnknown)},
		} {
			ans := wire(t, rda.Message(req, hss))
			if h := ans.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandReportSMDeliveryStatus ||
				h.CommandFlags != diam.ProxiableFlag || h.HopByHopID != req.Header.HopByHopID {
				t.Errorf("RDA header = %+v", h)
			}
			gotA, err := ParseReportSMDeliveryStatusAnswer(ans)
			if err != nil || !reflect.DeepEqual(gotA, rda) {
				t.Errorf("RDA: got %+v, %v, want %+v", gotA, err, rda)
			}
		}
	}
}

// TestReportSMDeliveryStatusRequestBytes checks the encoding against the
// numbers of the specifications: the header and the AVP order of TS
// 29.338 5.3.2.7, and the codes and flags of the outcome in Table
// 5.3.3.1/1.
func TestReportSMDeliveryStatusRequestBytes(t *testing.T) {
	b, err := ReportSMDeliveryStatusRequest{
		Header:         diameter.Header{SessionID: scHost + ";4;1", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm},
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
		SCAddress:      scAddress,
		SMDeliveryOutcome: SMDeliveryOutcome{
			MMESMDeliveryOutcome:      &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseAbsentUser},
			SMSF3GPPSMDeliveryOutcome: &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseUEMemoryCapacityExceeded},
		},
	}.Message().Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 8388649 || app != 16777312 {
		t.Fatalf("RDR header: flags %#x, command %d, application %d; want R and P set, 8388649, 16777312", flags, code, app)
	}
	avps := rawAVPs(t, b[20:])
	var order []uint32
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Auth-Session-State, Origin-Host, Origin-Realm,
	// Destination-Realm, User-Identifier, SC-Address,
	// SM-Delivery-Outcome.
	if want := []uint32{263, 277, 264, 296, 283, 3102, 3300, 3316}; !reflect.DeepEqual(order, want) {
		t.Errorf("RDR AVP codes = %v, want %v", order, want)
	}
	outcome := find(t, avps, 3316, 10415)
	if outcome.flags != 0xc0 {
		t.Errorf("SM-Delivery-Outcome: flags %#x", outcome.flags)
	}
	inner := rawAVPs(t, outcome.data)
	mme := find(t, inner, 3317, 10415)
	if mme.flags != 0xc0 {
		t.Errorf("MME-SM-Delivery-Outcome: flags %#x", mme.flags)
	}
	if a := find(t, rawAVPs(t, mme.data), 3321, 10415); a.flags != 0xc0 || binary.BigEndian.Uint32(a.data) != 1 {
		t.Errorf("SM-Delivery-Cause: flags %#x, data % X, want ABSENT_USER", a.flags, a.data)
	}
	smsf := find(t, inner, 3336, 10415)
	if smsf.flags != 0x80 {
		t.Errorf("SMSF-3GPP-SM-Delivery-Outcome: flags %#x, want V without M", smsf.flags)
	}
	if a := find(t, rawAVPs(t, smsf.data), 3321, 10415); !bytes.Equal(a.data, []byte{0, 0, 0, 0}) {
		t.Errorf("SM-Delivery-Cause: data % X, want UE_MEMORY_CAPACITY_EXCEEDED", a.data)
	}
}

func TestParseReportSMDeliveryStatusErrors(t *testing.T) {
	rdr := func(avps ...*diam.AVP) *diam.Message {
		m := diameter.NewRequest(ApplicationIDS6c, CommandReportSMDeliveryStatus)
		begin(m, diameter.Header{SessionID: "s", OriginHost: scHost, OriginRealm: realm, DestinationRealm: realm})
		for _, a := range avps {
			m.AddAVP(a)
		}
		return m
	}
	user := UserIdentifier{MSISDN: msisdn}.avp()
	sc := diameter.VendorAVP(avpSCAddress, mv, encodeTBCD(scAddress))
	absent := SMDeliveryOutcome{MMESMDeliveryOutcome: &NodeSMDeliveryOutcome{SMDeliveryCause: SMDeliveryCauseAbsentUser}}.avp()
	for name, m := range map[string]*diam.Message{
		"without User-Identifier":     rdr(sc, absent),
		"without SC-Address":          rdr(user, absent),
		"without SM-Delivery-Outcome": rdr(user, sc),
		"with an empty outcome":       rdr(user, sc, SMDeliveryOutcome{}.avp()),
		"with an outcome without its cause": rdr(user, sc, diameter.VendorAVP(avpSMDeliveryOutcome, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
			diameter.VendorAVP(avpMMESMDeliveryOutcome, mv, &diam.GroupedAVP{AVP: []*diam.AVP{
				diameter.VendorAVP(avpAbsentUserDiagnosticSM, mv, datatype.Unsigned32(1)),
			}}),
		}})),
	} {
		if got, err := ParseReportSMDeliveryStatusRequest(wire(t, m)); err == nil {
			t.Errorf("RDR %s: got %+v, want an error", name, got)
		}
	}
}
