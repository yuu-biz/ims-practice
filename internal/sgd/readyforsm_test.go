package sgd

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

var ipsmgw = diameter.Identity{OriginHost: ipsmgwHost, OriginRealm: ipsmgwRealm}

func TestReadyForSMRoundTrip(t *testing.T) {
	for _, reason := range []AlertReason{AlertReasonUEPresent, AlertReasonUEMemoryAvailable} {
		r := ReadyForSMRequest{
			Header:         diameter.Header{SessionID: ipsmgwHost + ";4;1", OriginHost: ipsmgwHost, OriginRealm: ipsmgwRealm, DestinationRealm: realm},
			UserIdentifier: UserIdentifier{MSISDN: msisdn},
			IPSMGWNumber:   ipsmgwNumber,
			AlertReason:    reason,
		}
		req := wire(t, r.Message())
		if h := req.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandReadyForSM ||
			h.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
			t.Errorf("request header = %+v", h)
		}
		got, err := ParseReadyForSMRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, r) {
			t.Errorf("request\n got %+v\nwant %+v", got, r)
		}

		for _, a := range []ReadyForSMAnswer{
			{Result: diameter.Base(diameter.DiameterSuccess)},
			{Result: diameter.Experimental(DiameterErrorUserUnknown)},
		} {
			ans := wire(t, a.Message(req, hss))
			if h := ans.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandReadyForSM ||
				h.CommandFlags != diam.ProxiableFlag || h.HopByHopID != req.Header.HopByHopID || h.EndToEndID != req.Header.EndToEndID {
				t.Errorf("answer header = %+v", h)
			}
			gotA, err := ParseReadyForSMAnswer(ans)
			if err != nil || !reflect.DeepEqual(gotA, a) {
				t.Errorf("answer: got %+v, %v, want %+v", gotA, err, a)
			}
		}
	}
}

// TestReadyForSMRequestBytes checks the encoding against the numbers it
// is made of: the experimental command code of RFC 6733 11.2.1 in the
// application of S6c (TS 29.338 4.8), User-Identifier of TS 29.336 6.4.2,
// IP-SM-GW-Number of TS 29.336 6.4.14 and Alert-Reason of TS 29.272
// 7.3.83, each with M and V set and the 3GPP Vendor-Id.
func TestReadyForSMRequestBytes(t *testing.T) {
	b, err := ReadyForSMRequest{
		Header:         diameter.Header{SessionID: ipsmgwHost + ";4;1", OriginHost: ipsmgwHost, OriginRealm: ipsmgwRealm, DestinationRealm: realm},
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
		IPSMGWNumber:   ipsmgwNumber,
		AlertReason:    AlertReasonUEMemoryAvailable,
	}.Message().Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 16777214 || app != 16777312 {
		t.Fatalf("header: flags %#x, command %d, application %d; want R and P set, 16777214, 16777312", flags, code, app)
	}
	avps := rawAVPs(t, b[20:])
	var order []uint32
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Auth-Session-State, Origin-Host, Origin-Realm,
	// Destination-Realm, User-Identifier, IP-SM-GW-Number, Alert-Reason.
	if want := []uint32{263, 277, 264, 296, 283, 3102, 3100, 1434}; !reflect.DeepEqual(order, want) {
		t.Errorf("AVP codes = %v, want %v", order, want)
	}
	user := find(t, avps, 3102, 10415)
	if a := find(t, rawAVPs(t, user.data), 701, 10415); user.flags != 0xc0 || a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 11 F2")) {
		t.Errorf("User-Identifier: flags %#x, MSISDN flags %#x, data % X", user.flags, a.flags, a.data)
	}
	if a := find(t, avps, 3100, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 91 F2")) {
		t.Errorf("IP-SM-GW-Number: flags %#x, data % X", a.flags, a.data)
	}
	if a := find(t, avps, 1434, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "00 00 00 01")) {
		t.Errorf("Alert-Reason: flags %#x, data % X", a.flags, a.data)
	}
}

func TestParseReadyForSMErrors(t *testing.T) {
	request := func(avps ...*diam.AVP) *diam.Message {
		m := diameter.NewRequest(ApplicationIDS6c, CommandReadyForSM)
		begin(m, diameter.Header{SessionID: "s", OriginHost: ipsmgwHost, OriginRealm: ipsmgwRealm, DestinationRealm: realm})
		for _, a := range avps {
			m.AddAVP(a)
		}
		return m
	}
	user := UserIdentifier{MSISDN: msisdn}.avp()
	number := diameter.VendorAVP(avpIPSMGWNumber, mv, encodeTBCD(ipsmgwNumber))
	reason := func(r uint32) *diam.AVP { return diameter.VendorAVP(avpAlertReason, mv, datatype.Enumerated(r)) }
	for name, m := range map[string]*diam.Message{
		"without User-Identifier":         request(number, reason(0)),
		"naming the user by IMSI only":    request(UserIdentifier{UserName: imsi}.avp(), number, reason(0)),
		"without IP-SM-GW-Number":         request(user, reason(0)),
		"without Alert-Reason":            request(user, number),
		"with an Alert-Reason of no name": request(user, number, reason(2)),
	} {
		if got, err := ParseReadyForSMRequest(wire(t, m)); err == nil {
			t.Errorf("request %s: got %+v, want an error", name, got)
		}
	}
	bare := diam.NewMessage(CommandReadyForSM, diam.ProxiableFlag, ApplicationIDS6c, 0, 0, request().Dictionary())
	bare.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("s"))
	if got, err := ParseReadyForSMAnswer(wire(t, bare)); err == nil {
		t.Errorf("answer without a result: got %+v, want an error", got)
	}
}

// TestS6cClientSendsReadyForSM: the IP-SM-GW tells the HSS that a user
// can take short messages again, and the client fills in the Session-Id
// and the origin of its node.
func TestS6cClientSendsReadyForSM(t *testing.T) {
	c := connect(t, hss, ipsmgw, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return ReadyForSMAnswer{Result: diameter.Experimental(DiameterErrorUserUnknown)}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)

	a, err := client.ReadyForSM(t.Context(), ReadyForSMRequest{
		Header:         diameter.Header{DestinationRealm: realm},
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
		IPSMGWNumber:   ipsmgwNumber,
		AlertReason:    AlertReasonUEPresent,
	})
	if err != nil {
		t.Fatalf("ReadyForSM: %v", err)
	}
	if a.Result != diameter.Experimental(DiameterErrorUserUnknown) {
		t.Errorf("answer result = %v", a.Result)
	}

	got, err := ParseReadyForSMRequest(c.request(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginHost != ipsmgwHost || got.SessionID == "" || got.DestinationRealm != realm ||
		got.UserIdentifier.MSISDN != msisdn || got.IPSMGWNumber != ipsmgwNumber || got.AlertReason != AlertReasonUEPresent {
		t.Errorf("the request was %+v", got)
	}
}

// TestS6cClientRefusesAReadyForSMTheFormatWouldNotCarry: a request
// without a realm, without one of the numbers or with a number that TBCD
// cannot hold, or with a reason of no name never goes out.
func TestS6cClientRefusesAReadyForSMTheFormatWouldNotCarry(t *testing.T) {
	c := connect(t, hss, ipsmgw, ApplicationIDS6c, func(request *diam.Message) *diam.Message {
		return ReadyForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(request, hss)
	})
	client := NewS6cClient(c.connecting, c.peer)
	good := ReadyForSMRequest{
		Header:         diameter.Header{DestinationRealm: realm},
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
		IPSMGWNumber:   ipsmgwNumber,
		AlertReason:    AlertReasonUEMemoryAvailable,
	}
	for name, change := range map[string]func(*ReadyForSMRequest){
		"names no realm":                 func(r *ReadyForSMRequest) { r.DestinationRealm = "" },
		"names the user by IMSI only":    func(r *ReadyForSMRequest) { r.UserIdentifier = UserIdentifier{UserName: imsi} },
		"has an MSISDN that is not E164": func(r *ReadyForSMRequest) { r.UserIdentifier.MSISDN = "+1 202 555 0112" },
		"has no IP-SM-GW-Number":         func(r *ReadyForSMRequest) { r.IPSMGWNumber = "" },
		"has a reason of no name":        func(r *ReadyForSMRequest) { r.AlertReason = 2 },
	} {
		r := good
		change(&r)
		if _, err := client.ReadyForSM(t.Context(), r); err == nil {
			t.Errorf("ReadyForSM sent a request that %s", name)
		}
	}
	c.noRequest(t)
}
