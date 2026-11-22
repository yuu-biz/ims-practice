package diameter

import (
	"reflect"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// TestHeaderRoutingAVPs checks the order the Add methods write in, and
// that a request which names no node writes no Destination-Host at all,
// which TS 29.229 5.5 allows. Such a request reads back with an empty
// DestinationHost.
func TestHeaderRoutingAVPs(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	toRealm := Header{SessionID: "icscf.example;1;2", OriginHost: "icscf.example",
		OriginRealm: "example", DestinationRealm: "example"}
	toHost := toRealm
	toHost.DestinationHost = "hss.example"

	for _, c := range []struct {
		name   string
		header Header
		want   []uint32
	}{
		{"no Destination-Host", toRealm,
			[]uint32{avp.SessionID, avp.OriginHost, avp.OriginRealm, avp.DestinationRealm}},
		{"a named HSS", toHost,
			[]uint32{avp.SessionID, avp.OriginHost, avp.OriginRealm, avp.DestinationHost, avp.DestinationRealm}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := NewRequest(ApplicationIDCx, 300)
			c.header.AddSessionID(m)
			c.header.AddOrigin(m)
			c.header.AddDestinationHost(m)
			c.header.AddDestinationRealm(m)
			var got []uint32
			for _, a := range m.AVP {
				got = append(got, a.Code)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("AVP codes = %v, want %v", got, c.want)
			}
			var back Header
			if err := roundTrip(t, m).Unmarshal(&back); err != nil {
				t.Fatal(err)
			}
			if back != c.header {
				t.Errorf("read back %+v, want %+v", back, c.header)
			}
		})
	}
}

func TestNewRequestSetsTheRequestAndProxiableBits(t *testing.T) {
	m := NewRequest(ApplicationIDSh, 306)
	if m.Header.CommandFlags != diam.RequestFlag|diam.ProxiableFlag {
		t.Errorf("flags = %#x, want R and P", m.Header.CommandFlags)
	}
	if m.Header.CommandCode != 306 || m.Header.ApplicationID != ApplicationIDSh {
		t.Errorf("header = %+v, want Command-Code 306 and Application-Id %d", m.Header, ApplicationIDSh)
	}
	// The identifiers are left to go-diameter, which fills in both, so
	// that an answer can be paired with its request.
	if m.Header.HopByHopID == 0 || m.Header.EndToEndID == 0 {
		t.Errorf("Hop-by-Hop %d and End-to-End %d, want both filled in", m.Header.HopByHopID, m.Header.EndToEndID)
	}
}

// TestNewAnswerFollowsItsRequest pins what an answer takes from the
// request it answers (RFC 6733 6.2).
func TestNewAnswerFollowsItsRequest(t *testing.T) {
	request := NewRequest(ApplicationIDCx, 300)
	Header{SessionID: "icscf.example;1;2"}.AddSessionID(request)
	answer := NewAnswer(request)
	if answer.Header.CommandCode != request.Header.CommandCode ||
		answer.Header.ApplicationID != request.Header.ApplicationID ||
		answer.Header.HopByHopID != request.Header.HopByHopID ||
		answer.Header.EndToEndID != request.Header.EndToEndID {
		t.Errorf("answer header %+v, want the one of %+v", answer.Header, request.Header)
	}
	if answer.Header.CommandFlags != diam.ProxiableFlag {
		t.Errorf("flags = %#x, want P alone", answer.Header.CommandFlags)
	}
	a, err := answer.FindAVP(avp.SessionID, 0)
	if err != nil || string(a.Data.(datatype.UTF8String)) != "icscf.example;1;2" {
		t.Errorf("Session-Id = %v, %v; want the one of the request", a, err)
	}
}

// TestVendorAVPFlags: the table of AVP codes of each application says
// which of its AVPs set M beside V.
func TestVendorAVPFlags(t *testing.T) {
	// Public-Identity of TS 29.229 Table 6.3.0.1 sets M and V.
	if a := VendorAVP(601, avp.Mbit|avp.Vbit, datatype.UTF8String("sip:a@example")); a.Flags != avp.Mbit|avp.Vbit ||
		a.VendorID != VendorID3GPP || a.Code != 601 {
		t.Errorf("Public-Identity = code %d, flags %#x, vendor %d; want 601, 0xc0, %d", a.Code, a.Flags, a.VendorID, VendorID3GPP)
	}
	// Identity-Set of TS 29.329 Table 6.3.1 sets V and must not set M.
	if a := VendorAVP(708, avp.Vbit, datatype.Enumerated(0)); a.Flags != avp.Vbit || a.VendorID != VendorID3GPP {
		t.Errorf("Identity-Set = flags %#x, vendor %d; want 0x80, %d", a.Flags, a.VendorID, VendorID3GPP)
	}
}

// TestDictionaryLoadsOnce: several places call LoadDictionary, and
// loading the same XML twice would duplicate its definitions, so only
// the first call does the work and later ones return its result.
func TestDictionaryLoadsOnce(t *testing.T) {
	if err := LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	if err := LoadDictionary(); err != nil {
		t.Fatalf("the second call loaded again and failed: %v", err)
	}
	broken := NewDictionary([]byte("this is not a dictionary"))
	first := broken.Load()
	if first == nil {
		t.Fatal("a document that is not XML was loaded")
	}
	if second := broken.Load(); second != first {
		t.Errorf("the second Load returned %v, want the error of the first, %v", second, first)
	}
}
