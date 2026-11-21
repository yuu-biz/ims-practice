package diameter

import (
	"bytes"
	"net"
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// roundTrip encodes m to wire format and decodes it again, as the peer
// on the other end would.
func roundTrip(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := diam.ReadMessage(&buf, dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMessagesRoundTripToEvents(t *testing.T) {
	local := Identity{OriginHost: hss, OriginRealm: realm}
	ip := net.IPv4(172, 20, 0, 2)
	ids := MessageIDs{HopByHop: 0x11, EndToEnd: 0x22}

	tests := []struct {
		name string
		msg  *diam.Message
		want Event
	}{
		{"CER", capabilitiesExchangeRequest(local, []uint32{ApplicationIDCx}, ip, ids),
			RConnCER{Request: ids, OriginHost: hss, ApplicationIDs: []uint32{ApplicationIDCx}}},
		{"CEA", capabilitiesExchangeAnswer(local, []uint32{ApplicationIDCx}, ip, ids, DiameterNoCommonApplication),
			RcvCEA{ResultCode: DiameterNoCommonApplication, OriginHost: hss, ApplicationIDs: []uint32{ApplicationIDCx}}},
		{"DWR", deviceWatchdogRequest(local, ids), RcvDWR{Request: ids}},
		{"DWA", deviceWatchdogAnswer(local, ids), RcvDWA{}},
		{"DPR", disconnectPeerRequest(local, ids, Busy), RcvDPR{Request: ids, Cause: Busy}},
		{"DPA", disconnectPeerAnswer(local, ids), RcvDPA{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseEvent(roundTrip(t, tt.msg))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("event = %#v, want %#v", got, tt.want)
			}
			if name := commandName(tt.msg); name != tt.name {
				t.Errorf("command name = %q, want %q", name, tt.name)
			}
		})
	}
}

// The CER is the first thing a learner reads in a capture, so check
// that it carries the AVPs of RFC 6733 5.3.1 in order.
func TestCapabilitiesExchangeRequestAVPs(t *testing.T) {
	local := Identity{OriginHost: scscf, OriginRealm: realm}
	m := roundTrip(t, capabilitiesExchangeRequest(local, []uint32{ApplicationIDCx}, net.IPv4(172, 20, 0, 3), MessageIDs{HopByHop: 1, EndToEnd: 1}))
	want := []string{
		"Origin-Host", "Origin-Realm", "Host-IP-Address", "Vendor-Id",
		"Product-Name", "Supported-Vendor-Id", "Vendor-Specific-Application-Id",
	}
	var got []string
	for _, a := range m.AVP {
		d, err := dict.Default.FindAVP(0, a.Code)
		if err != nil {
			t.Fatalf("AVP %d not in the base dictionary", a.Code)
		}
		got = append(got, d.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AVPs = %v, want %v", got, want)
	}
	if !isRequest(m) || m.Header.CommandCode != 257 || m.Header.ApplicationID != 0 {
		t.Errorf("header = %+v, want a request with Command-Code 257 and Application-ID 0", m.Header)
	}
	if a, err := m.FindAVP(avp.HostIPAddress, 0); err != nil || !net.IP(a.Data.(datatype.Address)).Equal(net.IPv4(172, 20, 0, 3)) {
		t.Errorf("Host-IP-Address = %v, want 172.20.0.3", a)
	}
	if a, err := m.FindAVP(avp.SupportedVendorID, 0); err != nil || a.Data.(datatype.Unsigned32) != datatype.Unsigned32(VendorID3GPP) {
		t.Errorf("Supported-Vendor-Id = %v, want %d", a, VendorID3GPP)
	}
}

// A node advertises one Vendor-Specific-Application-Id per application
// it supports, and nothing else.
func TestCapabilitiesAdvertiseEveryApplicationOfTheNode(t *testing.T) {
	local := Identity{OriginHost: hss, OriginRealm: realm}
	apps := []uint32{ApplicationIDCx, ApplicationIDSh}
	m := roundTrip(t, capabilitiesExchangeAnswer(local, apps, net.IPv4(172, 20, 0, 2), MessageIDs{}, DiameterSuccess))
	if got := applicationIDs(m); !reflect.DeepEqual(got, apps) {
		t.Errorf("advertised %v, want %v", got, apps)
	}
}
