package sgd

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

func TestAlertServiceCentreRoundTrip(t *testing.T) {
	alr := AlertServiceCentreRequest{
		Header:         Header{SessionID: hssHost + ";3;1", OriginHost: hssHost, OriginRealm: realm, DestinationHost: scHost, DestinationRealm: realm},
		SCAddress:      scAddress,
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
	}
	req := wire(t, alr.Message())
	if h := req.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandAlertServiceCentre ||
		h.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
		t.Errorf("ALR header = %+v", h)
	}
	got, err := ParseAlertServiceCentreRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, alr) {
		t.Errorf("ALR\n got %+v\nwant %+v", got, alr)
	}

	ala := AlertServiceCentreAnswer{Result: Base(DiameterSuccess)}
	ans := wire(t, ala.Message(req, sc))
	if h := ans.Header; h.ApplicationID != ApplicationIDS6c || h.CommandCode != CommandAlertServiceCentre ||
		h.CommandFlags != diam.ProxiableFlag || h.HopByHopID != req.Header.HopByHopID || h.EndToEndID != req.Header.EndToEndID {
		t.Errorf("ALA header = %+v", h)
	}
	gotA, err := ParseAlertServiceCentreAnswer(ans)
	if err != nil || !reflect.DeepEqual(gotA, ala) {
		t.Errorf("ALA: got %+v, %v, want %+v", gotA, err, ala)
	}
}

// TestAlertServiceCentreRequestBytes checks the encoding against the
// numbers of the specifications: the header and the AVP order of TS
// 29.338 5.3.2.5, the SC-Address of Table 6.3.3.1/1 and the
// User-Identifier of TS 29.336 6.4.2.
func TestAlertServiceCentreRequestBytes(t *testing.T) {
	b, err := AlertServiceCentreRequest{
		Header:         Header{SessionID: hssHost + ";3;1", OriginHost: hssHost, OriginRealm: realm, DestinationHost: scHost, DestinationRealm: realm},
		SCAddress:      scAddress,
		UserIdentifier: UserIdentifier{MSISDN: msisdn},
	}.Message().Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if flags, code, app := b[4], binary.BigEndian.Uint32(b[4:])&0xffffff, binary.BigEndian.Uint32(b[8:]); flags != 0xc0 || code != 8388648 || app != 16777312 {
		t.Fatalf("ALR header: flags %#x, command %d, application %d; want R and P set, 8388648, 16777312", flags, code, app)
	}
	avps := rawAVPs(t, b[20:])
	var order []uint32
	for _, a := range avps {
		order = append(order, a.code)
	}
	// Session-Id, Auth-Session-State, Origin-Host, Origin-Realm,
	// Destination-Host, Destination-Realm, SC-Address, User-Identifier.
	if want := []uint32{263, 277, 264, 296, 293, 283, 3300, 3102}; !reflect.DeepEqual(order, want) {
		t.Errorf("ALR AVP codes = %v, want %v", order, want)
	}
	if a := find(t, avps, 3300, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 01 F0")) {
		t.Errorf("SC-Address: flags %#x, data % X", a.flags, a.data)
	}
	user := find(t, avps, 3102, 10415)
	if user.flags != 0xc0 {
		t.Errorf("User-Identifier: flags %#x", user.flags)
	}
	if a := find(t, rawAVPs(t, user.data), 701, 10415); a.flags != 0xc0 || !bytes.Equal(a.data, mustHex(t, "21 20 55 05 11 F2")) {
		t.Errorf("MSISDN: flags %#x, data % X", a.flags, a.data)
	}
}

func TestParseAlertServiceCentreErrors(t *testing.T) {
	alr := func(avps ...*diam.AVP) *diam.Message {
		m := newRequest(ApplicationIDS6c, CommandAlertServiceCentre)
		Header{SessionID: "s", OriginHost: hssHost, OriginRealm: realm, DestinationHost: scHost, DestinationRealm: realm}.begin(m)
		for _, a := range avps {
			m.AddAVP(a)
		}
		return m
	}
	user := UserIdentifier{MSISDN: msisdn}.avp()
	for name, m := range map[string]*diam.Message{
		"without SC-Address":          alr(user),
		"without User-Identifier":     alr(vendorAVP(avpSCAddress, mv, encodeTBCD(scAddress))),
		"SC-Address that is not TBCD": alr(vendorAVP(avpSCAddress, mv, datatype.OctetString([]byte{0x0A})), user),
	} {
		if got, err := ParseAlertServiceCentreRequest(wire(t, m)); err == nil {
			t.Errorf("ALR %s: got %+v, want an error", name, got)
		}
	}
	bare := diam.NewMessage(CommandAlertServiceCentre, diam.ProxiableFlag, ApplicationIDS6c, 0, 0, alr().Dictionary())
	bare.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("s"))
	if got, err := ParseAlertServiceCentreAnswer(wire(t, bare)); err == nil {
		t.Errorf("ALA without a result: got %+v, want an error", got)
	}
}
